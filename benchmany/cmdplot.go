// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/aclements/go-moremath/stats"
)

var cmdPlotFlags = flag.NewFlagSet(os.Args[0]+" plot", flag.ExitOnError)

var (
	plotBaseline string
)

// Currently I'm plotting this using gnuplot:
//
// plot for [i=3:50] 'data' using 0:i index "ns/op" with l title columnhead

// TODO: HTML output using Google Charts?

// TODO: Low-pass filter?

func init() {
	f := cmdPlotFlags
	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s plot [flags] <revision range>\n", os.Args[0])
		f.PrintDefaults()
	}
	f.StringVar(&gitDir, "C", "", "run git in `dir`")
	f.StringVar(&plotBaseline, "baseline", "", "normalize to `revision`; revision may be \"first\" or \"last\"")
	registerSubcommand("plot", "[flags] <revision range> - print benchmark results", cmdPlot, f)
}

func cmdPlot() {
	if cmdPlotFlags.NArg() < 1 {
		cmdPlotFlags.Usage()
		os.Exit(2)
	}

	commits := getCommits(cmdPlotFlags.Args())

	// Put commits in forward chronological order.
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}

	// Load benchmark statistics.
	logFiles := []string{}
	for _, commit := range commits {
		if exists(commit.logPath) {
			logFiles = append(logFiles, commit.logPath)
		}
	}
	c := readFiles(logFiles...)
	for _, stat := range c.Stats {
		stat.ComputeStats()
	}

	// Get baseline commit.
	var baselineCommit *commitInfo
	if plotBaseline == "first" || plotBaseline == "last" {
		// Find the first/last commit with benchmark data.
		for _, commit := range commits {
			if c.ConfigSet[commit.logPath] {
				baselineCommit = commit
				if plotBaseline == "first" {
					break
				}
			}
		}
	} else if plotBaseline != "" {
		hash := trimNL(git("rev-parse", "--", plotBaseline))
		for _, commit := range commits {
			if hash == commit.hash {
				baselineCommit = commit
				break
			}
		}
		if baselineCommit == nil {
			fmt.Fprintf(os.Stderr, "baseline revision %s not found in revision range\n", hash)
			os.Exit(2)
		}
	}
	if baselineCommit != nil && !c.ConfigSet[baselineCommit.logPath] {
		fmt.Fprintf(os.Stderr, "no benchmark data for baseline commit\n")
		os.Exit(2)
	}

	// Print a table per unit.
	var key BenchKey
	baseline := make([]float64, 0)
	means := make([]float64, 0)
	for i, unit := range c.Units {
		key.Unit = unit
		if i > 0 {
			fmt.Printf("\n\n")
		}
		fmt.Printf("# %s\n", unit)

		// Print table of commit vs. benchmark mean.
		subc := c.Filter(BenchKey{Unit: unit})
		fmt.Printf("date commit geomean")
		for _, bench := range subc.Benchmarks {
			fmt.Printf(" %s", bench)
		}
		fmt.Printf("\n")

		// Get baseline numbers.
		baseline = baseline[:0]
		if baselineCommit == nil {
			for range subc.Benchmarks {
				baseline = append(baseline, 1)
			}
		} else {
			key.Config = baselineCommit.logPath
			for _, bench := range subc.Benchmarks {
				key.Benchmark = bench
				baseline = append(baseline, subc.Stats[key].Mean)
			}
		}

		for _, commit := range commits {
			key.Config = commit.logPath
			if !subc.ConfigSet[commit.logPath] {
				continue
			}

			means = means[:0]
			for _, bench := range subc.Benchmarks {
				key.Benchmark = bench
				means = append(means, subc.Stats[key].Mean)
			}

			fmt.Printf("%s %s %g", commit.commitDate.Format(time.RFC3339), commit.hash[:7], stats.GeoMean(means)/stats.GeoMean(baseline))
			for i, bench := range subc.Benchmarks {
				key.Benchmark = bench
				fmt.Printf(" %g", subc.Stats[key].Mean/baseline[i])
			}
			fmt.Printf("\n")
		}
	}
}
