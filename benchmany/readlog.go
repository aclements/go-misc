// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"io/ioutil"
	"log"
	"strconv"
	"strings"

	"github.com/aclements/go-moremath/stats"
)

// ComputeStats updates the derived statistics in s from the raw
// samples in s.Values.
func (stat *Benchstat) ComputeStats() {
	stat.Mean = stats.Mean(stat.Values)
}

// A Benchstat is the metrics along one axis (e.g., ns/op or MB/s)
// for all runs of a specific benchmark.
type Benchstat struct {
	Unit   string
	Values []float64 // metrics
	Mean   float64   // mean of Values
}

// A BenchKey identifies one metric (e.g., "ns/op", "B/op") from one
// benchmark (function name sans "Benchmark" prefix) in one
// configuration (input file name).
type BenchKey struct {
	Config, Benchmark, Unit string
}

type Collection struct {
	Stats map[BenchKey]*Benchstat

	// Keys gives all keys of Stats in the order added.
	Keys []BenchKey

	// Configs, Benchmarks, and Units give the set of configs,
	// benchmarks, and units from the keys in Stats in an order
	// meant to match the order the benchmarks were read in.
	Configs, Benchmarks, Units []string

	// ConfigSet, BenchmarkSet, and UnitSet are set
	// representations of Configs, Benchmarks, and Units.
	ConfigSet, BenchmarkSet, UnitSet map[string]bool
}

func (c *Collection) AddStat(key BenchKey) *Benchstat {
	if stat, ok := c.Stats[key]; ok {
		return stat
	}

	c.addKey(key)
	stat := &Benchstat{Unit: key.Unit}
	c.Stats[key] = stat
	return stat
}

func (c *Collection) addKey(key BenchKey) {
	addString := func(strings *[]string, set map[string]bool, add string) {
		if set[add] {
			return
		}
		*strings = append(*strings, add)
		set[add] = true
	}
	c.Keys = append(c.Keys, key)
	addString(&c.Configs, c.ConfigSet, key.Config)
	addString(&c.Benchmarks, c.BenchmarkSet, key.Benchmark)
	addString(&c.Units, c.UnitSet, key.Unit)
}

func (c *Collection) Filter(key BenchKey) *Collection {
	c2 := NewCollection()
	for _, k := range c.Keys {
		if (key.Config == "" || key.Config == k.Config) &&
			(key.Benchmark == "" || key.Benchmark == k.Benchmark) &&
			(key.Unit == "" || key.Unit == k.Unit) {
			c2.addKey(k)
			c2.Stats[k] = c.Stats[k]
		}
	}
	return c2
}

func NewCollection() *Collection {
	return &Collection{
		Stats:        make(map[BenchKey]*Benchstat),
		ConfigSet:    make(map[string]bool),
		BenchmarkSet: make(map[string]bool),
		UnitSet:      make(map[string]bool),
	}
}

// readFiles reads a set of benchmark files as a Collection.
func readFiles(files ...string) *Collection {
	c := NewCollection()
	for _, file := range files {
		readFile(file, c)
	}
	return c
}

var unitOfXMetric = map[string]string{
	"time":           "ns/op",
	"allocated":      "allocated bytes/op",      // ΔMemStats.TotalAlloc / N
	"allocs":         "allocs/op",               // ΔMemStats.Mallocs / N
	"sys-total":      "bytes from system",       // MemStats.Sys
	"sys-heap":       "heap bytes from system",  // MemStats.HeapSys
	"sys-stack":      "stack bytes from system", // MemStats.StackSys
	"sys-gc":         "GC bytes from system",    // MemStats.GCSys
	"sys-other":      "other bytes from system", // MemStats.OtherSys+MSpanSys+MCacheSys+BuckHashSys
	"gc-pause-total": "STW ns/op",               // ΔMemStats.PauseTotalNs / N
	"gc-pause-one":   "STW ns/GC",               // ΔMemStats.PauseTotalNs / ΔNumGC
	"rss":            "max RSS bytes",           // Rusage.Maxrss * 1<<10
	"cputime":        "user+sys ns/op",          // Rusage.Utime+Stime
	"virtual-mem":    "peak VM bytes",           // /proc/self/status VmPeak
}

// readFile reads a set of benchmarks from a file in to a Collection.
func readFile(file string, c *Collection) {
	c.Configs = append(c.Configs, file)
	key := BenchKey{Config: file}

	text, err := ioutil.ReadFile(file)
	if err != nil {
		log.Fatal(err)
	}
	for _, line := range strings.Split(string(text), "\n") {
		if strings.HasPrefix(line, "GOPERF-METRIC:") {
			// x/benchmarks-style output.
			line := line[14:]
			f := strings.Split(line, "=")
			val, err := strconv.ParseFloat(f[1], 64)
			if err != nil {
				continue
			}
			key.Benchmark = f[0]
			key.Unit = unitOfXMetric[f[0]]
			if key.Unit == "" {
				continue
			}
			stat := c.AddStat(key)
			stat.Values = append(stat.Values, val)
			continue
		}

		f := strings.Fields(line)
		if len(f) < 4 {
			continue
		}
		name := f[0]
		if !strings.HasPrefix(name, "Benchmark") {
			continue
		}
		name = strings.TrimPrefix(name, "Benchmark")
		n, _ := strconv.Atoi(f[1])
		if n == 0 {
			continue
		}

		key.Benchmark = name
		for i := 2; i+2 <= len(f); i += 2 {
			val, err := strconv.ParseFloat(f[i], 64)
			if err != nil {
				continue
			}
			key.Unit = f[i+1]
			stat := c.AddStat(key)
			stat.Values = append(stat.Values, val)
		}
	}
}
