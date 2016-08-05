package main

import (
	"fmt"
	"io"

	"go/token"

	"golang.org/x/tools/go/pointer"
	"golang.org/x/tools/go/ssa"
)

type LockOrder struct {
	sp *StringSpace
	m  map[lockOrderEdge]lockOrderInfo
}

type lockOrderEdge struct {
	fromId, toId int
}

type lockOrderInfo struct {
	fromStack, toStack *StackFrame
}

func NewLockOrder(sp *StringSpace) *LockOrder {
	return &LockOrder{sp, make(map[lockOrderEdge]lockOrderInfo)}
}

func (lo *LockOrder) Add(locked *LockSet, locking pointer.PointsToSet, stack *StackFrame) {
	newls := lo.sp.NewSet().Plus(locking, stack) // TODO: Unnecessary
	for i := 0; i < locked.bits.BitLen(); i++ {
		if locked.bits.Bit(i) != 0 {
			for j := 0; j < newls.bits.BitLen(); j++ {
				if newls.bits.Bit(j) != 0 {
					edge := lockOrderEdge{i, j}
					if _, ok := lo.m[edge]; ok {
						continue
					}
					lo.m[edge] = lockOrderInfo{
						locked.stacks[i],
						stack,
					}
				}
			}
		}
	}
}

func (lo *LockOrder) WriteToDot(w io.Writer) {
	fmt.Fprintf(w, "digraph locks {\n")
	for edge := range lo.m {
		fromName := lo.sp.s[edge.fromId]
		toName := lo.sp.s[edge.toId]
		fmt.Fprintf(w, "  %q -> %q;\n", fromName, toName)
	}
	fmt.Fprintf(w, "}\n")
}

func (lo *LockOrder) Check(w io.Writer, fset *token.FileSet) {
	// Compute out-edge map.
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

	// Report cycles.
	printStack := func(stack []*ssa.Call, tail string) {
		indent := 6
		for i, call := range stack[1:] {
			fmt.Fprintf(w, "%*scalls %s at %s\n", indent, "", call.Parent(), fset.Position(stack[i].Pos()))
			indent += 2
		}
		fmt.Fprintf(w, "%*s%s at %s\n", indent, "", tail, fset.Position(stack[len(stack)-1].Pos()))
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
			from, to := cycle[i], cycle[i+1]
			info := lo.m[lockOrderEdge{from, to}]

			fromStack := info.fromStack.Flatten()
			toStack := info.toStack.Flatten()

			var common int
			for common < len(fromStack) && common < len(toStack) && fromStack[common] == toStack[common] {
				common++
			}

			lastCommonFn := fromStack[common].Parent()
			fmt.Fprintf(w, "    thread %d acquires %s then %s:\n", i+1, lo.sp.s[from], lo.sp.s[to])

			fmt.Fprintf(w, "    %s\n", lastCommonFn)
			printStack(fromStack[common:], fmt.Sprintf("acquires %s", lo.sp.s[from]))
			printStack(toStack[common:], fmt.Sprintf("acquires %s", lo.sp.s[to]))

			fmt.Fprintf(w, "\n")
		}
	}
}
