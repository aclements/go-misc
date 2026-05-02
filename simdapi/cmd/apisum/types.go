package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type ParsedType struct {
	Kind  TypeKind
	Name  string
	Base  string // Uint, Int, Float, Mask (even for basic types)
	Bits  int    // Element bit width
	Lanes int    // Lanes; 1 for basic type
}

type TypeKind string

const (
	TypeBasic TypeKind = "basic"
	TypeVec   TypeKind = "vec"
	TypeMask  TypeKind = "mask"
)

var (
	pureVecRegex = regexp.MustCompile(`^(Int|Uint|Float)(8|16|32|64)x(2|4|8|16|32|64)$`)
)

func parseType(name string) ParsedType {
	if m := pureVecRegex.FindStringSubmatch(name); len(m) > 1 {
		bits, _ := strconv.Atoi(m[2])
		lanes, _ := strconv.Atoi(m[3])
		return ParsedType{Kind: TypeVec, Name: name, Base: m[1], Bits: bits, Lanes: lanes}
	}
	if m := pureMaskRegex.FindStringSubmatch(name); len(m) > 1 {
		bits, _ := strconv.Atoi(m[1])
		lanes, _ := strconv.Atoi(m[2])
		return ParsedType{Kind: TypeMask, Name: name, Base: "Mask", Bits: bits, Lanes: lanes}
	}
	if m := basicRegex.FindStringSubmatch(name); len(m) > 1 {
		bits, _ := strconv.Atoi(m[2])
		base := strings.ToUpper(m[1][:1]) + m[1][1:]
		return ParsedType{Kind: TypeBasic, Name: name, Base: base, Bits: bits, Lanes: 1}
	}
	return ParsedType{Name: name}
}

// getElemType returns the basic Go type representing an element of t.
func (t *ParsedType) getElemType() string {
	switch t.Base {
	case "Int", "Uint", "Float":
		return fmt.Sprintf("%s%d", strings.ToLower(t.Base), t.Bits)
	}
	return ""
}
