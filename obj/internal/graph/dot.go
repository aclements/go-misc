// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"fmt"
	"io"
	"os"
)

// Dot contains options for generating a Graphviz Dot graph from a
// Graph.
type Dot struct {
	// Name is the name given to the graph. Usually this can be
	// left blank.
	Name string

	// Label returns the string to use as a label for the given
	// node. If nil, nodes are labeled with their node numbers.
	Label func(node int) string
}

func defaultLabel(node int) string {
	return fmt.Sprintf("%d", node)
}

// Print writes the Dot form of g to os.Stdout.
func (d Dot) Print(g Graph) error {
	return d.Fprint(g, os.Stdout)
}

// Fprint writes the Dot form of g to w.
func (d Dot) Fprint(g Graph, w io.Writer) error {
	label := d.Label
	if label == nil {
		label = defaultLabel
	}

	_, err := fmt.Fprintf(w, "digraph %s {\n", dotString(d.Name))
	if err != nil {
		return err
	}

	for i := 0; i < g.NumNodes(); i++ {
		// Define node.
		_, err = fmt.Fprintf(w, "n%d [label=%s];\n", i, dotString(label(i)))
		if err != nil {
			return err
		}

		// Connect node.
		for _, out := range g.Out(i) {
			_, err = fmt.Fprintf(w, "n%d -> n%d;\n", i, out)
			if err != nil {
				return err
			}
		}
	}

	_, err = fmt.Fprintf(w, "}\n")
	return err
}

// dotString returns s as a quoted dot string.
func dotString(s string) string {
	buf := []byte{'"'}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			buf = append(buf, '\\', 'n')
		case '\\', '"', '{', '}', '<', '>', '|':
			// TODO: Option to allow formatting
			// characters? Maybe private use code points
			// to encode formatting characters? Or
			// something more usefully structured?
			buf = append(buf, '\\', s[i])
		default:
			buf = append(buf, s[i])
		}
	}
	buf = append(buf, '"')
	return string(buf)
}
