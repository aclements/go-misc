package main

import (
	"cmp"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type NormOptions struct {
	// NameFields matches the name, with named subexpressions "B", "N", and "L"
	// matching the base, element bits, and lane count fields respectively.
	//
	// If nil, no name normalization is done.
	NameFields *regexp.Regexp

	Name NormUnitFlags // Defaults to NormOutput

	Ins []NormUnitFlags // Defaults to NormInput

	Out NormUnitFlags // Defaults to NormInput
}

// NormUnitFlags holds the flags for a single "unit", such as a function name or
// type.
type NormUnitFlags struct {
	All     NormFieldFlags
	B, N, L NormFieldFlags // Defaults to All if unset

	// SwapNL indicates that the roles of "N" and "L" should be swapped.
	SwapNL bool
}

// NormFieldFlags holds the flags for a single "field", such as a base name,
type NormFieldFlags int

const (
	// NormInput, NormOutput, and NormControl indicate the domain of a
	// normalization field.
	NormInput NormFieldFlags = 1 << iota
	NormOutput
	NormControl

	// NormRoundUp indicates that this field can be rounded up to make a type
	// possible. Can be applied to element bits or lane count.
	NormRoundUp

	// NormDiv8 indicates that this field can match x/8.
	NormDiv8

	NormDefault NormFieldFlags = 0
)

func (u NormUnitFlags) WithDefaults(allDefault NormFieldFlags) NormUnitFlags {
	u.All = cmp.Or(u.All, allDefault)
	u.B = cmp.Or(u.B, u.All)
	u.N = cmp.Or(u.N, u.All)
	u.L = cmp.Or(u.L, u.All)
	return u
}

func (o NormOptions) WithDefaults() NormOptions {
	o.Name = o.Name.WithDefaults(NormOutput)
	o.Ins = slices.Clone(o.Ins)
	for i := range o.Ins {
		o.Ins[i] = o.Ins[i].WithDefaults(NormInput)
	}
	o.Out = o.Out.WithDefaults(NormInput)
	return o
}

type NormalizedMethod struct {
	// Method is the original raw method metadata.
	Method *MethodMeta
	// NormName is the canonicalized operation name (e.g., "Abs", "ConvertToFloat32").
	NormName string
	// NormSig is the canonicalized signature string (e.g., "() T", "() Float32x<M>").
	NormSig string
	// InRounded indicates that an argument type was expanded to hit the minimum width.
	InRounded bool
	// OutRounded indicates that a result type was expanded.
	OutRounded bool
}

func normalizeMethods(typeGroups []*TypeGroup, fset *token.FileSet) []*NormalizedMethod {
	var result []*NormalizedMethod

	for _, tg := range typeGroups {
		switch tg.Type.Kind {
		case TypeVec, TypeMask:
		default:
			panic("bad type group kind")
		}
		for _, m := range tg.Funcs {
			result = append(result, m.normalize(fset))
		}
	}

	return result
}

func (m *MethodMeta) normalize(fset *token.FileSet) *NormalizedMethod {
	options := m.NormOptions().WithDefaults()

	// Normalize name
	normName := m.Name
	if options.NameFields != nil {
		ns, ok := NewNormString(m.Name, options.NameFields)
		if !ok {
			panic(fmt.Sprintf("method name %q does not match %s", m.Name, options.NameFields))
		}
		normName, _ = normalizeUnit(ns, m.Receiver, options.Name)
	}
	// Even if we don't have detailed fields for the name, we can replace a whole
	// vector receiver with <T>.
	if options.Name.B&options.Name.N&options.Name.L&NormInput != 0 {
		normName = strings.ReplaceAll(normName, m.Receiver.String, "<T>")
	}

	// Normalize types
	ft := cloneFuncType(fset, m.Decl.Type)
	inRounded := false
	if ft.Params != nil {
		for i, paramGroup := range ft.Params.List {
			inOpts := NormUnitFlags{}.WithDefaults(NormInput)
			if i < len(options.Ins) {
				inOpts = options.Ins[i]
			}
			if normalizeType(paramGroup.Type, m.Receiver, inOpts) {
				inRounded = true
			}
		}
	}
	outRounded := false
	if ft.Results != nil {
		for _, resultGroup := range ft.Results.List {
			if normalizeType(resultGroup.Type, m.Receiver, options.Out) {
				outRounded = true
			}
		}
	}

	normSig := nodeToString(fset, ft)

	return &NormalizedMethod{
		Method:     m,
		NormName:   normName,
		NormSig:    normSig,
		InRounded:  inRounded,
		OutRounded: outRounded,
	}
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

func normalizeType(typ ast.Expr, input ParsedType, options NormUnitFlags) bool {
	var maskType string
	if input.Kind == TypeVec {
		maskType = input.With("Mask", "", "")
	}

	rounded := false
	ast.Inspect(typ, func(n ast.Node) bool {
		// Match arrays
		if arr, ok := n.(*ast.ArrayType); ok && arr.Len != nil {
			if lit, ok := arr.Len.(*ast.BasicLit); ok && lit.Kind == token.INT {
				if options.L&NormInput != 0 {
					val, _ := strconv.Atoi(lit.Value)
					if val == input.Lanes() {
						arr.Len = &ast.Ident{Name: "M"}
					}
				}
			}
		}

		// Match named types
		if ident, ok := n.(*ast.Ident); ok {
			if ident.Name == maskType {
				// Mask type implied by input type
				ident.Name = "Mask"
				return true
			}

			t := parseType(ident.Name)
			if t.Kind == "" {
				return true
			}

			r1 := false
			ident.Name, r1 = normalizeUnit(t.NormString, input, options)
			rounded = rounded || r1
		}
		return true
	})

	return rounded
}

// normalizeUnit normalizes a single "unit" such as a type or function name.
func normalizeUnit(unit NormString, input ParsedType, options NormUnitFlags) (string, bool) {
	rounded := false
	var B, N, L string

	if options.B&NormInput != 0 {
		switch unit.Base() {
		case strings.ToLower(input.Base()):
			// Check this first in case input is already a built-in and thus
			// lower-case.
			B = "<b>"
		case input.Base():
			B = "<B>"
		}
	}

	if options.SwapNL {
		unit.N, unit.L = unit.L, unit.N
	}

	minSize := 0
	switch input.Kind {
	case TypeMask:
		minSize = 8 // Basic type width
	case TypeVec:
		minSize = 128
	}

	if options.N&NormInput != 0 {
		want := input.ElemBits()
		r1 := options.N&NormRoundUp != 0 && unit.Lanes()*want < minSize
		if r1 {
			want = minSize / unit.Lanes()
		}
		switch unit.ElemBits() {
		case want:
			N = "<N>"
		case want / 2:
			N = "<N/2>"
		case want * 2:
			N = "<N*2>"
		}
		rounded = rounded || (r1 && N != "")
	}

	if options.L&NormInput != 0 && unit.Lanes() != 1 {
		want := input.Lanes()
		r1 := options.L&NormRoundUp != 0 && unit.ElemBits()*want < minSize
		if r1 {
			want = minSize / unit.ElemBits()
		}
		switch unit.Lanes() {
		case want:
			L = "<L>"
		case want / 2:
			L = "<L/2>"
		case want * 2:
			L = "<L*2>"
		case want / 8:
			if options.L&NormDiv8 != 0 {
				L = "<L/8>"
			}
		}
		rounded = rounded || (r1 && L != "")
	}

	if B == "<B>" && N == "<N>" && L == "<L>" {
		return "T", rounded
	}
	norm := unit.With(B, N, L)

	// Finally, remove any "><" so we don't get annoying things "<b><N>".
	norm = strings.ReplaceAll(norm, "><", "")
	return norm, rounded
}
