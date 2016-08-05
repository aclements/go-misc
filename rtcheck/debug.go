package main

import (
	"fmt"
	"io"
)

// A DebugTree captures a hierarchical debug trace. It's useful for
// visualizing the execution of recursive functions.
type DebugTree struct {
	cur   *debugTreeNode
	roots []*debugTreeNode

	nextEdge string
}

type debugTreeNode struct {
	label    string
	parent   *debugTreeNode
	edges    []string
	children []*debugTreeNode
}

func (t *DebugTree) Push(label string) {
	node := &debugTreeNode{label: label, parent: t.cur}
	if t.cur == nil {
		t.roots = append(t.roots, node)
	} else {
		t.cur.edges = append(t.cur.edges, t.nextEdge)
		t.cur.children = append(t.cur.children, node)
	}
	t.cur = node
	t.nextEdge = ""
}

func (t *DebugTree) Pushf(format string, args ...interface{}) {
	t.Push(fmt.Sprintf(format, args...))
}

func (t *DebugTree) Append(label string) {
	t.cur.label += label
}

func (t *DebugTree) Appendf(format string, args ...interface{}) {
	t.Append(fmt.Sprintf(format, args...))
}

func (t *DebugTree) Pop() {
	if t.cur == nil {
		panic("unbalanced Push/Pop")
	}
	t.cur = t.cur.parent
	t.nextEdge = ""
}

func (t *DebugTree) Leaf(label string) {
	t.Push(label)
	t.Pop()
}

func (t *DebugTree) Leaff(format string, args ...interface{}) {
	t.Leaf(fmt.Sprintf(format, args...))
}

func (t *DebugTree) SetEdge(label string) {
	t.nextEdge = label
}

func (t *DebugTree) WriteToDot(w io.Writer) {
	id := func(n *debugTreeNode) string {
		return fmt.Sprintf("n%p", n)
	}

	var rec func(n *debugTreeNode)
	rec = func(n *debugTreeNode) {
		nid := id(n)
		fmt.Fprintf(w, "%s [label=%q];\n", nid, n.label)
		for i, child := range n.children {
			fmt.Fprintf(w, "%s -> %s", nid, id(child))
			if n.edges[i] != "" {
				fmt.Fprintf(w, " [label=%q]", n.edges[i])
			}
			fmt.Fprint(w, ";\n")
			rec(child)
		}
	}

	fmt.Fprint(w, "digraph debug {\n")
	for _, root := range t.roots {
		rec(root)
	}
	fmt.Fprint(w, "}\n")
}
