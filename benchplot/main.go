// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Command benchplot plots the results of benchmarks over time.
//
// benchplot takes an input file in Go benchmark format [1]. Each
// benchmark result must have a "commit" configuration key that gives
// the full commit hash of the revision that gave that result.
// benchplot will cross-reference these hashes against the specified
// Git repository and plot each metric over time for each benchmark.
//
// [1] https://github.com/golang/proposal/blob/master/design/14313-benchmark-format.md
package main

import (
	"bytes"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"runtime"
	"runtime/pprof"
	"strings"

	"github.com/aclements/go-gg/gg"
	"github.com/aclements/go-gg/table"
	"github.com/aclements/go-misc/bench"
)

func main() {
	log.SetPrefix("benchplot: ")
	log.SetFlags(0)

	defaultGitDir, _ := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	defaultGitDir = bytes.TrimRight(defaultGitDir, "\n")
	var (
		flagCPUProfile = flag.String("cpuprofile", "", "write CPU profile to `file`")
		flagMemProfile = flag.String("memprofile", "", "write heap profile to `file`")
		flagGitDir     = flag.String("C", string(defaultGitDir), "run git in `dir`")
		flagOut        = flag.String("o", "", "write output to `file` (default: stdout)")
		flagTable      = flag.Bool("table", false, "output a table instead of a plot")
	)
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [flags] [inputs...]\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if *flagCPUProfile != "" {
		f, err := os.Create(*flagCPUProfile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if *flagMemProfile != "" {
		defer func() {
			runtime.GC()
			f, err := os.Create(*flagMemProfile)
			if err != nil {
				log.Fatal(err)
			}
			pprof.WriteHeapProfile(f)
			f.Close()
		}()
	}

	// Parse benchmark inputs.
	paths := flag.Args()
	if len(paths) == 0 {
		paths = []string{"-"}
	}
	var benchmarks []*bench.Benchmark
	for _, path := range paths {
		func() {
			f := os.Stdin
			if path != "-" {
				var err error
				f, err = os.Open(path)
				if err != nil {
					log.Fatal(err)
				}
				defer f.Close()
			}

			bs, err := bench.Parse(f)
			if err != nil {
				log.Fatal(err)
			}
			benchmarks = append(benchmarks, bs...)
		}()
	}
	bench.ParseValues(benchmarks, nil)

	// Prepare gg tables.
	var tab table.Grouping
	btab, configCols, resultCols := benchmarksToTable(benchmarks)
	if btab.Column("commit") == nil {
		tab = btab
	} else {
		gtab := commitsToTable(Commits(*flagGitDir))
		tab = table.Join(btab, "commit", gtab, "commit")
	}

	// Prepare for output.
	f := os.Stdout
	if *flagOut != "" {
		var err error
		f, err = os.Create(*flagOut)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
	}

	// Output table.
	if *flagTable {
		table.Fprint(f, tab)
		return
	}

	// Plot.
	//
	// TODO: Collect nrows/ncols from the plot itself.
	p, nrows, ncols := plot(tab, configCols, resultCols)
	if !(len(paths) == 1 && paths[0] == "-") {
		p.Add(gg.Title(strings.Join(paths, " ")))
	}

	// Render plot.
	p.WriteSVG(f, 500*ncols, 350*nrows)
}
