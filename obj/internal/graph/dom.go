// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

// IDom returns the immediate dominator of each node of g. Nodes that
// don't have an immediate dominator (including root) are assigned -1.
func IDom(g BiGraph, root int) []int {
	// This implements the "engineered algorithm" of Cooper,
	// Harvey, and Kennedy, "A Simple, Fast Dominance Algorithm",
	// 2001.
	//
	// Unlike in Cooper, we mostly use the original node naming,
	// but "intersect" maps into the post-order node naming as
	// needed.

	po := PostOrder(g, root)

	// Compute the post-order node naming for the "intersect"
	// routine. poNum maps from node to post-order name.
	poNum := make([]int, g.NumNodes())
	for i, n := range po {
		poNum[n] = i
	}

	rpo, po := Reverse(po), nil

	// Initialize IDom.
	idom := make([]int, g.NumNodes())
	for i := range idom {
		idom[i] = -1
	}
	idom[root] = root

	// Iterate to convergence.
	changed := true
	for changed {
		changed = false
		for _, b := range rpo {
			if b == root {
				continue
			}

			newIdom := -1
			for _, p := range g.In(b) {
				if idom[p] == -1 {
					continue
				}
				if newIdom == -1 {
					newIdom = p
					continue
				}
				newIdom = intersect(idom, poNum, p, newIdom)
			}

			if idom[b] != newIdom {
				idom[b] = newIdom
				changed = true
			}
		}
	}

	// Clear root's dominator, which is currently a self-loop.
	idom[root] = -1

	return idom
}

func intersect(idom, poNum []int, b1, b2 int) int {
	for b1 != b2 {
		for poNum[b1] < poNum[b2] {
			b1 = idom[b1]
		}
		for poNum[b2] < poNum[b1] {
			b2 = idom[b2]
		}
	}
	return b1
}

// DomFrontier returns the dominance frontier of each node in g. idom
// must be IDom(g, root). idom may be nil, in which case this computes
// IDom.
func DomFrontier(g BiGraph, root int, idom []int) [][]int {
	// This implements the dominance frontier algorithm of Cooper,
	// Harvey, and Kennedy, "A Simple, Fast Dominance Algorithm",
	// 2001.

	if idom == nil {
		idom = IDom(g, root)
	}

	df := make([][]int, g.NumNodes())
	for b, bdom := range idom {
		preds := g.In(b)
		if len(preds) < 2 {
			continue
		}

		for _, pred := range preds {
			runner := pred
			for runner != bdom {
				// Add b to runner's DF set.
				for _, rdf := range df[runner] {
					if rdf == b {
						goto found
					}
				}
				df[runner] = append(df[runner], b)
			found:
				runner = idom[runner]
			}
		}
	}

	// Make sure empty sets are filled in.
	for i := range df {
		if df[i] == nil {
			df[i] = []int{}
		}
	}
	return df
}

// Dom computes the dominator tree from the immediate dominators (as
// computed by IDom).
func Dom(idom []int) *DomTree {
	children := make([][]int, len(idom))

	// Chop up a single slice used to store the children.
	cspace := make([]int, len(idom))
	for _, parent := range idom {
		if parent != -1 {
			cspace[parent]++
		}
	}
	used := 0
	for i, n := range cspace {
		children[i] = cspace[used:used : used+n]
		used += n
	}

	// Actually create the children tree now.
	for node, parent := range idom {
		if parent != -1 {
			children[parent] = append(children[parent], node)
		}
	}

	return &DomTree{idom, children}
}

// DomTree is a dominator tree.
//
// It also satisfies the BiGraph interface, which edges pointing
// toward children.
type DomTree struct {
	idom     []int
	children [][]int
}

func (t *DomTree) IDom(n int) int {
	return t.idom[n]
}

func (t *DomTree) NumNodes() int {
	return len(t.idom)
}

func (t *DomTree) In(n int) []int {
	return t.idom[n : n+1]
}

func (t *DomTree) Out(n int) []int {
	return t.children[n]
}
