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
	"flag"
	"fmt"
	"go/ast"
	"go/doc"
	"go/format"
	"go/parser"
	"go/token"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
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
	// XXX Delete
	baseRegex = regexp.MustCompile(`^(Int|Uint|Float|Mask)(\d+)$`)
)

func (m *MethodMeta) GetCategory() string {
	isMask := m.Receiver.Kind == TypeMask
	if isMask {
		return "Mask"
	}
	if (!m.IsMethod && (strings.HasPrefix(m.Name, "Load") || strings.HasPrefix(m.Name, "Broadcast"))) || strings.HasPrefix(m.Name, "Store") {
		return "Load/Store"
	}
	if hasAnyPrefix(m.Name, "Abs", "Add", "Average", "Div", "DotProduct", "Max", "Min", "Mul", "Reciprocal", "Scale", "Sqrt", "Sub", "Sum") {
		return "Math"
	}
	if hasAnyPrefix(m.Name, "ConvertTo", "TruncateTo", "SaturateTo", "ExtendTo", "ExtendLo") {
		return "Numeric conversion"
	}
	if anyOf(m.Name, "Equal", "Less", "LessEqual", "Greater", "GreaterEqual", "NotEqual", "IsNaN", "IsZero") {
		return "Comparison"
	}
	if hasAnyPrefix(m.Name, "Ceil", "Floor", "RoundToEven", "Trunc") {
		return "Rounding"
	}
	if anyOf(m.Name, "And", "AndNot", "Not", "Or", "Xor") {
		return "Logical"
	}
	if hasAnyPrefix(m.Name, "CarrylessMultiply", "GaloisField", "LeadingZeros", "OnesCount") {
		return "Bit math"
	}
	if hasAnyPrefix(m.Name, "Permute", "ConcatPermute", "Compress", "Expand", "Interleave", "Select") {
		return "Shuffles"
	}
	if hasAnyPrefix(m.Name, "Shift", "Rotate", "ConcatShift") {
		return "Shifts"
	}
	if hasAnyPrefix(m.Name, "AES", "SHA") {
		return "Crypto"
	}
	if hasAnyPrefix(m.Name, "As") {
		return "Reinterpretation"
	}
	return "Ungrouped"
}

func anyOf(s string, options ...string) bool {
	return slices.Contains(options, s)
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func (m *MethodMeta) NormOptions() NormOptions {
	category := m.GetCategory()

	type f = NormUnitFlags

	// TODO: The AES* sizes are weird. Are they controls?

	if category == "Load/Store" {
		// Name is usually in the "output" domain, but since these are constructors,
		// it's in the "input" domain.
		return NormOptions{Name: f{All: NormInput}}
	}

	if category == "Reinterpretation" {
		// Name and result are in output domain
		return NormOptions{Name: f{All: NormOutput}, Out: f{All: NormOutput}}
	}

	if strings.HasPrefix(m.Name, "Broadcast1To") {
		// Result B and N are input domain; name L and result L are output domain
		return NormOptions{
			NameFields: regexp.MustCompile(`Broadcast1To(?<L>\d+)`),
			Name:       f{L: NormOutput},
			Out:        f{L: NormOutput},
		}
	}

	if category == "Numeric conversion" {
		// Name B N and result B N are output domain. Result L is input domain rounding allowed
		return NormOptions{
			NameFields: regexp.MustCompile(`.*To(?<B>[a-zA-Z]+)(?<N>\d+).*`),
			Name:       f{B: NormOutput, N: NormOutput},
			Out:        f{B: NormOutput, N: NormOutput, L: NormInput | NormRoundUp},
		}
	}

	if m.Name == "GetElem" || m.Name == "SetElem" || strings.HasPrefix(m.Name, "Select") || strings.HasPrefix(m.Name, "PermuteScalars") {
		// First param group is control domain
		//
		// TODO: Most of these should have obvious parameter names, but not all do
		return NormOptions{
			Ins: []f{{All: NormControl}},
		}
	}

	if strings.HasPrefix(m.Name, "Shift") || strings.HasPrefix(m.Name, "Rotate") {
		// If first param group is a basic type, it's control domain
		//
		// TODO: Always call this "shift" or "by" or "bits"
		if m.Params[0].Kind == TypeBasic {
			return NormOptions{
				Ins: []f{{All: NormControl}},
			}
		}
	}

	if strings.HasPrefix(m.Name, "GaloisFieldAffineTransform") {
		// First param BN is control; lanes is L/8 (!)
		return NormOptions{
			Ins: []f{{B: NormControl, N: NormControl, L: NormInput | NormDiv8}},
		}
	}

	if strings.HasPrefix(m.Name, "CarrylessMultiply") {
		// First param group is a (very silly!) control.
		return NormOptions{
			Ins: []f{{All: NormControl}},
		}
	}

	if category == "Mask" {
		if strings.HasSuffix(m.Name, "FromBits") {
			// Param is input domain, width rounding allowed. The element width matches
			// the number of lanes.
			return NormOptions{
				Name: f{All: NormInput},
				Ins:  []f{{L: NormInput | NormRoundUp, SwapNL: true}},
			}
		}
		if strings.HasPrefix(m.Name, "ToBits") {
			// Result is input domain, width rounding allowed. The element width
			// matches the number of lanes.
			return NormOptions{
				Out: f{L: NormInput | NormRoundUp, SwapNL: true},
			}
		}
	}

	if m.Decl != nil && m.Decl.Type.Params != nil {
		ins := make([]NormUnitFlags, len(m.Decl.Type.Params.List))
		for i, list := range m.Decl.Type.Params.List {
			switch list.Names[0].Name {
			case "prec", "shift": // control
				ins[i].All = NormControl
			case "indices": // Uint<N>x<L> control
				ins[i].B = NormControl
			case "rconVal", "constant": // Cypto
				ins[i].All = NormControl
			}
		}
		return NormOptions{Ins: ins}
	}

	return NormOptions{}
}

func main() {
	flag.Usage = func() {
		out := flag.CommandLine.Output()
		fmt.Fprintf(out, "Usage: %s [pkgDir]\n\n", os.Args[0])
		fmt.Fprintf(out, "Parses and aggregates Go SIMD vector and mask type signatures.\n\n")
		fmt.Fprintf(out, "Arguments:\n")
		fmt.Fprintf(out, "  pkgDir   Path to the internal archsimd package. Defaults to $GOROOT/src/simd/archsimd.\n")
	}

	flag.Parse()
	if flag.NArg() > 1 {
		flag.Usage()
		os.Exit(2)
	}

	pkgDir := flag.Arg(0)
	if pkgDir == "" {
		out, err := exec.Command("go", "env", "GOROOT").Output()
		if err != nil {
			log.Fatalf("Failed to get GOROOT: %v", err)
		}
		goroot := strings.TrimSpace(string(out))
		pkgDir = filepath.Join(goroot, "src", "simd", "archsimd")
	}

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
