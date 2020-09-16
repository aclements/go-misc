// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"go/types"
	"io"
	"log"
	"os"
	"strings"

	"golang.org/x/tools/go/packages"
)

func main() {
	flag.Parse()
	pkgPaths := flag.Args()

	// Get the package count to give the user some feedback.
	cfg := &packages.Config{}
	pkgs, err := packages.Load(cfg, pkgPaths...)
	if err != nil {
		log.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "checking %d packages...\n", len(pkgs))

	// Parse and type-check the packages.
	cfg.Mode = packages.NeedName | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedTypesSizes
	pkgs, err = packages.Load(cfg, pkgPaths...)
	if err != nil {
		log.Fatal(err)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}

	// Analyze.
	//
	// TODO: A different approach would be to ask "what fraction
	// of functions fit entirely in registers given N registers?"
	// That will never quite reach 100% because of arrays. It's
	// also technically sensitive to both the number of float
	// registers and the number of integer registers, but hardly
	// any float registers are used in practice. It also means
	// every register count will have its own stack usage
	// distribution.
	var hists Hists
	var nFuncs, nArray int
	for _, pkg := range pkgs {
		for _, obj := range pkg.TypesInfo.Defs {
			obj, ok := obj.(*types.Func)
			if !ok {
				continue
			}
			typ := obj.Type().(*types.Signature)

			var f Flattener
			f.Sizes = pkg.TypesSizes

			// Arguments
			if r := typ.Recv(); r != nil {
				f.Flatten(r.Type())
			}
			ps := typ.Params()
			for i := 0; i < ps.Len(); i++ {
				f.Flatten(ps.At(i).Type())
			}

			// if f.Ints > 100 {
			// 	fmt.Println(obj, f.Ints)
			// }

			hists.AddRegs(&f)

			// Results
			f.Ints, f.Floats = 0, 0
			rs := typ.Results()
			for i := 0; i < rs.Len(); i++ {
				f.Flatten(rs.At(i).Type())
			}

			hists.AddRegs(&f)
			hists.AddStack(&f)

			nFuncs++
			if f.HasArray {
				nArray++
			}
		}
	}
	hists.Print(os.Stdout, true)
	fmt.Printf("Functions with arrays: %d / %d\n", nArray, nFuncs)
}

type Flattener struct {
	Sizes types.Sizes

	Ints, Floats, StackBytes int

	HasArray bool
}

func (f *Flattener) Flatten(args ...types.Type) {
	for _, arg := range args {
		switch arg := arg.(type) {
		default:
			log.Fatal("unknown type: ", arg)

		case *types.Named:
			f.Flatten(arg.Underlying())

		case *types.Array:
			switch arg.Len() {
			case 0:
				continue
				// Special-casing 1 makes no difference in std or cmd.
			// case 1:
			// 	f.Flatten(arg.Elem())
			default:
				// Arrays always go on the stack.
				align := int(f.Sizes.Alignof(arg))
				f.StackBytes = (f.StackBytes + align - 1) &^ (align - 1)
				f.StackBytes += int(f.Sizes.Sizeof(arg))
				f.HasArray = true
			}

		case *types.Struct:
			for i := 0; i < arg.NumFields(); i++ {
				f.Flatten(arg.Field(i).Type())
			}

		case *types.Basic:
			switch arg.Kind() {
			case types.Bool, types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
				types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr:
				// TODO: 64-bit on 32-bit arch needs two regs.
				f.Ints++

			case types.Float32, types.Float64:
				f.Floats++

			case types.Complex64, types.Complex128:
				f.Floats += 2

			case types.String:
				f.Ints += 2

			case types.UnsafePointer:
				f.Ints++

			default:
				log.Fatal("unknown basic kind: ", arg)
			}

		case *types.Chan, *types.Map, *types.Pointer, *types.Signature:
			// These are all represented as a single pointer word.
			f.Ints++

		case *types.Interface:
			// Two pointer words.
			f.Ints += 2

		case *types.Slice:
			// One pointer word plus two scalar words.
			f.Ints += 3
		}
	}
}

type Hist struct {
	Counts map[int]int
}

func (h *Hist) Add(n int) {
	if h.Counts == nil {
		h.Counts = make(map[int]int)
	}
	h.Counts[n]++
}

func (h *Hist) Print(w io.Writer, markdown bool) {
	// Find the max key and total weight.
	max, weight := 0, 0
	for k, v := range h.Counts {
		weight += v
		if k+1 > max {
			max = k + 1
		}
	}

	if markdown {
		fmt.Fprintln(w, "| #regs | #funcs | frac | cdf |")
		fmt.Fprintln(w, "| --- | --- | --- | --- |")
	}

	// Print.
	sum := 0
	for i := 0; i < max; i++ {
		sum += h.Counts[i]
		qtile := float64(sum) / float64(weight)

		fmt.Fprintf(w, "| %3d | %5d | %5.1f%% | %s |\n", i, h.Counts[i], qtile*100, bar(qtile, 20))
	}
}

func bar(frac float64, width int) string {
	blocks := []string{"▏", "▎", "▍", "▌", "▋", "▊", "▉", "█"}
	total := int(frac*float64(width)*8 + 0.5)
	return strings.Repeat("█", total/8) + blocks[total%8]
}

type Hists struct {
	Ints, Floats, StackBytes Hist
}

func (h *Hists) AddRegs(f *Flattener) {
	h.Ints.Add(f.Ints)
	h.Floats.Add(f.Floats)
}

func (h *Hists) AddStack(f *Flattener) {
	h.StackBytes.Add(f.StackBytes)
}

func (h *Hists) Print(w io.Writer, markdown bool) {
	fmt.Fprintln(w, "# integer registers:")
	h.Ints.Print(w, markdown)
	fmt.Fprintln(w, "# float registers:")
	h.Floats.Print(w, markdown)
	fmt.Fprintln(w, "# stack bytes (with unlimited registers):")
	h.StackBytes.Print(w, markdown)
}
