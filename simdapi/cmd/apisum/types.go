package main

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type ParsedType struct {
	Kind TypeKind

	// Base is Uint, Int, Float, Mask, uint, int, float

	NormString
}

type TypeKind string

const (
	TypeBasic TypeKind = "basic"
	TypeVec   TypeKind = "vec"
	TypeMask  TypeKind = "mask"
)

var (
	vecRegex   = regexp.MustCompile(`^(?<B>Int|Uint|Float|Mask)(?<N>8|16|32|64)x(?<L>2|4|8|16|32|64)$`)
	basicRegex = regexp.MustCompile(`^(?<B>int|uint|float)(?<N>8|16|32|64)$`)
)

func parseType(name string) ParsedType {
	if ns, ok := NewNormString(name, vecRegex); ok {
		switch ns.Base() {
		case "Mask":
			return ParsedType{Kind: TypeMask, NormString: ns}
		default:
			return ParsedType{Kind: TypeVec, NormString: ns}
		}
	}
	if ns, ok := NewNormString(name, basicRegex); ok {
		return ParsedType{Kind: TypeBasic, NormString: ns}
	}
	return ParsedType{NormString: NormString{String: name}}
}

// NormString is a string with marked fields for base type (B), element bits
// (N), and lanes (L).
type NormString struct {
	String  string
	B, N, L []int // nil or start,end indexes
}

// NewNormString matches s against re to mark the B, N, and L fields. re may
// have capture groups named "B", "N", and "L" and will be implicitly anchored
// to match the whole string.
func NewNormString(s string, re *regexp.Regexp) (NormString, bool) {
	m := re.FindStringSubmatchIndex(s)
	if m == nil || m[0] != 0 || m[1] != len(s) {
		return NormString{}, false
	}
	ns := NormString{String: s}
	for i, name := range re.SubexpNames() {
		switch name {
		case "B":
			ns.B = m[i*2:][:2]
		case "N":
			ns.N = m[i*2:][:2]
		case "L":
			ns.L = m[i*2:][:2]
		case "":
		default:
			panic(fmt.Sprintf("unknown subexpression name %q", name))
		}
	}
	return ns, true
}

func (n NormString) Base() string {
	if n.B == nil {
		return ""
	}
	return n.String[n.B[0]:n.B[1]]
}

func (n NormString) ElemBits() int {
	if n.N == nil {
		return 1
	}
	x, _ := strconv.Atoi(n.String[n.N[0]:n.N[1]])
	return x
}

func (n NormString) Lanes() int {
	if n.L == nil {
		return 1
	}
	x, _ := strconv.Atoi(n.String[n.L[0]:n.L[1]])
	return x
}

// With replaces B, N, and L in the input string. Any of these can be "",
// indicating no replacement.
func (n NormString) With(B, N, L string) string {
	type part struct {
		idx  []int
		repl string
	}
	var parts []part
	addPart := func(name string, idx []int, repl string) {
		if repl == "" {
			return
		}
		if idx == nil {
			panic(fmt.Sprintf("cannot replace %s missing from %q", name, n.String))
		}
		parts = append(parts, part{idx, repl})
	}
	addPart("B", n.B, B)
	addPart("N", n.N, N)
	addPart("L", n.L, L)
	if len(parts) == 0 {
		return n.String
	}
	slices.SortFunc(parts, func(a, b part) int {
		return cmp.Compare(a.idx[0], b.idx[0])
	})
	var buf strings.Builder
	last := 0
	for _, part := range parts {
		buf.WriteString(n.String[last:part.idx[0]])
		buf.WriteString(part.repl)
		last = part.idx[1]
	}
	buf.WriteString(n.String[last:])
	return buf.String()
}
