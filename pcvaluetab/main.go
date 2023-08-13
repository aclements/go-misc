// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// pcvaluetab is an experiment with alternate pcvalue encodings.
//
// Usage: pcvaluetab {binary}
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"

	"golang.org/x/exp/maps"
)

func main() {
	const debug = false
	const debugCheckDecode = true

	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}
	binPath := flag.Arg(0)

	var fileBytes int
	if stat, err := os.Stat(binPath); err != nil {
		log.Fatal(err)
	} else {
		fileBytes = int(stat.Size())
	}

	symtab := LoadSymTab(binPath)

	// Walk the funcs.
	var fnSizes Dist
	var funcBytes int
	var tabOffsetDist Dist
	refBytes := 0
	type tabInfo struct {
		tab   *VarintPCData
		alt   []byte
		count int
	}
	dups := make(map[PCTabKey]*tabInfo)
	altDups := make(map[string]int)
	for _, fn := range symtab.Funcs {
		if debug {
			fmt.Printf("%+v\n", fn)
		}

		funcBytes += fn.EncSize

		for _, pcTabKey := range fn.PCTabs {
			tabOffsetDist.Add(int(pcTabKey))

			refBytes += 4
			if pcTabKey == 0 {
				// Unused.
				continue
			}

			info := dups[pcTabKey]
			if info == nil {
				info = new(tabInfo)
				dups[pcTabKey] = info

				info.tab = symtab.PCTabs[pcTabKey]
				info.alt = linearIndex(info.tab)
				if debug {
					fmt.Printf("%+v\n", info.tab)
					fmt.Printf("% 3x\n", info.alt)
					if len(info.alt) > len(info.tab.Raw) {
						fmt.Println("LONGER", len(info.alt), len(info.tab.Raw))
					}
				}

				if debugCheckDecode {
					for pc := uint32(0); pc < info.tab.TextLen; pc++ {
						want := info.tab.Lookup(pc)
						got := lookupLinearIndex(info.alt, info.tab.TextLen, pc)

						if want != got {
							log.Fatalf("at PC %d, want %d, got %d", pc, want, got)
						}
					}
				}
			}
			info.count++

			// Add to the altDups table. The alternate encoding might
			// deduplicate better than the varint encoding, so we count this
			// separately.
			altDups[string(info.alt)]++
		}

		fnSizes.Add(fn.TextLen)
	}

	diffPct := func(before, after int) float64 {
		return float64(100*after)/float64(before) - 100
	}

	// TODO: Does it make sense to combine all of the tables of a function into
	// one indexed by (tableID * pcLen) + pc? Then we really can't dedup, but we
	// could put this combined table right after the func_ and not need the
	// references. Is there any way we could combine this with optional
	// deduplication?

	fmt.Printf("file: %d bytes\n", fileBytes)
	fmt.Printf("functab: %d bytes\n", funcBytes)
	fmt.Printf("refs: %d bytes\n", refBytes)
	fmt.Printf("function sizes:\n%s\n", fnSizes.StringSummary())
	fmt.Printf("pcdata table offsets:\n%s\n", tabOffsetDist.StringSummary())
	fmt.Println()

	fmt.Printf("## varint encoding\n")
	postDedupBytes := 0
	preDedupBytes := 0
	var sizes Dist
	for _, info := range dups {
		size := len(info.tab.Raw)
		postDedupBytes += size
		preDedupBytes += size * info.count
		sizes.Add(size)
	}
	fmt.Printf("tabs: %d bytes post-dedup\n%s\n", postDedupBytes, sizes.StringSummary())
	fmt.Printf("tabs: %d bytes pre-dedup\n", preDedupBytes)
	fmt.Printf("dedup saves: %d bytes\n", preDedupBytes-postDedupBytes)
	if true {
		dedupCountBySize := make(map[int]int)
		for _, info := range dups {
			dedupCountBySize[len(info.tab.Raw)] += info.count
		}

		fmt.Printf("duplicates by size:\n")
		fmt.Printf("%7s %7s %7s:\n", "size", "#dups", "saving")
		sizes := maps.Keys(dedupCountBySize)
		sort.Ints(sizes)
		for _, size := range sizes {
			fmt.Printf("%7d %7d %7d\n", size, dedupCountBySize[size], size*dedupCountBySize[size])
		}
	}
	fmt.Println()

	fmt.Printf("## alternate encoding\n")
	altPostDedupBytes := 0
	altPreDedupBytes := 0
	var altSizes Dist
	for alt, count := range altDups {
		altPostDedupBytes += len(alt)
		altPreDedupBytes += len(alt) * count
		altSizes.Add(len(alt))
	}
	fmt.Printf("tabs: %d bytes post-dedup (%+f%% vs varint)\n%s\n", altPostDedupBytes, diffPct(postDedupBytes, altPostDedupBytes), altSizes.StringSummary())
	fmt.Printf("tabs: %d bytes pre-dedup\n", altPreDedupBytes)
	fmt.Printf("dedup saves: %d bytes\n", altPreDedupBytes-altPostDedupBytes)
	fmt.Printf("file size change: %+f%%\n", diffPct(fileBytes, fileBytes-postDedupBytes+altPostDedupBytes))
}
