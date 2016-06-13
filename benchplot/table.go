// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"math"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/aclements/go-gg/table"
	"github.com/aclements/go-misc/bench"
)

func benchmarksToTable(bs []*bench.Benchmark) (t *table.Table, configCols, resultCols []string) {
	// Gather name, config, and result columns.
	nan := math.NaN()
	names := make([]string, len(bs))
	configs, results := map[string]reflect.Value{}, map[string][]float64{}
	for i, b := range bs {
		names[i] = b.Name

		for k, c := range b.Config {
			seq, ok := configs[k]
			if !ok {
				t := reflect.SliceOf(reflect.TypeOf(c.Value))
				seq = reflect.MakeSlice(t, len(bs), len(bs))
				configs[k] = seq
			}
			seq.Index(i).Set(reflect.ValueOf(c.Value))
		}

		for k, v := range b.Result {
			seq, ok := results[k]
			if !ok {
				seq = make([]float64, len(bs))
				for i := range seq {
					seq[i] = nan
				}
				results[k] = seq
			}
			seq[i] = v
		}
	}

	// Build table.
	tab := new(table.Builder).Add("name", names)

	keys := make([]string, 0, len(configs))
	for k := range configs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		nicekey := strings.Replace(key, "-", " ", -1)
		niceval := configs[key].Interface()
		if n, ok := niceval.([]time.Time); ok {
			niceval = byTime(n)
		}

		tab.Add(nicekey, niceval)
		configCols = append(configCols, nicekey)
	}

	keys = make([]string, 0, len(results))
	for k := range results {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		nicekey := strings.Replace(key, "-", " ", -1)
		if nicekey == "ns/op" {
			// TODO: Use the unit parser from benchstat.
			nicekey = "time/op"
			durations := make([]time.Duration, len(results[key]))
			for i, x := range results[key] {
				durations[i] = time.Duration(x)
			}
			tab.Add(nicekey, durations)
		} else {
			tab.Add(nicekey, results[key])
		}
		resultCols = append(resultCols, nicekey)
	}

	return tab.Done(), configCols, resultCols
}

func commitsToTable(commits []CommitInfo) *table.Table {
	hashCol := make([]string, len(commits))
	authorDateCol := make(byTime, len(commits))
	commitDateCol := make(byTime, len(commits))
	branchCol := make([]string, len(commits))
	j := 0
	for i := range commits {
		ci := &commits[i]

		hashCol[j] = ci.Hash
		authorDateCol[j] = ci.AuthorDate
		commitDateCol[j] = ci.CommitDate
		branchCol[j] = ci.Branch
		j++
	}

	return new(table.Builder).
		Add("commit", hashCol).
		Add("author date", authorDateCol).
		Add("commit date", commitDateCol).
		Add("branch", branchCol).
		Done()
}

type byTime []time.Time

func (s byTime) Len() int {
	return len(s)
}

func (s byTime) Less(i, j int) bool {
	return s[i].Before(s[j])
}

func (s byTime) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
