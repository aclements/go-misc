// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command ptype prints Go types from a binary using DWARF info.
package main

import (
	"debug/dwarf"
	"debug/elf"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s binary <type-regexp...>\n", os.Args[0])
	}
	flag.Parse()
	if flag.NArg() < 1 {
		flag.Usage()
		os.Exit(2)
	}
	binPath := flag.Arg(0)

	// Parse type regexp args.
	regexps := []*regexp.Regexp{}
	for _, tre := range flag.Args()[1:] {
		re, err := regexp.Compile("^" + tre)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad regexp %q: %s", tre, err)
			os.Exit(1)
		}
		regexps = append(regexps, re)
	}
	if len(regexps) == 0 {
		regexps = append(regexps, regexp.MustCompile(".*"))
	}

	// Parse binary.
	f, err := elf.Open(binPath)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	d, err := f.DWARF()
	if err != nil {
		log.Fatal(err)
	}

	// Find all of the named types.
	r := d.Reader()
	for {
		ent, err := r.Next()
		if err != nil {
			log.Fatal(err)
		}
		if ent == nil {
			break
		}

		if ent.Tag != dwarf.TagTypedef {
			continue
		}

		name, ok := ent.Val(dwarf.AttrName).(string)
		if !ok {
			continue
		}

		// Do we want this type?
		matched := false
		for _, re := range regexps {
			if re.MatchString(name) {
				matched = true
				break
			}
		}
		if isBuiltinName(name) || !matched {
			r.SkipChildren()
			continue
		}

		// Print the type.
		base, ok := ent.Val(dwarf.AttrType).(dwarf.Offset)
		if !ok {
			log.Printf("type %s has unknown underlying type", name)
			continue
		}

		typ, err := d.Type(base)
		if err != nil {
			log.Fatal(err)
		}

		pkg := ""
		if i := strings.LastIndex(name, "."); i >= 0 {
			pkg = name[:i+1]
		}

		p := &typePrinter{pkg: pkg}
		p.fmt("type %s ", name)
		p.printType(typ)
		p.fmt("\n\n")

		r.SkipChildren()
	}
}

func isBuiltinName(typeName string) bool {
	switch typeName {
	case "string":
		return true
	}
	return strings.HasPrefix(typeName, "map[") ||
		strings.HasPrefix(typeName, "func(") ||
		strings.HasPrefix(typeName, "chan ") ||
		strings.HasPrefix(typeName, "chan<- ") ||
		strings.HasPrefix(typeName, "<-chan ")
}

type typePrinter struct {
	offset []int64
	depth  int
	nameOk int
	pkg    string

	// pos is the current character position on this line.
	pos int

	// lineComment is a comment to print at the end of this line.
	lineComment string
}

func (p *typePrinter) fmt(f string, args ...interface{}) {
	b := fmt.Sprintf(f, args...)
	if strings.IndexAny(b, "\n\t") < 0 {
		fmt.Printf("%s", b)
		p.pos += utf8.RuneCountInString(b)
		return
	}
	lines := strings.Split(b, "\n")
	for i, line := range lines {
		hasNL := i < len(lines)-1
		if p.lineComment == "" && hasNL {
			// Fast path for complete lines with no comment.
			fmt.Printf("%s\n", line)
			p.pos = 0
			continue
		}

		for _, r := range line {
			if r == '\t' {
				p.pos = (p.pos + 8) &^ 7
			} else {
				p.pos++
			}
		}
		fmt.Printf("%s", line)
		if hasNL {
			if p.lineComment != "" {
				space := 50 - p.pos
				if space < 1 {
					space = 1
				}
				fmt.Printf("%*s// %s", space, "", p.lineComment)
				p.lineComment = ""
			}
			fmt.Printf("\n")
			p.pos = 0
		}
	}
}

func (p *typePrinter) setLineComment(f string, args ...interface{}) {
	if p.lineComment != "" {
		panic("multiple line comments")
	}
	p.lineComment = fmt.Sprintf(f, args...)
}

func (p *typePrinter) stripPkg(name string) string {
	if p.pkg != "" && strings.HasPrefix(name, p.pkg) {
		return name[len(p.pkg):]
	}
	return name
}

func (p *typePrinter) printType(typ dwarf.Type) {
	if p.offset == nil {
		p.offset = []int64{0}
	}

	if p.nameOk > 0 && typ.Common().Name != "" {
		p.fmt("%s", p.stripPkg(typ.Common().Name))
		p.offset[len(p.offset)-1] += typ.Size()
		return
	}

	switch typ := typ.(type) {
	case *dwarf.ArrayType:
		if typ.Count < 0 {
			p.fmt("[incomplete]")
		} else {
			p.fmt("[%d]", typ.Count)
		}
		if typ.StrideBitSize > 0 {
			p.fmt("/* %d bit element */", typ.StrideBitSize)
		}
		origOffset := p.offset
		p.offset = append(p.offset, typ.Type.Size(), 0)
		p.printType(typ.Type)
		p.offset = origOffset

	case *dwarf.StructType:
		if typ.StructName != "" && (p.nameOk > 0 || isBuiltinName(typ.StructName)) {
			p.fmt("%s", p.stripPkg(typ.StructName))
			break
		}

		if strings.HasPrefix(typ.StructName, "[]") {
			p.fmt("[]")
			elem := typ.Field[0].Type
			origOffset := p.offset
			p.offset = append(p.offset, elem.Size(), 0)
			p.printType(elem)
			p.offset = origOffset
			break
		}

		if typ.StructName == "runtime.eface" {
			p.fmt("interface{}")
			break
		} else if typ.StructName == "runtime.iface" {
			p.fmt("interface{ ... }")
			break
		}

		isUnion := typ.Kind == "union"
		p.fmt("%s {", typ.Kind)
		if typ.Incomplete {
			p.fmt(" incomplete ")
		}
		p.depth++
		startOffset := p.offset[len(p.offset)-1]
		var prevEnd int64
		for i, f := range typ.Field {
			indent := "\n" + strings.Repeat("\t", p.depth)
			p.fmt(indent)
			// TODO: Bit offsets?
			if !isUnion {
				offset := startOffset + f.ByteOffset
				if i > 0 && prevEnd < offset {
					p.fmt("// %d byte gap", offset-prevEnd)
					p.fmt(indent)
				}
				p.offset[len(p.offset)-1] = offset
				p.setLineComment("offset %s", p.strOffset())
				if f.Type.Size() < 0 {
					// Who knows. Give up.
					// TODO: This happens for funcs.
					prevEnd = (1 << 31) - 1
				} else {
					prevEnd = offset + f.Type.Size()
				}
			}
			p.fmt("%s ", f.Name)
			p.printType(f.Type)
			if f.BitSize != 0 {
				p.fmt(" : %d", f.BitSize)
			}
		}
		p.offset[len(p.offset)-1] = startOffset
		p.depth--
		if len(typ.Field) == 0 {
			p.fmt("}")
		} else {
			p.fmt("\n%s}", strings.Repeat("\t", p.depth))
		}

	case *dwarf.EnumType:
		p.fmt("enum") // TODO

	case *dwarf.BoolType, *dwarf.CharType, *dwarf.ComplexType, *dwarf.FloatType, *dwarf.IntType, *dwarf.UcharType, *dwarf.UintType:
		// Basic types.
		p.fmt("%s", typ.String())

	case *dwarf.PtrType:
		origOffset := p.offset
		p.offset = []int64{0}
		p.nameOk++
		p.fmt("*")
		p.printType(typ.Type)
		p.nameOk--
		p.offset = origOffset

	case *dwarf.FuncType:
		// TODO: Expand ourselves so we can clean up argument
		// types, etc.
		p.fmt(typ.String())

	case *dwarf.QualType:
		p.fmt("/* %s */ ", typ.Qual)
		p.printType(typ.Type)

	case *dwarf.TypedefType:
		n := typ.Common().Name
		if isBuiltinName(n) {
			// TODO: Make Go-ifying optional.
			//
			// TODO: Expand map types ourselves if
			// possible so we can clean up the type names.
			p.fmt("%s", n)
			return
		}

		real := typ.Type
		for {
			if real2, ok := real.(*dwarf.TypedefType); ok {
				real = real2.Type
			} else {
				break
			}
		}
		if str, ok := real.(*dwarf.StructType); ok {
			switch str.StructName {
			case "runtime.iface", "runtime.eface":
				// Named interface type.
				p.fmt(p.stripPkg(n))
				return
			}
		}

		// TODO: If it's "type x map..." or similar, we never
		// see the "map[...]" style name and only see that x's
		// underlying type is a pointer to a struct named
		// "hash<...>".

		p.fmt("/* %s */ ", p.stripPkg(n))
		p.printType(real)

	case *dwarf.UnspecifiedType:
		p.fmt("unspecified")

	case *dwarf.VoidType:
		p.fmt("void")
	}

	p.offset[len(p.offset)-1] += typ.Size()
}

func (p *typePrinter) strOffset() string {
	buf := fmt.Sprintf("%d", p.offset[0])
	for i, idx := 1, 'i'; i < len(p.offset); i, idx = i+2, idx+1 {
		buf += fmt.Sprintf(" + %d*%c", p.offset[i], idx)
		if p.offset[i+1] != 0 {
			buf += fmt.Sprintf(" + %d", p.offset[i+1])
		}
	}
	return buf
}
