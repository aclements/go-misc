// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
)

var sinkInt int

func TestCount0124(t *testing.T) {
	var src strings.Builder
	fmt.Fprintf(&src, "var count0124Tab = [...]uint8{\n")

	for x := 0; x < 256; x++ {
		want := count0124Slow(uint8(x))
		gotFormula := count0124Formula(uint8(x))
		gotTable := count0124(uint8(x))

		t.Logf("%#08b => %d %d %d", x, want, gotFormula, gotTable)
		if want != gotFormula || want != gotTable {
			t.Errorf("count implementations differ for x=%#08b", x)
		}

		fmt.Fprintf(&src, "%d,", want)
		if x%16 == 15 {
			fmt.Fprintf(&src, "\n")
		}
	}

	src.WriteByte('}')
	t.Log(src.String())
}

func BenchmarkCount0124(b *testing.B) {
	// Generate test data.
	var data [1024]byte // Must be a power of 2 for optimal codegen
	rand.Read(data[:])

	b.Run("formula", func(b *testing.B) {
		var sink uint
		for i := 0; i < b.N; i++ {
			sink = count0124Formula(data[i&(len(data)-1)])
		}
		sinkInt = int(sink)
	})
	b.Run("slow", func(b *testing.B) {
		var sink uint
		for i := 0; i < b.N; i++ {
			sink = count0124Slow(data[i&(len(data)-1)])
		}
		sinkInt = int(sink)
	})
	b.Run("table", func(b *testing.B) {
		var sink uint
		for i := 0; i < b.N; i++ {
			sink = count0124(data[i&(len(data)-1)])
		}
		sinkInt = int(sink)
	})
}

var flagBinary = flag.String("bench-binary", "", "use PCDATA from `binary` for benchmarks")

func BenchmarkDecode(b *testing.B) {
	if *flagBinary == "" {
		b.Skip("-bench-binary not set")
	}

	symtab := LoadSymTab(*flagBinary)

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

	b.Run(filepath.Base(*flagBinary), func(b *testing.B) {
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
	})
}
