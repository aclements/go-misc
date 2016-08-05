package main

import (
	"fmt"
	"io"

	"go/token"

	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
)

// LockOrder tracks a lock graph and reports cycles that prevent the
// graph from being a partial order.
type LockOrder struct {
	sp   *StringSpace
	fset *token.FileSet
	m    map[lockOrderEdge]map[lockOrderInfo]struct{}
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

	return cycles
}

// WriteToDot writes the lock graph in the dot language to w, with
// cycles highlighted.
func (lo *LockOrder) WriteToDot(w io.Writer) {
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
	for edge, stacks := range lo.m {
		fromName := lo.sp.s[edge.fromId]
		toName := lo.sp.s[edge.toId]
		var props string
		if _, ok := cycleEdges[edge]; ok {
			width := 1 + 6*float64(len(stacks))/float64(maxStack)
			props = fmt.Sprintf(" [label=%d,penwidth=%f,color=red]", len(stacks), width)
		}
		fmt.Fprintf(w, "  %q -> %q%s;\n", fromName, toName, props)
	}
	fmt.Fprintf(w, "}\n")
}

// Check writes a text report of lock cycles to w.
func (lo *LockOrder) Check(w io.Writer) {
	cycles := lo.FindCycles()
	fset := lo.fset

	// Report cycles.
	printStack := func(stack []*ssa.Call, tail string) {
		indent := 6
		for i, call := range stack[1:] {
			fmt.Fprintf(w, "%*scalls %s at %s\n", indent, "", call.Parent(), fset.Position(stack[i].Pos()))
			indent += 2
		}
		fmt.Fprintf(w, "%*s%s at %s\n", indent, "", tail, fset.Position(stack[len(stack)-1].Pos()))
	}
	printInfo := func(tid int, edge lockOrderEdge, info lockOrderInfo) {
		fromStack := info.fromStack.Flatten(nil)
		toStack := info.toStack.Flatten(nil)

		lastCommonFn := fromStack[0].Parent()
		fmt.Fprintf(w, "    %s\n", lastCommonFn)
		printStack(fromStack, fmt.Sprintf("acquires %s", lo.sp.s[edge.fromId]))
		printStack(toStack, fmt.Sprintf("acquires %s", lo.sp.s[edge.toId]))
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
				printInfo(i, edge, info)
			}
			fmt.Fprintf(w, "\n")
		}
	}
}
