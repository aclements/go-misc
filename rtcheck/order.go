// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"go/token"
	"html/template"
	"io"
	"io/ioutil"
	"log"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
)

// LockOrder tracks a lock graph and reports cycles that prevent the
// graph from being a partial order.
type LockOrder struct {
	sp   *StringSpace
	fset *token.FileSet
	m    map[lockOrderEdge]map[lockOrderInfo]struct{}

	// cycles is the cached result of FindCycles, or nil.
	cycles [][]int
}

type lockOrderEdge struct {
	fromId, toId int
}

type lockOrderInfo struct {
	fromStack, toStack *StackFrame // Must be interned and common trimmed
}

// NewLockOrder returns an empty lock graph. Source locations in
// reports will be resolved using fset.
func NewLockOrder(fset *token.FileSet) *LockOrder {
	return &LockOrder{
		sp:   nil,
		fset: fset,
		m:    make(map[lockOrderEdge]map[lockOrderInfo]struct{}),
	}
}

// Add adds lock edges to the lock order, given that the locks in
// locked are currently held and the locks in locking are being
// acquired at stack.
func (lo *LockOrder) Add(locked *LockSet, locking pointer.PointsToSet, stack *StackFrame) {
	lo.cycles = nil
	if lo.sp == nil {
		lo.sp = locked.sp
	} else if lo.sp != locked.sp {
		panic("locks come from a different StringSpace")
	}

	newls := NewLockSet(lo.sp).Plus(locking, stack) // TODO: Unnecessary
	for i := 0; i < locked.bits.BitLen(); i++ {
		if locked.bits.Bit(i) != 0 {
			for j := 0; j < newls.bits.BitLen(); j++ {
				if newls.bits.Bit(j) != 0 {
					// Trim the common prefix of
					// the two stacks, since we
					// only care about how we got
					// from locked to locking.
					lockedStack := locked.stacks[i]
					fromStack, toStack := lockedStack.TrimCommonPrefix(stack)

					// Add info to edge.
					edge := lockOrderEdge{i, j}
					info := lockOrderInfo{
						fromStack.Intern(),
						toStack.Intern(),
					}
					infos := lo.m[edge]
					if infos == nil {
						infos = make(map[lockOrderInfo]struct{})
						lo.m[edge] = infos
					}
					infos[info] = struct{}{}
				}
			}
		}
	}
}

// FindCycles returns a list of cycles in the lock order. Each cycle
// is a list of lock IDs from the StringSpace in cycle order (without
// any repetition).
func (lo *LockOrder) FindCycles() [][]int {
	if lo.cycles != nil {
		return lo.cycles
	}

	// Compute out-edge adjacency list.
	out := map[int][]int{}
	for edge := range lo.m {
		out[edge.fromId] = append(out[edge.fromId], edge.toId)
	}

	// Use DFS to find cycles.
	//
	// TODO: Implement a real cycle-finding algorithm. This one is
	// terrible.
	path, pathSet := []int{}, map[int]struct{}{}
	cycles := [][]int{}
	var dfs func(root, node int)
	dfs = func(root, node int) {
		if _, ok := pathSet[node]; ok {
			// Only report as a cycle if we got back to
			// where we started and this is the lowest
			// numbered node in the cycle. This gets us
			// each elementary cycle exactly once.
			if node == root {
				minNode := node
				for _, n := range path {
					if n < minNode {
						minNode = n
					}
				}
				if node == minNode {
					pathCopy := append([]int(nil), path...)
					cycles = append(cycles, pathCopy)
				}
			}
			return
		}
		pathSet[node] = struct{}{}
		path = append(path, node)
		for _, next := range out[node] {
			dfs(root, next)
		}
		path = path[:len(path)-1]
		delete(pathSet, node)
	}
	for root := range out {
		dfs(root, root)
	}

	// Cache the result.
	lo.cycles = cycles
	return cycles
}

// WriteToDot writes the lock graph in the dot language to w, with
// cycles highlighted.
func (lo *LockOrder) WriteToDot(w io.Writer) {
	lo.writeToDot(w)
}

func (lo *LockOrder) writeToDot(w io.Writer) map[lockOrderEdge]string {
	// Find cycles to highlight edges.
	cycles := lo.FindCycles()
	cycleEdges := map[lockOrderEdge]struct{}{}
	var maxStack int
	for _, cycle := range cycles {
		for i, fromId := range cycle {
			toId := cycle[(i+1)%len(cycle)]
			edge := lockOrderEdge{fromId, toId}
			cycleEdges[edge] = struct{}{}
			if len(lo.m[edge]) > maxStack {
				maxStack = len(lo.m[edge])
			}
		}
	}

	fmt.Fprintf(w, "digraph locks {\n")
	fmt.Fprintf(w, "  tooltip=\" \";\n")
	var nodes big.Int
	nid := func(lockId int) string {
		return fmt.Sprintf("l%d", lockId)
	}
	// Write edges.
	edgeIds := make(map[lockOrderEdge]string)
	for edge, stacks := range lo.m {
		var props string
		if _, ok := cycleEdges[edge]; ok {
			width := 1 + 6*float64(len(stacks))/float64(maxStack)
			props = fmt.Sprintf(",label=%d,penwidth=%f,color=red", len(stacks), width)
		}
		id := fmt.Sprintf("edge%d-%d", edge.fromId, edge.toId)
		edgeIds[edge] = id
		tooltip := fmt.Sprintf("%s -> %s", lo.sp.s[edge.fromId], lo.sp.s[edge.toId])
		fmt.Fprintf(w, "  %s -> %s [id=%q,tooltip=%q%s];\n", nid(edge.fromId), nid(edge.toId), id, tooltip, props)
		nodes.SetBit(&nodes, edge.fromId, 1)
		nodes.SetBit(&nodes, edge.toId, 1)
	}
	// Write nodes. This excludes lone locks: these are only the
	// locks that participate in some ordering
	for i := 0; i < nodes.BitLen(); i++ {
		if nodes.Bit(i) == 1 {
			fmt.Fprintf(w, "  %s [label=%q];\n", nid(i), lo.sp.s[i])
		}
	}
	fmt.Fprintf(w, "}\n")
	return edgeIds
}

type renderedPath struct {
	RootFn   string
	From, To []renderedFrame
}

type renderedFrame struct {
	Op  string
	Pos token.Position
}

func (lo *LockOrder) renderInfo(edge lockOrderEdge, info lockOrderInfo) renderedPath {
	fset := lo.fset
	fromStack := info.fromStack.Flatten(nil)
	toStack := info.toStack.Flatten(nil)
	rootFn := fromStack[0].Parent()
	renderStack := func(stack []*ssa.Call, tail string) []renderedFrame {
		var frames []renderedFrame
		for i, call := range stack[1:] {
			frames = append(frames, renderedFrame{"calls " + call.Parent().String(), fset.Position(stack[i].Pos())})
		}
		frames = append(frames, renderedFrame{tail, fset.Position(stack[len(stack)-1].Pos())})
		return frames
	}
	return renderedPath{
		rootFn.String(),
		renderStack(fromStack, "acquires "+lo.sp.s[edge.fromId]),
		renderStack(toStack, "acquires "+lo.sp.s[edge.toId]),
	}
}

// Check writes a text report of lock cycles to w.
//
// This report is thorough, but can be quite repetitive, since a
// single edge can participate in multiple cycles.
func (lo *LockOrder) Check(w io.Writer) {
	cycles := lo.FindCycles()

	// Report cycles.
	printStack := func(stack []renderedFrame) {
		indent := 6
		for _, fr := range stack {
			fmt.Fprintf(w, "%*s%s at %s\n", indent, "", fr.Op, fr.Pos)
			indent += 2
		}
	}
	printInfo := func(rinfo renderedPath) {
		fmt.Fprintf(w, "    %s\n", rinfo.RootFn)
		printStack(rinfo.From)
		printStack(rinfo.To)
	}
	for _, cycle := range cycles {
		cycle = append(cycle, cycle[0])
		fmt.Fprintf(w, "lock cycle: ")
		for i, node := range cycle {
			if i != 0 {
				fmt.Fprintf(w, " -> ")
			}
			fmt.Fprintf(w, lo.sp.s[node])
		}
		fmt.Fprintf(w, "\n")

		for i := 0; i < len(cycle)-1; i++ {
			edge := lockOrderEdge{cycle[i], cycle[i+1]}
			infos := lo.m[edge]

			fmt.Fprintf(w, "  %d path(s) acquire %s then %s:\n", len(infos), lo.sp.s[edge.fromId], lo.sp.s[edge.toId])
			for info, _ := range infos {
				rinfo := lo.renderInfo(edge, info)
				printInfo(rinfo)
			}
			fmt.Fprintf(w, "\n")
		}
	}
}

// WriteToHTML writes a self-contained, interactive HTML lock graph
// report to w. It requires dot to be in $PATH.
func (lo *LockOrder) WriteToHTML(w io.Writer) {
	// Generate SVG from dot graph.
	cmd := exec.Command("dot", "-Tsvg")
	dotin, err := cmd.StdinPipe()
	if err != nil {
		log.Fatal("creating pipe to dot: ", err)
	}
	dotDone := make(chan bool)
	var edgeIds map[lockOrderEdge]string
	go func() {
		edgeIds = lo.writeToDot(dotin)
		dotin.Close()
		dotDone <- true
	}()
	svg, err := cmd.Output()
	if err != nil {
		log.Fatal("error running dot: ", err)
	}
	<-dotDone
	// Strip stuff before the SVG tag so we can put it into HTML.
	if i := bytes.Index(svg, []byte("<svg")); i > 0 {
		svg = svg[i:]
	}

	// Construct JSON for lock graph details.
	//
	// TODO: This JSON is ludicrously inefficient. It's so big it
	// takes appreciable time for the browser to load this.
	type jsonEdge struct {
		EdgeID string
		Locks  [2]string
		Paths  []renderedPath
	}
	jsonEdges := []jsonEdge{}
	for edge, infos := range lo.m {
		var rpaths []renderedPath
		for info := range infos {
			rpaths = append(rpaths, lo.renderInfo(edge, info))
		}
		jsonEdges = append(jsonEdges, jsonEdge{
			EdgeID: edgeIds[edge],
			Locks: [2]string{lo.sp.s[edge.fromId],
				lo.sp.s[edge.toId],
			},
			Paths: rpaths,
		})
	}

	// Find the static file path.
	//
	// TODO: Optionally bake these into the binary.
	var static string
	var found bool
	for _, gopath := range filepath.SplitList(os.Getenv("GOPATH")) {
		static = filepath.Join(gopath, "src/github.com/aclements/go-misc/rtcheck/static")
		if _, err := os.Stat(static); err == nil {
			found = true
			break
		}
	}
	if !found {
		log.Fatal("unable to find HTML template in $GOPATH")
	}

	// Generate HTML.
	tmpl, err := template.ParseFiles(filepath.Join(static, "tmpl-order.html"))
	if err != nil {
		log.Fatal("loading HTML templates: ", err)
	}
	mainJS, err := ioutil.ReadFile(filepath.Join(static, "main.js"))
	if err != nil {
		log.Fatal("loading main.js: ", err)
	}
	err = tmpl.Execute(w, map[string]interface{}{
		"graph":  template.HTML(svg),
		"edges":  jsonEdges,
		"mainJS": template.JS(mainJS),
	})
	if err != nil {
		log.Fatal("executing HTML template: ", err)
	}
}
