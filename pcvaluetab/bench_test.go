// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
)

var flagBinary stringList

func init() {
	flag.Var(&flagBinary, "bench-binary", "use PCDATA from `binary` for benchmarks; can be given multiple times")
}

type stringList struct {
	list []string
}

func (l *stringList) String() string {
	return strings.Join(l.list, ",")
}

func (l *stringList) Set(x string) error {
	l.list = append(l.list, x)
	return nil
}

func BenchmarkDecode(b *testing.B) {
	if len(flagBinary.list) == 0 {
		b.Skip("-bench-binary not set")
	}

	for _, bin := range flagBinary.list {
		b.Run(filepath.Base(bin), func(b *testing.B) {
			decode1(b, bin)
		})
	}
}

func decode1(b *testing.B, bin string) {
	symtab := LoadSymTab(bin)

	// Random sample of tables.
	const nSamples = 1024
	type sample struct {
		varintTab *VarintPCData
		altTab    []byte
		textLen   uint32
		pc        uint32
	}
	samples := make([]sample, nSamples)
	for i := range samples {
		// Pick a random table.
		var tab *VarintPCData
		for _, tab = range symtab.PCTabs {
			break
		}
		// Re-encode it.
		altTab := linearIndex(tab)
		// Pick a random PC.
		pc := uint32(rand.Intn(int(tab.TextLen)))

		samples[i] = sample{tab, altTab, tab.TextLen, pc}
	}

	b.Run("varint-cache-nohit", func(b *testing.B) {
		var cache pcvalueCache
		for i := 0; i < b.N; i++ {
			// In practice this will never hit in the cache because there
			// are so many random samples.
			sample := &samples[i%len(samples)]
			lookupVarintPCData(sample.varintTab.Raw, uintptr(sample.pc), &cache)
		}
	})
	b.Run("varint-cache-hit", func(b *testing.B) {
		var cache pcvalueCache
		for i := 0; i < b.N; i++ {
			// Hit 7 times out of 8. That's probably dramatically higher
			// than the hit rate in real applications.
			sample := &samples[(i/8)%len(samples)]
			lookupVarintPCData(sample.varintTab.Raw, uintptr(sample.pc), &cache)
		}
	})
	b.Run("varint-cache-none", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sample := &samples[i%len(samples)]
			lookupVarintPCData(sample.varintTab.Raw, uintptr(sample.pc), nil)
		}
	})
	b.Run("alt", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sample := &samples[i%len(samples)]
			lookupLinearIndex(sample.altTab, sample.textLen, sample.pc)
		}
	})
}
