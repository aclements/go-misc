// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

// Graph represents a directed graph. The nodes of the graph must be
// densely numbered starting at 0.
type Graph interface {
	// NumNodes returns the number of nodes in this graph.
	NumNodes() int

	// Out returns the nodes to which node i points.
	Out(i int) []int
}

// BiGraph extends Graph to graphs that represent both out-edges and
// in-edges.
type BiGraph interface {
	Graph

	// In returns the nodes which point to node i.
	In(i int) []int
}

// MakeBiGraph constructs a BiGraph from what may be a unidirectional
// Graph. If g is already a BiGraph, this returns g.
func MakeBiGraph(g Graph) BiGraph {
	if g, ok := g.(BiGraph); ok {
		return g
	}

	preds := make([][]int, g.NumNodes())
	for i := range preds {
		for _, j := range g.Out(i) {
			preds[j] = append(preds[j], i)
		}
	}

	return &bigraph{g, preds}
}

type bigraph struct {
	Graph
	preds [][]int
}

func (b *bigraph) In(i int) []int {
	return b.preds[i]
}

// IntGraph is a basic Graph g where g[i] is the list of out-edge
// indexes of node i.
type IntGraph [][]int

func (g IntGraph) NumNodes() int {
	return len(g)
}

func (g IntGraph) Out(i int) []int {
	return g[i]
}
