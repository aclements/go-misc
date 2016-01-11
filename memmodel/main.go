// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command memmodel is a memory model model checker.
//
// memmodel compares the relative strengths of different memory models
// using model checking techniques. It determines (up to typical
// limitations of model checking) which memory models are equivalent,
// stronger, and weaker than others, and produces this partial order
// as well as example programs that demonstrate the differences
// between non-equivalent memory models.
//
//
// Output
//
// memmodel generates a dot graph showing the partial order of memory
// models. Each node shows a set of equivalently strong memory models
// and edges point from the stronger models to the weaker models.
//
// TODO: Do a better job of outputting example programs.
//
//
// Supported memory models
//
// memmodel supports strict consistency (SC), x86-style total store
// order (TSO), acquire/release, and unordered memory models.
//
// Some of these memory models have two different, but equivalent
// specification strategies. Any model followed by "(HB)" is specified
// as a set rules for constructing a happens-before graph. Otherwise,
// the model is specified as an non-deterministic operational machine
// (e.g., TSO is implemented in terms of store buffers and store
// buffer forwarding). Operational machines tend to be easier to
// reason about, but the happens-before model is extremely flexible.
// Having both helps ensure that our specifications are what we
// intended.
//
// Likewise, some models have options. The operational implementation
// of TSO supports optional memory fences around loads and stores.
//
//
// How it works
//
// memmodel generates a large number of "litmus test" programs, where
// each program consists of a set of threads reading and writing
// shared variables. For each program, it determines all permissible
// outcomes under each memory model. If an outcome is permitted by
// memory model A but not memory model B, then A is weaker than B.
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
	TSOModel{MFenceLoad: true},
	HBModel{HBSC{}},
	HBModel{HBTSO{}},
	HBModel{HBAcqRel{}},
	HBModel{HBUnordered{}},
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
