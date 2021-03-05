// Copyright 2020 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// To analyze kubelet:
//
//     ( X=$PWD; cd -q ~/s/kubernetes && $X/abi $(go list -deps ./cmd/kubelet) )

import (
	"flag"
	"fmt"
	"go/types"
	"io"
	"log"
	"math"
	"os"
	"reflect"
	"sort"

	"golang.org/x/tools/go/packages"
)

const (
	minIntRegs = 0
	maxIntRegs = 16

	// The number of floating-point registers has little
	// effect. Just fix it at 8.
	minFloatRegs = 8
	maxFloatRegs = 8

	// Comparison mode.
	modeCompare = false
)

func main() {
	flag.Parse()
	pkgPaths := flag.Args()

	// Get the package count to give the user some feedback.
	cfg := &packages.Config{}
	cfg.Mode = packages.NeedName
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

	// Extract all the functions.
	var funcs []*types.Func
	var sizes types.Sizes
	for _, pkg := range pkgs {
		sizes = pkg.TypesSizes
		for _, obj := range pkg.TypesInfo.Defs {
			if obj, ok := obj.(*types.Func); ok {
				funcs = append(funcs, obj)
			}
		}
	}

	// Analyze.
	qtiles := []float64{0.5, 0.95, 0.99}
	qtileLabels := []string{"p50", "p95", "p99"}
	table := [][]interface{}{
		{"", "", "", "stack args", "spills", "stack total"},
		{"ints", "floats", "% fit", qtileLabels, qtileLabels, qtileLabels},
	}
	if modeCompare {
		qtiles = []float64{0.5, 0.95, 0.99, 1.0}
		qtileLabels = []string{"p50", "p95", "p99", "max"}
		table = [][]interface{}{
			{"", "", "", "", "Δ stack bytes"},
			{"ints", "floats", "Δ % fit", "diff", qtileLabels, "% bigger"},
		}
	}
	opts := ABIOptions{
		EmptyArray:   true,
		OneArray:     true,
		SplitArrays:  false,
		IgnoreBlank:  false,
		SpillRegs:    false,
		EmptyOnStack: true,
	}
	cmp := opts
	cmp.ABI0 = true

	const infinity = math.MaxInt32
	analyze := func(opts, cmp ABIOptions) {
		var stackBytes []int
		var spillBytes []int
		var stackTotal []int
		var overheads []int // Stack bytes vs ABI0
		fit := 0            // # functions that fit entirely in registers
		cmpFit := 0
		cmpDiff := 0   // # functions with any frame difference
		cmpLarger := 0 // # functions with larger stack frames in cmp

		for _, f := range funcs {
			sig := f.Type().(*types.Signature)

			frame := opts.Assign(sig, sizes)

			stackBytes = append(stackBytes, frame.StackBytes)
			spillBytes = append(spillBytes, frame.StackSpillBytes)
			stackTotal = append(stackTotal, frame.StackTotal)
			if frame.StackBytes == 0 {
				fit++
			}

			if modeCompare {
				// Compare to alternate options.
				frameCmp := cmp.Assign(sig, sizes)
				overhead := frameCmp.StackTotal - frame.StackTotal
				overheads = append(overheads, overhead)
				if frameCmp.StackBytes == 0 {
					cmpFit++
				}
				if frame != frameCmp {
					cmpDiff++
				}
				if overhead > 0 {
					cmpLarger++
				}
			}
		}

		row := []interface{}{opts.IntRegs, opts.FloatRegs}
		if opts.IntRegs == infinity {
			row[0] = "∞"
		}
		if opts.FloatRegs == infinity {
			row[1] = "∞"
		}

		if modeCompare {
			pct := func(n int) string {
				return fmt.Sprintf("%5.2f%%", 100*float64(n)/float64(len(funcs)))
			}
			row = append(row, pct(cmpFit-fit))
			row = append(row, []interface{}{cmpDiff, pct(cmpDiff)})
			row = append(row, intQuantiles(overheads, qtiles...))
			row = append(row, pct(cmpLarger))
		} else {
			row = append(row, fmt.Sprintf("%4.1f%%", 100*float64(fit)/float64(len(funcs))))
			row = append(row, intQuantiles(stackBytes, qtiles...))
			row = append(row, intQuantiles(spillBytes, qtiles...))
			row = append(row, intQuantiles(stackTotal, qtiles...))
		}

		table = append(table, row)
	}
	analyze(opts, cmp)
	for opts.IntRegs = minIntRegs; opts.IntRegs <= maxIntRegs; opts.IntRegs++ {
		for opts.FloatRegs = minFloatRegs; opts.FloatRegs <= maxFloatRegs; opts.FloatRegs++ {
			cmp.IntRegs, cmp.FloatRegs = opts.IntRegs, opts.FloatRegs
			analyze(opts, cmp)
		}
	}
	opts.IntRegs, opts.FloatRegs = infinity, maxFloatRegs
	cmp.IntRegs, cmp.FloatRegs = opts.IntRegs, opts.FloatRegs
	analyze(opts, cmp)

	// Print results.
	printTable(os.Stdout, table)
}

type ABIOptions struct {
	IntRegs, FloatRegs int

	ABI0 bool // Use ABI0 (other options are ignored)

	EmptyArray   bool // Empty arrays don't stack-assign
	OneArray     bool // Size-1 arrays don't stack-assign
	SplitArrays  bool // Stack-assign arrays separately from rest of arg
	IgnoreBlank  bool // Skip assigning blank fields
	SpillRegs    bool // Structure spill space as register words
	EmptyOnStack bool // Stack-assign zero-sized values
}

type frameBuilder struct {
	opts    *ABIOptions
	sizes   types.Sizes
	ptrSize int

	ints, floats int

	Frame
}

type Frame struct {
	ArgInts, ArgFloats int
	ResInts, ResFloats int

	StackBytes      int // Stack bytes without spill slots
	StackSpillBytes int // Stack bytes of spill slots
	StackTotal      int // Stack bytes for complete argument frame.
}

func (a *ABIOptions) Assign(sig *types.Signature, sizes types.Sizes) Frame {
	ptrSize := int(sizes.Sizeof(types.Typ[types.Uintptr]))
	f := frameBuilder{opts: a, sizes: sizes, ptrSize: ptrSize}

	// Arguments
	if r := sig.Recv(); r != nil {
		f.AddArg(r.Type(), true)
	}
	ps := sig.Params()
	for i := 0; i < ps.Len(); i++ {
		f.AddArg(ps.At(i).Type(), true)
	}
	f.ArgInts, f.ArgFloats = f.ints, f.floats
	f.StackBytes = align(f.StackBytes, ptrSize)
	f.StackSpillBytes = align(f.StackSpillBytes, ptrSize)

	// Results
	f.ints, f.floats = 0, 0
	rs := sig.Results()
	for i := 0; i < rs.Len(); i++ {
		f.AddArg(rs.At(i).Type(), false)
	}
	f.StackBytes = align(f.StackBytes, ptrSize)
	f.ResInts, f.ResFloats = f.ints, f.floats

	f.StackTotal = f.StackBytes + f.StackSpillBytes

	return f.Frame
}

func (f *frameBuilder) AddArg(arg types.Type, needsSpill bool) {
	if f.opts.ABI0 {
		f.StackAssign(arg)
		return
	}

	si, sf, sb := f.ints, f.floats, f.StackBytes
	if f.RegAssign(arg, true) {
		if needsSpill {
			// Assign spill space.
			if f.opts.SpillRegs {
				f.StackSpillBytes += (f.ints-si)*f.ptrSize + (f.floats-sf)*8
			} else {
				f.StackSpillBytes = align(f.StackSpillBytes, int(f.sizes.Alignof(arg)))
				f.StackSpillBytes += int(f.sizes.Sizeof(arg))
			}
		}
	} else {
		// Stack-assign the whole thing.
		f.ints, f.floats, f.StackBytes = si, sf, sb
		f.StackAssign(arg)
	}
}

func (f *frameBuilder) RegAssign(arg types.Type, top bool) bool {
	switch arg := arg.(type) {
	default:
		log.Fatal("unknown type: ", arg)
		return false

	case *types.Named:
		return f.RegAssign(arg.Underlying(), top)

	case *types.Array:
		if f.opts.EmptyArray && arg.Len() == 0 {
			// Special-case empty arrays.
			return true
		}
		if f.opts.OneArray && arg.Len() == 1 {
			// Special-case length-1 arrays.
			return f.RegAssign(arg.Elem(), false)
		}
		if f.opts.SplitArrays {
			// Arrays can go on the stack without failing
			// the whole argument.
			f.StackAssign(arg)
			return true
		} else {
			// Arrays fail the whole argument.
			return false
		}

	case *types.Struct:
		for i := 0; i < arg.NumFields(); i++ {
			if f.opts.IgnoreBlank && arg.Field(i).Name() == "_" {
				continue
			}
			if !f.RegAssign(arg.Field(i).Type(), false) {
				return false
			}
		}

	case *types.Basic:
		switch arg.Kind() {
		case types.Bool, types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
			types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64, types.Uintptr:
			// TODO: 64-bit on 32-bit arch needs two regs.
			f.ints++

		case types.Float32, types.Float64:
			f.floats++

		case types.Complex64, types.Complex128:
			f.floats += 2

		case types.String:
			f.ints += 2

		case types.UnsafePointer:
			f.ints++

		default:
			log.Fatal("unknown basic kind: ", arg)
		}

	case *types.Chan, *types.Map, *types.Pointer, *types.Signature:
		// These are all represented as a single pointer word.
		f.ints++

	case *types.Interface:
		// Two pointer words.
		f.ints += 2

	case *types.Slice:
		// One pointer word plus two scalar words.
		f.ints += 3
	}

	// Check for out-of-registers.
	return f.ints <= f.opts.IntRegs && f.floats <= f.opts.FloatRegs
}

func (f *frameBuilder) StackAssign(arg types.Type) {
	f.StackBytes = align(f.StackBytes, int(f.sizes.Alignof(arg)))
	f.StackBytes += int(f.sizes.Sizeof(arg))
}

func align(x, n int) int {
	return (x + n - 1) &^ (n - 1)
}

func intQuantiles(xs []int, qs ...float64) []int {
	sort.Ints(xs)
	vs := make([]int, 0, len(qs))
	for _, q := range qs {
		i := int(q * float64(len(xs)))
		if i < 0 {
			i = 0
		} else if i >= len(xs) {
			i = len(xs) - 1
		}
		vs = append(vs, xs[i])
	}
	return vs
}

func floatQuantiles(xs []float64, qs ...float64) []float64 {
	sort.Float64s(xs)
	vs := make([]float64, 0, len(qs))
	for _, q := range qs {
		i := int(q * float64(len(xs)))
		if i < 0 {
			i = 0
		} else if i >= len(xs) {
			i = len(xs)
		}
		vs = append(vs, xs[i])
	}
	return vs
}

func printTable(w io.Writer, table [][]interface{}) {
	type layoutNode struct {
		w        int
		children []*layoutNode
	}
	type cellKey struct {
		row int
		col *layoutNode
	}

	// Stringify cells and construct layout
	cells := make(map[cellKey]string)
	layout := &layoutNode{}
	var walk func(ri int, row reflect.Value, node *layoutNode) int
	walk = func(ri int, row reflect.Value, node *layoutNode) int {
		if row.Kind() == reflect.Interface {
			row = row.Elem()
		}

		if row.Kind() != reflect.Slice {
			// This is a cell.
			val := fmt.Sprint(row)
			if len(val) > node.w {
				node.w = len(val)
			}
			cells[cellKey{ri, node}] = val
			return node.w
		}

		// This is a slice.
		totalW := 0
		rowLen := row.Len()
		for vi := 0; vi < rowLen; vi++ {
			var child *layoutNode
			if vi < len(node.children) {
				child = node.children[vi]
			} else {
				child = &layoutNode{}
				node.children = append(node.children, child)
			}
			totalW += walk(ri, row.Index(vi), child)
		}
		// Add in interior column spacing.
		totalW += 3 * (rowLen - 1)
		if totalW > node.w {
			node.w = totalW
		}
		return node.w
	}
	for ri, row := range table {
		walk(ri, reflect.ValueOf(row), layout)
	}

	// Print table
	var printNode func(ri int, node *layoutNode, fillW int)
	printNode = func(ri int, node *layoutNode, fillW int) {
		if val, ok := cells[cellKey{ri, node}]; ok {
			if fillW < node.w {
				fillW = node.w
			}
			fmt.Fprintf(w, "| %*s ", fillW, val)
			return
		}

		for ci, child := range node.children {
			parentW := 0
			if ci == len(node.children)-1 {
				parentW = fillW
			} else {
				fillW -= child.w
			}
			printNode(ri, child, parentW)
		}
	}
	for ri := range table {
		printNode(ri, layout, 0)
		fmt.Fprintf(w, "|\n")
	}
}
