// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package bench

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

func Print(bs []*Benchmark) error {
	return Fprint(os.Stdout, bs)
}

func Fprint(w io.Writer, bs []*Benchmark) error {
	type kv struct {
		k, v string
	}
	type block struct {
		config []kv
		bs     []*Benchmark
	}

	configKeys := func(b *Benchmark, inBlock bool) []string {
		var keys []string
		for k, config := range b.Config {
			if config.InBlock == inBlock {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		return keys
	}

	// Split bs into configuration blocks.
	blocks := []block{}
	lastConfig := map[string]string{}
	for _, b := range bs {
		// Find changed block configuration.
		var changed []kv
		for _, k := range configKeys(b, true) {
			config := b.Config[k]
			lc, ok := lastConfig[k]
			if ok && lc == config.RawValue {
				continue
			}

			changed = append(changed, kv{k, config.RawValue})
			lastConfig[k] = config.RawValue
		}

		if len(blocks) == 0 || changed != nil {
			// Start a new configuration block.
			blocks = append(blocks, block{changed, nil})
		}

		// Add benchmark to latest block.
		bbs := &blocks[len(blocks)-1].bs
		*bbs = append(*bbs, b)
	}

	// Format each configuration block.
	for i, block := range blocks {
		// Print configuration values.
		if i > 0 {
			if _, err := fmt.Fprint(w, "\n"); err != nil {
				return err
			}
		}
		for _, kv := range block.config {
			// TODO: Syntax check.
			if _, err := fmt.Fprintf(w, "%s: %s\n", kv.k, kv.v); err != nil {
				return err
			}
		}
		if len(block.config) > 0 {
			if _, err := fmt.Fprint(w, "\n"); err != nil {
				return err
			}
		}

		// Construct benchmark lines.
		lines := make([][]string, len(block.bs))
		for _, b := range block.bs {
			// Construct benchmark name.
			name := []string{"Benchmark" + b.Name}
			gomaxprocs, haveGMP := "", false
			for _, k := range configKeys(b, false) {
				config := b.Config[k]
				if k == "gomaxprocs" {
					gomaxprocs = config.RawValue
					haveGMP = true
					continue
				}
				// TODO: Syntax check.
				name = append(name, fmt.Sprintf("%s:%s", k, config.RawValue))
			}
			if haveGMP && gomaxprocs != "1" {
				if len(name) == 1 {
					// Use short form.
					name[0] = fmt.Sprintf("%s-%s", name[0], gomaxprocs)
				} else {
					name = append(name, fmt.Sprintf("gomaxprocs:%s", gomaxprocs))
				}
			}

			// Construct results.
			line := []string{
				strings.Join(name, "/"),
				fmt.Sprint(b.Iterations),
			}
			resultKeys := []string{}
			for k := range b.Result {
				resultKeys = append(resultKeys, k)
			}
			sort.Sort(resultKeySorter(resultKeys))
			for _, k := range resultKeys {
				result := b.Result[k]
				// TODO: Syntax check.
				line = append(line, fmt.Sprint(result), k)
			}

			lines = append(lines, line)
		}

		// Compute column widths.
		widths := make([]int, 0)
		for _, line := range lines {
			for i, elt := range line {
				if i >= len(widths) {
					widths = append(widths, len(elt))
				} else if len(elt) > widths[i] {
					widths[i] = len(elt)
				}
			}
		}

		// Print lines.
		for _, line := range lines {
			for i, elt := range line {
				var err error
				p := widths[i]
				if i == 1 || i >= 2 && i%2 == 0 {
					// Right align.
					_, err = fmt.Fprintf(w, "%*s  ", p, elt)
				} else if i < len(line)-1 {
					// Left align and pad.
					_, err = fmt.Fprintf(w, "%-*s  ", p, elt)
				} else {
					// Left align, no pad, EOL.
					_, err = fmt.Fprintf(w, "%s\n", elt)
				}
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

var fixedKeys = map[string]int{
	"ns/op": -2,
	"MB/s":  -1,
}

type resultKeySorter []string

func (s resultKeySorter) Len() int {
	return len(s)
}

func (s resultKeySorter) Less(i, j int) bool {
	if fixedKeys[s[i]] != fixedKeys[s[j]] {
		return fixedKeys[s[i]] < fixedKeys[s[j]]
	}

	return s[i] < s[j]
}

func (s resultKeySorter) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}
