// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command memmodel is a memory model model checker.
//
// It generates a large number of "litmus test" programs consisting of
// reads and writes of variables on multiple threads. For each
// program, it determines all permissible outcomes under different
// memory models (currently SC, TSO, and TSO with memory barriers
// after stores) and determines which memory models are weaker or
// stronger than which others. It produces a dot graph of the partial
// order of memory model strength and, for every pair of models A and
// B where A is weaker than B, it gives an example program where A
// permits outcomes that B excludes.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
)

type Model interface {
	Eval(p *Prog, outcomes *OutcomeSet)
	String() string
}

var models = []Model{
	SCModel{},
	TSOModel{},
	TSOModel{StoreMFence: true},
	HBModel{HBSC{}},
	HBModel{HBTSO{}},
}

// TODO: Make the operation mode a flag. Also have a mode for only
// showing where the models differ.
const showProgs = false

func main() {
	flagOut := flag.String("o", "", "continuously write model graph to `output` dot file")
	flagNoSimplify := flag.Bool("no-simplify", false, "disable graph simplification")
	flag.Parse()
	if flag.NArg() > 0 {
		flag.Usage()
		os.Exit(2)
	}

	// counterexamples[i][j] gives an example program where model
	// i permits outcomes that model j does not.
	counterexamples := make([][]Prog, len(models))
	for i := range counterexamples {
		counterexamples[i] = make([]Prog, len(models))
	}

	n := 0
	outcomes := make([]OutcomeSet, len(models))
	for p := range GenerateProgs() {
		if !showProgs && n%10 == 0 {
			fmt.Fprintf(os.Stderr, "\r%d progs", n)
		}
		n++

		for i, model := range models {
			model.Eval(&p, &outcomes[i])
		}

		if showProgs {
			fmt.Println(&p)
			names := []string{}
			for _, model := range models {
				names = append(names, model.String())
			}
			printOutcomeTable(os.Stdout, names, outcomes)
			fmt.Println()
		}

		for i := range counterexamples {
			for j := range counterexamples[i] {
				if i == j {
					continue
				}
				if counterexamples[i][j].Threads[0].Ops[0].Type != OpExit {
					// Already have a counterexample.
					continue
				}
				if outcomes[i] == outcomes[j] {
					continue
				}
				if outcomes[i].Contains(&outcomes[j]) {
					// Model i permits outcomes
					// that model j does not. (i
					// is weaker than j.)
					counterexamples[i][j] = p
				}
				// TODO: Prefer smaller
				// counterexamples.
			}
		}

		if n%100 == 0 && *flagOut != "" {
			// dot uses inotify wrong, so it doesn't
			// notice if we write to a temp file and
			// rename it over the output file.
			f, err := os.Create(*flagOut)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			writeModelGraph(f, counterexamples, !*flagNoSimplify)
			f.Close()
		}
	}
	fmt.Fprintf(os.Stderr, "\r%d progs\n", n)

	f := os.Stdout
	if *flagOut != "" {
		var err error
		f, err = os.Create(*flagOut)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
	}
	writeModelGraph(f, counterexamples, !*flagNoSimplify)
}

func writeModelGraph(w io.Writer, counterexamples [][]Prog, simplify bool) {
	fmt.Fprintln(w, "digraph memmodel {")
	if simplify {
		fmt.Fprintln(w, "label=\"A -> B means A is stronger than B\";")
	} else {
		fmt.Fprintln(w, "label=\"A -> B means A is stronger than or equal to B\";")
	}

	// Create Graph.
	g := new(Graph)
	nodes := []*GNode{}
	for _, model := range models {
		nodes = append(nodes, g.NewNode(model.String()))
	}
	for i := range counterexamples {
		for j, p := range counterexamples[i] {
			if i == j {
				continue
			}
			if p.Threads[0].Ops[0].Type == OpExit {
				// No counterexample. Model i is
				// stronger than or equal to model j.
				g.Edge(nodes[i], nodes[j])
			} else {
				// Print the counter example. Model i
				// is weaker than model j.
				fmt.Fprintf(w, "# %q is weaker than %q;\n", models[i], models[j])
				fmt.Fprintln(w, "# "+strings.Replace(p.String(), "\n", "\n# ", -1))
				// TODO: Print an example of why.
			}
		}
	}

	if simplify {
		// Reduce equivalence classes to single nodes. Because
		// this is currently a non-strict partial order,
		// maximal cliques correspond to equivalence classes
		// and are unambiguous. This makes the graph a strict
		// partial order.
		cliques := g.MaximalCliques()
		g = g.CollapseNodes(cliques)
		// Now that we have a strict partial order (a DAG),
		// remove edges that are implied by other edges.
		g.TransitiveReduction()
	}

	// Print graph.
	g.ToDot(w, "")

	fmt.Fprintln(w, "}")
}
