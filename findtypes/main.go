// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command findtypes compares checkmarks failures with the types in a
// binary to find likely matches.
//
// findtypes deduces the likely pointer/scalar map from the output of
// a checkmarks failure and compares it against the pointer/scalar
// maps of all types in a binary. The output is a scored and ranked
// list of the most closely matching types, along with their
// pointer/scalar maps.
package main

import (
	"bufio"
	"debug/dwarf"
	"debug/elf"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
	"os"
	"regexp"
	"sort"
	"strconv"
)

const ptrSize = 8 // TODO: Get from DWARF.

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s failure binary\n", os.Args[0])
	}
	flag.Parse()
	if flag.NArg() != 2 {
		flag.Usage()
		os.Exit(1)
	}
	failPath, binPath := flag.Arg(0), flag.Arg(1)

	// Parse greyobject failure.
	failFile, err := os.Open(failPath)
	if err != nil {
		log.Fatal(err)
	}
	failure := parseGreyobject(failFile)
	failFile.Close()
	if failure.words == nil {
		log.Fatal("failed to parse failure message in %s", failPath)
	}
	fmt.Print("failure:")
	for i, known := range failure.words {
		if i%32 == 0 {
			fmt.Printf("\n\t")
		} else if i%16 == 0 {
			fmt.Printf(" ")
		}
		switch known {
		case 0:
			fmt.Print("S")
		case 1:
			fmt.Print("P")
		case 2:
			fmt.Print("?")
		}
	}
	fmt.Println()

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

	// Find all of the types.
	type comparison struct {
		ti    *typeInfo
		score float64
	}
	var results []comparison
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
		base, ok := ent.Val(dwarf.AttrType).(dwarf.Offset)
		if !ok {
			log.Printf("type %s has unknown underlying type", name)
			continue
		}

		typ, err := d.Type(base)
		if err != nil {
			log.Fatal(err)
		}
		ti := &typeInfo{name: name, words: int(typ.Size()+ptrSize-1) / ptrSize}
		ti.processType(typ, 0)
		if ti.incomplete {
			log.Printf("ignoring incomplete type %s", ti.name)
			continue
		}

		score := failure.compare(ti)
		results = append(results, comparison{ti, score})
	}

	// Print results.
	sort.Slice(results, func(i, j int) bool {
		return results[i].score < results[j].score
	})
	if len(results) > 10 {
		results = results[len(results)-10:]
	}
	for _, c := range results {
		fmt.Print(c.score, " ", c.ti.name)
		failure.printCompare(c.ti)
	}
}

type typeInfo struct {
	name       string
	ptr        big.Int
	words      int
	incomplete bool
}

func (t *typeInfo) processType(typ dwarf.Type, offset int) {
	switch typ := typ.(type) {
	case *dwarf.ArrayType:
		if typ.Count < 0 || typ.StrideBitSize > 0 {
			t.incomplete = true
			return
		}
		for i := 0; i < int(typ.Count); i++ {
			// TODO: Alignment?
			t.processType(typ.Type, offset+i*int(typ.Type.Size()))
		}

	case *dwarf.StructType:
		if typ.Kind == "union" {
			t.incomplete = true
			log.Printf("encountered union")
			return
		}
		if typ.Incomplete {
			t.incomplete = true
			return
		}
		for _, f := range typ.Field {
			if f.BitSize != 0 {
				t.incomplete = true
				log.Printf("encountered bit field")
				return
			}
			t.processType(f.Type, offset+int(f.ByteOffset))
		}

	case *dwarf.BoolType, *dwarf.CharType, *dwarf.ComplexType,
		*dwarf.EnumType, *dwarf.FloatType, *dwarf.IntType,
		*dwarf.UcharType, *dwarf.UintType:
		// Nothing

	case *dwarf.PtrType:
		if typ.Size() != ptrSize {
			log.Fatalf("funny PtrSize size: %d", typ.Size())
		}
		if offset%ptrSize != 0 {
			log.Fatal("unaligned pointer")
		}
		t.ptr.SetBit(&t.ptr, offset/ptrSize, 1)

	case *dwarf.FuncType:
		// Size is -1.
		if offset%ptrSize != 0 {
			log.Fatal("unaligned pointer")
		}
		t.ptr.SetBit(&t.ptr, offset/ptrSize, 1)

	case *dwarf.QualType:
		t.processType(typ.Type, offset)

	case *dwarf.TypedefType:
		t.processType(typ.Type, offset)

	case *dwarf.UnspecifiedType:
		t.incomplete = true
		log.Printf("encountered UnspecifiedType")

	case *dwarf.VoidType:
		t.incomplete = true
		log.Printf("encountered VoidType")
	}
}

type greyobjectFailure struct {
	words []int // 0 scalar, 1 pointer, 2 unknown
}

var (
	spanRe = regexp.MustCompile(`base=.* s\.elemsize=(\d+)`)
	baseRe = regexp.MustCompile(`\*\(base\+(\d+)\) = (0x[0-9a-f]+)( <==)?$`)
)

func parseGreyobject(r io.Reader) *greyobjectFailure {
	var failure greyobjectFailure
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		l := scanner.Text()

		subs := spanRe.FindStringSubmatch(l)
		if subs != nil {
			elemsize, _ := strconv.Atoi(subs[1])
			failure.words = make([]int, elemsize/ptrSize)
			for i := range failure.words {
				failure.words[i] = 2
			}
			continue
		}

		subs = baseRe.FindStringSubmatch(l)
		if subs == nil {
			continue
		}

		offset, _ := strconv.ParseInt(subs[1], 0, 64)
		val, _ := strconv.ParseInt(subs[2], 0, 64)

		// TODO: This only recognizes heap pointers. Maybe
		// look at the binary to figure out reasonable global
		// pointers?
		known := 2
		if val>>32 == 0xc4 {
			known = 1
		} else if val != 0 {
			known = 0
		}

		failure.words[offset/ptrSize] = known
	}
	if err := scanner.Err(); err != nil {
		log.Fatal("reading greyobject output:", err)
	}
	return &failure
}

func (f *greyobjectFailure) compare(ti *typeInfo) float64 {
	score, denom := 0.0, 0.0
	for i, known := range f.words {
		if known == 2 {
			continue
		}
		denom++
		if ti.words < i {
			score -= 1
		} else if int(ti.ptr.Bit(i)) == known {
			score += 1
		} else {
			score -= 1
		}
	}
	if ti.words > len(f.words) {
		score -= float64(ti.words - len(f.words))
	}
	return score / denom
}

func (f *greyobjectFailure) printCompare(ti *typeInfo) {
	l := ti.words
	if len(f.words) > l {
		l = len(f.words)
	}
	for i := 0; i < l; i++ {
		if i%32 == 0 {
			fmt.Printf("\n\t")
		} else if i%16 == 0 {
			fmt.Printf(" ")
		}

		have := int(ti.ptr.Bit(i))

		var want int
		if i < len(f.words) {
			want = f.words[i]
		} else {
			want = 1 - have
		}

		switch {
		case want == 2:
			fmt.Print("?")
		case have == want:
			if have == 0 {
				fmt.Print("S")
			} else {
				fmt.Print("P")
			}
		case have != want:
			if have == 0 {
				fmt.Print("s")
			} else {
				fmt.Print("p")
			}
		}
	}
	fmt.Println()
}
