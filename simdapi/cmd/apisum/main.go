// apisum parses and aggregates Go SIMD vector and mask type signatures from
// an internal Go SIMD architecture package (such as archsimd).
//
// The program operates in four main phases:
//  1. AST Parsing: Reads exportable functions and methods into MethodMeta metadata structs.
//  2. Classification: Categorizes rows into logical groupings (e.g., Mask, Crypto, Numeric conversion).
//  3. Normalization: Discovers equivalence classes across methods by canonicalizing
//     identifiers, types, and lane counts (accounting for 128-bit hardware limits).
//     AST nodes are safely cloned during this process to guarantee non-destructive iteration.
//  4. Aggregation & Output: Sorts deduplicated rows and renders formatted Markdown
//     implementation tables complete with hardware-caveat footnotes.
package main

import (
	"bytes"
	"go/ast"
	"go/doc"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"path/filepath"
	"regexp"
	"strings"
)

type TypeGroup struct {
	Name  string // Type name
	Type  ParsedType
	Funcs []*MethodMeta // Methods and type constructors
}

type MethodMeta struct {
	Name     string
	IsMethod bool
	Receiver ParsedType
	Params   []ParsedType
	Results  []ParsedType
	Decl     *ast.FuncDecl
}

var (
	basicRegex    = regexp.MustCompile(`\b(int|uint|float)(8|16|32|64)\b`)
	pureMaskRegex = regexp.MustCompile(`^Mask(8|16|32|64)x(2|4|8|16|32|64)$`)
	baseRegex     = regexp.MustCompile(`^(Int|Uint|Float|Mask)(\d+)$`)
)

func getCategory(funcName string, isMask bool) string {
	if isMask {
		return "Mask"
	}
	if strings.HasPrefix(funcName, "AES") || strings.HasPrefix(funcName, "SHA") {
		return "Crypto"
	}
	if strings.HasPrefix(funcName, "As") {
		return "Reinterpretation"
	}
	if strings.HasPrefix(funcName, "ConvertTo") || strings.HasPrefix(funcName, "TruncateTo") || strings.HasPrefix(funcName, "SaturateTo") || strings.HasPrefix(funcName, "ExtendTo") || strings.HasPrefix(funcName, "ExtendLo") {
		return "Numeric conversion"
	}
	if funcName == "Equal" || funcName == "Less" || funcName == "LessEqual" || funcName == "Greater" || funcName == "GreaterEqual" || funcName == "NotEqual" {
		return "Comparison"
	}
	return "Ungrouped"
}

func (m *MethodMeta) GetCategory() string {
	isMask := m.Receiver.Base == "Mask"
	return getCategory(m.Name, isMask)
}

func (m *MethodMeta) ComputeFlags() NormalizeFlag {
	var f NormalizeFlag

	category := m.GetCategory()

	if category == "Reinterpretation" {
		return NormKeepAll
	}

	if strings.Contains(m.Name, "Broadcast1To") {
		f |= NormResultKeepLanes
	}

	if category == "Numeric conversion" {
		f |= NormResultKeepLanesExact | NormResultKeepBase | NormResultKeepBits | NormNameKeepBase | NormNameKeepBits
	}

	if strings.Contains(m.Name, "GetElem") || strings.Contains(m.Name, "SetElem") || strings.Contains(m.Name, "Select") || strings.Contains(m.Name, "PermuteScalars") {
		f |= NormParamKeepUint8
	}

	if strings.Contains(m.Name, "Shift") || strings.Contains(m.Name, "Rotate") {
		f |= NormParamKeepUint64
	}

	if m.Decl != nil && m.Decl.Type.Params != nil {
		for _, field := range m.Decl.Type.Params.List {
			for _, name := range field.Names {
				if strings.Contains(name.Name, "prec") {
					f |= NormParamKeepUint8
				}
			}
		}
	}

	if category == "Mask" && strings.Contains(m.Name, "FromBits") && m.Receiver.Lanes < 8 {
		f |= NormForceMaskLanes
	}

	return f
}

func cloneFuncType(fset *token.FileSet, ft *ast.FuncType) *ast.FuncType {
	sig := nodeToString(fset, ft)
	dummy := "package main\nfunc _" + sig
	f, err := parser.ParseFile(fset, "", dummy, 0)
	if err != nil {
		return ft
	}
	for _, decl := range f.Decls {
		if fd, ok := decl.(*ast.FuncDecl); ok {
			return fd.Type
		}
	}
	return ft
}

func main() {
	// TODO: Take the package directory on the command line, defaulting to `go env GOROOT`
	pkgDir := "/home/austin/go.tmp/src/simd/archsimd"
	fset, docPkg := parsePkg(pkgDir)
	typeMap := parseFuncs(docPkg, fset)
	methods := normalizeMethods(typeMap, fset)
	extMeta := groupMethods(methods)
	printMethodBlocks(extMeta)
}

func parsePkg(pkgDir string) (*token.FileSet, *doc.Package) {
	pattern := filepath.Join(pkgDir, "*.go")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		log.Fatalf("Failed to glob files in %s: %v", pkgDir, err)
	}

	fset := token.NewFileSet()
	var files []*ast.File
	for _, path := range matches {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		astFile, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			log.Fatalf("Failed to parse file %s: %v", path, err)
		}
		files = append(files, astFile)
	}

	docPkg, err := doc.NewFromFiles(fset, files, "simd/archsimd", doc.AllDecls)
	if err != nil {
		log.Fatalf("Failed to create doc package: %v", err)
	}
	return fset, docPkg
}

func parseFuncs(docPkg *doc.Package, fset *token.FileSet) []*TypeGroup {
	parseFieldList := func(list *ast.FieldList) []ParsedType {
		if list == nil {
			return nil
		}
		var out []ParsedType
		for _, field := range list.List {
			typeStr := nodeToString(fset, field.Type)
			out = append(out, parseType(typeStr))
		}
		return out
	}
	parseFunc := func(tg *TypeGroup, f *doc.Func) {
		if !ast.IsExported(f.Name) {
			return
		}

		params := parseFieldList(f.Decl.Type.Params)
		results := parseFieldList(f.Decl.Type.Results)

		tg.Funcs = append(tg.Funcs, &MethodMeta{
			Name:     f.Name,
			IsMethod: f.Decl.Recv != nil,
			Receiver: tg.Type,
			Params:   params,
			Results:  results,
			Decl:     f.Decl,
		})
	}

	var groups []*TypeGroup
	for _, t := range docPkg.Types {
		typeParsed := parseType(t.Name)
		if !(typeParsed.Kind == TypeVec || typeParsed.Kind == TypeMask) {
			continue
		}

		tg := &TypeGroup{Name: t.Name, Type: typeParsed}
		groups = append(groups, tg)

		for _, f := range t.Funcs {
			parseFunc(tg, f)
		}

		for _, m := range t.Methods {
			parseFunc(tg, m)
		}
	}
	return groups
}

func nodeToString(fset *token.FileSet, node ast.Node) string {
	var buf bytes.Buffer
	if err := format.Node(&buf, fset, node); err != nil {
		return ""
	}
	return buf.String()
}
