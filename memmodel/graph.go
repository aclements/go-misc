// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
	"strings"
)

type Graph struct {
	Nodes []*GNode
}

type GNode struct {
	ID    int
	Label string
	In    map[int]bool
	Out   map[int]bool
}

func (g *Graph) NewNode(label string) *GNode {
	id := len(g.Nodes)
	node := &GNode{ID: id, Label: label, In: make(map[int]bool), Out: make(map[int]bool)}
	g.Nodes = append(g.Nodes, node)
	return node
}

func (g *Graph) Edge(from, to *GNode) {
	from.Out[to.ID] = true
	to.In[from.ID] = true
}

func (g *Graph) RemoveEdge(from, to *GNode) {
	delete(from.Out, to.ID)
	delete(to.In, from.ID)
}

func (g *Graph) MaximalCliques() [][]int {
	cliques := [][]int{}

	have := make([]bool, len(g.Nodes))
	for id := range g.Nodes {
		if have[id] {
			continue
		}

		clique := []int{id}
		have[id] = true
		for oid := id + 1; oid < len(g.Nodes); oid++ {
			if have[oid] {
				continue
			}

			// If this node it connected to all nodes in
			// clique, add it to the clique.
			onode := g.Nodes[oid]
			for _, cid := range clique {
				if !onode.In[cid] || !onode.Out[cid] {
					goto notIn
				}
			}
			clique = append(clique, oid)
			have[oid] = true

		notIn:
		}

		cliques = append(cliques, clique)
	}

	return cliques
}

func (g *Graph) CollapseNodes(groups [][]int) *Graph {
	out := new(Graph)

	// Create a node for each group.
	groupNodes := []*GNode{}
	oldToNew := make([]*GNode, len(g.Nodes))
	for _, group := range groups {
		label := []string{}
		for _, id := range group {
			label = append(label, g.Nodes[id].Label)
		}
		groupNode := out.NewNode(strings.Join(label, "\n"))
		groupNodes = append(groupNodes, groupNode)
		for _, id := range group {
			oldToNew[id] = groupNode
		}
	}

	// Map old edges to new edges.
	for oid, oldNode := range g.Nodes {
		newNode := oldToNew[oid]
		if newNode == nil {
			continue
		}
		for to := range oldNode.Out {
			newTo := oldToNew[to]
			if newTo == nil {
				continue
			}
			if newTo == newNode && g.Nodes[to] != oldNode {
				// Eliminate edges within groups,
				// unless they were originally
				// self-edges.
				continue
			}
			out.Edge(newNode, newTo)
		}
	}

	return out
}

func (g *Graph) TransitiveReduction() {
	// TODO: This assumes a DAG; it doesn't work with cycles.
	type edge struct{ from, to *GNode }
	toRemove := make(map[edge]bool)
	visited := make([]bool, len(g.Nodes))
	var rec func(from, to *GNode, remove bool)
	rec = func(from, to *GNode, remove bool) {
		if remove {
			if visited[to.ID] {
				return
			}
			visited[to.ID] = true
			toRemove[edge{from, to}] = true
		}
		for next := range to.Out {
			rec(from, g.Nodes[next], true)
		}
	}
	for _, node := range g.Nodes {
		for i := range visited {
			visited[i] = false
		}

		// Do a DFS starting from each node reachable from
		// node and remove edges from node.
		for child := range node.Out {
			rec(node, g.Nodes[child], false)
		}
	}
	for edge := range toRemove {
		g.RemoveEdge(edge.from, edge.to)
	}

}

func (g *Graph) ToDot(w io.Writer, nodePrefix string) {
	for id, node := range g.Nodes {
		name := fmt.Sprintf("%s%d", nodePrefix, id)
		fmt.Fprintf(w, "%s [label=%q];\n", name, node.Label)
		for oid := range node.Out {
			fmt.Fprintf(w, "%s -> %s%d;\n", name, nodePrefix, oid)
		}
	}
}
