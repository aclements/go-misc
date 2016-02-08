// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"log"
	"math"
	"os"
	"strings"
	"time"

	"github.com/aclements/go-moremath/stats"
)

var cmdPlotFlags = flag.NewFlagSet(os.Args[0]+" plot", flag.ExitOnError)

var plot struct {
	baseline string
	json     bool
	html     bool
	title    string
	filter   bool
}

// To plot this in Gnuplot, use for example:
//
// plot for [i=3:50] 'data' using 0:i index "ns/op" with l title columnhead

func init() {
	f := cmdPlotFlags
	f.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s plot [flags] <revision range>\n", os.Args[0])
		f.PrintDefaults()
	}
	f.StringVar(&gitDir, "C", "", "run git in `dir`")
	f.StringVar(&outDir, "o", "", "read binaries and logs from `directory`")
	f.StringVar(&plot.baseline, "baseline", "", "normalize to `revision`; revision may be \"first\" or \"last\"")
	f.BoolVar(&plot.json, "json", false, "emit data in JSON")
	f.BoolVar(&plot.html, "html", false, "emit data in HTML")
	f.StringVar(&plot.title, "title", "", "title for HTML output")
	f.BoolVar(&plot.filter, "filter", false, "KZA filter benchmark results to reduce noise")
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
	if plot.baseline == "first" || plot.baseline == "last" {
		// Find the first/last commit with benchmark data.
		for _, commit := range commits {
			if c.ConfigSet[commit.logPath] {
				baselineCommit = commit
				if plot.baseline == "first" {
					break
				}
			}
		}
	} else if plot.baseline != "" {
		hash := trimNL(git("rev-parse", "--", plot.baseline))
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

	// Build tables.
	var tables []*Table
	var units []string
	var key BenchKey
	baseline := make([]float64, 0)
	means := make([]float64, 0)
	for _, unit := range c.Units {
		key.Unit = unit
		table := NewTable()
		tables = append(tables, table)
		units = append(units, unit)
		if unit == "ns/op" {
			units[len(units)-1] = "op/ns"
		}

		subc := c.Filter(BenchKey{Unit: unit})

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

		// Build columns.
		dateCol, commitCol, idxCol := []string{}, []string{}, []int{}
		geomeanCol, benchCols := []float64{}, make([][]float64, len(subc.Benchmarks))
		baselineIdx := -1
		for i, commit := range commits {
			key.Config = commit.logPath
			if !subc.ConfigSet[commit.logPath] {
				continue
			}
			if commit == baselineCommit {
				baselineIdx = len(dateCol)
			}

			means = means[:0]
			for _, bench := range subc.Benchmarks {
				key.Benchmark = bench
				means = append(means, subc.Stats[key].Mean)
			}

			dateCol = append(dateCol, commit.commitDate.Format(time.RFC3339))
			commitCol = append(commitCol, commit.hash[:7])
			idxCol = append(idxCol, i)
			geomeanCol = append(geomeanCol, stats.GeoMean(means)/stats.GeoMean(baseline))
			for i, bench := range subc.Benchmarks {
				key.Benchmark = bench
				benchCols[i] = append(benchCols[i], subc.Stats[key].Mean/baseline[i])
			}
			if unit == "ns/op" {
				j := len(geomeanCol) - 1
				geomeanCol[j] = 1 / geomeanCol[j]
				for i := range benchCols {
					benchCols[i][j] = 1 / benchCols[i][j]
				}
			}
		}

		// Filter the columns.
		if plot.filter {
			geomeanCol = AdaptiveKolmogorovZurbenko(geomeanCol, 15, 3)
			for i := range benchCols {
				benchCols[i] = AdaptiveKolmogorovZurbenko(benchCols[i], 15, 3)
			}
		}

		// Normalize the columns.
		if baselineCommit != nil {
			if baselineIdx == -1 {
				fmt.Fprintf(os.Stderr, "baseline commit has no data for %s\n", key.Unit)
				os.Exit(2)
			}
			divideCol(geomeanCol, geomeanCol[baselineIdx])
			for _, benchCol := range benchCols {
				divideCol(benchCol, benchCol[baselineIdx])
			}
		}

		// Trim the number of significant figures.
		roundCol(geomeanCol, 5)
		for _, benchCol := range benchCols {
			roundCol(benchCol, 5)
		}

		// Build the table.
		table.AddColumn("date", dateCol)
		table.AddColumn("i", idxCol)
		table.AddColumn("commit", commitCol)
		if len(benchCols) > 1 {
			table.AddColumn("geomean", geomeanCol)
		}
		for i, bench := range subc.Benchmarks {
			table.AddColumn(bench, benchCols[i])
		}
	}

	if plot.json || plot.html {
		var jsonTables []JSONTable
		for i, table := range tables {
			jsonTables = append(jsonTables, JSONTable{units[i], table})
		}
		if plot.json {
			if err := json.NewEncoder(os.Stdout).Encode(jsonTables); err != nil {
				log.Fatal(err)
			}
		} else {
			title := plot.title
			if plot.filter {
				title += ". Low-pass filtered with KZA(15,3)."
			}

			t := template.Must(template.New("plot").Parse(plotHTML))
			err := t.Execute(os.Stdout, map[string]interface{}{
				"Revs":   strings.Join(cmdPlotFlags.Args(), " "),
				"Tables": jsonTables,
				"Title":  title,
			})
			if err != nil {
				log.Fatal(err)
			}
		}
	} else {
		// Print tables.
		for i, table := range tables {
			if i > 0 {
				fmt.Printf("\n\n")
			}
			fmt.Printf("# %s\n", units[i])
			if err := table.WriteTSV(os.Stdout, true); err != nil {
				log.Fatal(err)
			}
		}
	}
}

type JSONTable struct {
	Unit string
	*Table
}

func divideCol(xs []float64, by float64) {
	for i, x := range xs {
		xs[i] = x / by
	}
}

func roundCol(xs []float64, sigfigs int) {
	for i, x := range xs {
		if x == 0 {
			continue
		}
		f := math.Pow(10, float64(sigfigs)-math.Ceil(math.Log10(math.Abs(x))))
		xs[i] = math.Floor(x*f+0.5) / f
	}
}
