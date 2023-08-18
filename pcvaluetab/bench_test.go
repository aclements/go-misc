// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var flagBinary stringList
var flagSizeStats = flag.Bool("size-stats", true, "report file and table size statistics (disable for profiling)")

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
		b.Run("bin="+filepath.Base(bin), func(b *testing.B) {
			symtab := LoadSymTab(bin)

			for _, pcMode := range []uint32{pcModeRandom, 0, 4096} {
				label := fmt.Sprint(pcMode)
				if pcMode == pcModeRandom {
					label = "random"
				}

				b.Run("pc="+label, func(b *testing.B) {
					decode1(b, symtab, pcMode)
				})
			}
			if *flagSizeStats {
				fileSizeStats(b, bin, symtab)
			}
		})
	}
}

func fileSizeStats(b *testing.B, bin string, symtab *SymTab) {
	var fileBytes int
	if stat, err := os.Stat(bin); err != nil {
		b.Fatal(err)
	} else {
		fileBytes = int(stat.Size())
	}

	var varintBytes, altBytes int
	gather := sync.OnceFunc(func() {
		// Collect the total size of the varint and alt tables.
		altDups := map[string]bool{}
		for _, tab := range symtab.PCTabs {
			varintBytes += len(tab.Raw)

			// Re-encode the varint tables.
			altTab := linearIndex(tab)
			if altDups[string(altTab)] {
				continue
			}
			altDups[string(altTab)] = true
			altBytes += len(altTab)
		}
	})

	// This is a bit goofy. We're not measuring time, so the normal testing.B
	// looping doesn't work. We start a sub-benchmark so that -test.bench
	// selection works, and then print out own result and call SkipNow to
	// prevent looping. This hack means results won't align nicely, and if this
	// this is the first "benchmark" to run, our results will appear before the
	// benchmark tags.

	b.Run("enc=varint", func(b *testing.B) {
		gather()
		fmt.Printf("%s\t%d\t%v table-bytes\t%v file-bytes\n", b.Name(), 1, varintBytes, fileBytes)
		b.SkipNow()
	})

	b.Run("enc=alt", func(b *testing.B) {
		gather()
		altFileBytes := fileBytes - varintBytes + altBytes
		fmt.Printf("%s\t%d\t%v table-bytes\t%v file-bytes\n", b.Name(), 1, altBytes, altFileBytes)
		// Metrics get kind of weird with "percent change", so just log it.
		fmt.Printf("file-bytes alt versus varint: %+f%%\n", diffPct(fileBytes, altFileBytes))
		b.SkipNow()
	})
}

const pcModeRandom = math.MaxUint32

func decode1(b *testing.B, symtab *SymTab, pcMode uint32) {
	// Random sample of tables.
	const nSamples = 1024
	type sample struct {
		varintTab *VarintPCData
		altTab    []byte
		textLen   uint32
		pc        uint32
	}
	samples := make([]sample, 0, nSamples)
	for len(samples) < nSamples {
		// Pick a random table.
		var tab *VarintPCData
		for _, tab = range symtab.PCTabs {
			break
		}
		// Pick a PC.
		pc := pcMode
		if pcMode == math.MaxUint32 {
			pc = uint32(rand.Intn(int(tab.TextLen)))
		} else if pc >= tab.TextLen {
			// Try again
			continue
		}
		// Re-encode it.
		altTab := linearIndex(tab)

		samples = append(samples, sample{tab, altTab, tab.TextLen, pc})
	}

	b.Run("enc=varint", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sample := &samples[i%len(samples)]
			lookupVarintPCData(sample.varintTab.Raw, uintptr(sample.pc), nil)
		}
	})
	b.Run("enc=varint/cachehit=0", func(b *testing.B) {
		var cache pcvalueCache
		for i := 0; i < b.N; i++ {
			// In practice this will never hit in the cache because there
			// are so many random samples.
			sample := &samples[i%len(samples)]
			lookupVarintPCData(sample.varintTab.Raw, uintptr(sample.pc), &cache)
		}
	})
	b.Run("enc=varint/cachehit=7:1", func(b *testing.B) {
		var cache pcvalueCache
		for i := 0; i < b.N; i++ {
			// Hit 7 times out of 8. That's probably dramatically higher
			// than the hit rate in real applications.
			sample := &samples[(i/8)%len(samples)]
			lookupVarintPCData(sample.varintTab.Raw, uintptr(sample.pc), &cache)
		}
	})
	b.Run("enc=alt", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sample := &samples[i%len(samples)]
			lookupLinearIndex(sample.altTab, sample.textLen, sample.pc)
		}
	})
}
