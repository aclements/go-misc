// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command dumptype prints Go types from a binary using DWARF info.
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

		fmt.Printf("type %s ", name)
		(&typePrinter{pkg: pkg}).printType(typ)
		fmt.Printf("\n\n")

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
		fmt.Print(p.stripPkg(typ.Common().Name))
		p.offset[len(p.offset)-1] += typ.Size()
		return
	}

	switch typ := typ.(type) {
	case *dwarf.ArrayType:
		if typ.Count < 0 {
			fmt.Print("[incomplete]")
		} else {
			fmt.Printf("[%d]", typ.Count)
		}
		if typ.StrideBitSize > 0 {
			fmt.Printf("/* %d bit element */", typ.StrideBitSize)
		}
		origOffset := p.offset
		p.offset = append(p.offset, typ.Type.Size(), 0)
		p.printType(typ.Type)
		p.offset = origOffset

	case *dwarf.StructType:
		if typ.StructName != "" && (p.nameOk > 0 || isBuiltinName(typ.StructName)) {
			fmt.Print(p.stripPkg(typ.StructName))
			break
		}

		if strings.HasPrefix(typ.StructName, "[]") {
			fmt.Print("[]")
			elem := typ.Field[0].Type
			origOffset := p.offset
			p.offset = append(p.offset, elem.Size(), 0)
			p.printType(elem)
			p.offset = origOffset
			break
		}

		if typ.StructName == "runtime.eface" {
			fmt.Print("interface{}")
			break
		} else if typ.StructName == "runtime.iface" {
			fmt.Print("interface{ ... }")
			break
		}

		isUnion := typ.Kind == "union"
		fmt.Printf("%s {", typ.Kind)
		if typ.Incomplete {
			fmt.Print(" incomplete ")
		}
		p.depth++
		startOffset := p.offset[len(p.offset)-1]
		for i, f := range typ.Field {
			if i != 0 {
				fmt.Println()
			}
			indent := "\n" + strings.Repeat("\t", p.depth)
			fmt.Print(indent)
			// TODO: Bit offsets?
			// TODO: Print gaps.
			if !isUnion {
				p.offset[len(p.offset)-1] = startOffset + f.ByteOffset
				fmt.Printf("// offset %s%s", p.strOffset(), indent)
			}
			fmt.Printf("%s ", f.Name)
			p.printType(f.Type)
			if f.BitSize != 0 {
				fmt.Printf(" : %d", f.BitSize)
			}
		}
		p.offset[len(p.offset)-1] = startOffset
		p.depth--
		if len(typ.Field) == 0 {
			fmt.Print("}")
		} else {
			fmt.Printf("\n%s}", strings.Repeat("\t", p.depth))
		}

	case *dwarf.EnumType:
		fmt.Print("enum") // TODO

	case *dwarf.BoolType, *dwarf.CharType, *dwarf.ComplexType, *dwarf.FloatType, *dwarf.IntType, *dwarf.UcharType, *dwarf.UintType:
		// Basic types.
		fmt.Print(typ.String())

	case *dwarf.PtrType:
		origOffset := p.offset
		p.offset = []int64{0}
		p.nameOk++
		fmt.Printf("*")
		p.printType(typ.Type)
		p.nameOk--
		p.offset = origOffset

	case *dwarf.FuncType:
		// TODO: Expand ourselves so we can clean up argument
		// types, etc.
		fmt.Printf(typ.String())

	case *dwarf.QualType:
		fmt.Printf("/* %s */ ", typ.Qual)
		p.printType(typ.Type)

	case *dwarf.TypedefType:
		n := typ.Common().Name
		if isBuiltinName(n) {
			// TODO: Make Go-ifying optional.
			//
			// TODO: Expand map types ourselves if
			// possible so we can clean up the type names.
			fmt.Print(n)
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
				fmt.Printf(p.stripPkg(n))
				return
			}
		}

		// TODO: If it's "type x map..." or similar, we never
		// see the "map[...]" style name and only see that x's
		// underlying type is a pointer to a struct named
		// "hash<...>".

		fmt.Printf("/* %s */ ", p.stripPkg(n))
		p.printType(real)

	case *dwarf.UnspecifiedType:
		fmt.Print("unspecified")

	case *dwarf.VoidType:
		fmt.Print("void")
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
