package main

import (
	"fmt"
	"log"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

type UnifiedMethod struct {
	// Ex is an example method for this group. NormName, NormSig, and IsMethod must match this.
	Ex *NormalizedMethod
	// NormName is the normalized operation name shared across merged implementations.
	NormName string
	// NormSig is the normalized signature string shared across merged implementations.
	NormSig string
	// IsMethod is true if this operation belongs to a receiver type rather than a standalone package function.
	IsMethod bool
	// Implementations maps receiver base types (e.g., "Int32", "Mask8") to their active bit widths (128, 256, 512).
	// Used by the printer to efficiently fill the width table cells.
	Implementations map[string]map[int]*NormalizedMethod
}

func groupMethods(methods []*NormalizedMethod) []*UnifiedMethod {
	type methodSortKey struct {
		category string
		name     string
		sig      string
		isMethod bool
	}

	rowMap := make(map[methodSortKey]*UnifiedMethod)
	var result []*UnifiedMethod

	for _, m := range methods {
		key := methodSortKey{m.Method.GetCategory(), m.NormName, m.NormSig, m.Method.IsMethod}
		row, exists := rowMap[key]

		if !exists {
			row = &UnifiedMethod{
				Ex:              m,
				NormName:        m.NormName,
				NormSig:         m.NormSig,
				IsMethod:        m.Method.IsMethod,
				Implementations: make(map[string]map[int]*NormalizedMethod),
			}
			rowMap[key] = row
			result = append(result, row)
		}

		BN := fmt.Sprintf("%s%d", m.Method.Receiver.Base(), m.Method.Receiver.ElemBits())
		width := m.Method.Receiver.ElemBits() * m.Method.Receiver.Lanes()

		if row.Implementations[BN] == nil {
			row.Implementations[BN] = make(map[int]*NormalizedMethod)
		}
		if row.Implementations[BN][width] != nil {
			log.Fatalf("duplicate normalized methods %s at %s %d", m.NormName, BN, width)
		}
		row.Implementations[BN][width] = m
	}

	return result
}

func printMethodBlocks(extMeta []*UnifiedMethod) {
	sort.Slice(extMeta, func(i, j int) bool {
		if extMeta[i].NormName != extMeta[j].NormName {
			return lessNatural(extMeta[i].NormName, extMeta[j].NormName)
		}
		if extMeta[i].IsMethod != extMeta[j].IsMethod {
			return extMeta[i].IsMethod
		}
		return extMeta[i].NormSig < extMeta[j].NormSig
	})

	buckets := make(map[string][]*UnifiedMethod)
	bucketOrder := []string{
		"Ungrouped",
		"Load/Store",
		"Math",
		"Numeric conversion",
		"Comparison",
		"Rounding",
		"Logical",
		"Bit math",
		"Shuffles",
		"Shifts",
		"Crypto",
		"Reinterpretation",
		"Mask",
	}

	for _, m := range extMeta {
		category := m.Ex.Method.GetCategory()
		if !slices.Contains(bucketOrder, category) {
			panic("unknown category " + category)
		}
		buckets[category] = append(buckets[category], m)
	}

	fmt.Println("## Table of Contents")
	for _, bName := range bucketOrder {
		if len(buckets[bName]) > 0 {
			link := "#" + strings.ToLower(bName)
			link = strings.ReplaceAll(link, " ", "-")
			link = strings.ReplaceAll(link, "/", "")
			fmt.Printf("- [%s](%s)\n", bName, link)
		}
	}
	fmt.Println()

	allWidths := []int{128, 256, 512}

	for _, bName := range bucketOrder {
		activeMethods := buckets[bName]
		if len(activeMethods) == 0 {
			continue
		}

		fmt.Printf("## %s\n\n", bName)

		for _, method := range activeMethods {
			sig := strings.TrimSpace(method.NormSig)
			if after, ok := strings.CutPrefix(sig, "func"); ok {
				sig = strings.TrimSpace(after)
			}

			basesMap := make(map[string]bool)
			standardWidths := []int{8, 16, 32, 64}
			floatWidths := []int{32, 64}

			for base := range method.Implementations {
				if strings.HasPrefix(base, "Float") {
					for _, w := range floatWidths {
						basesMap["Float"+strconv.Itoa(w)] = true
					}
				} else if strings.HasPrefix(base, "Mask") {
					basesMap[base] = true
				} else {
					var bareBase string
					if strings.HasPrefix(base, "Int") {
						bareBase = "Int"
					} else {
						bareBase = "Uint"
					}
					for _, w := range standardWidths {
						basesMap[bareBase+strconv.Itoa(w)] = true
					}
				}
			}

			var activeBases []string
			for k := range basesMap {
				activeBases = append(activeBases, k)
			}
			sort.Slice(activeBases, func(i, j int) bool {
				bI, bJ := activeBases[i], activeBases[j]
				mI := strings.HasPrefix(bI, "Mask")
				mJ := strings.HasPrefix(bJ, "Mask")
				if mI != mJ {
					return !mI
				}

				m1 := baseRegex.FindStringSubmatch(bI)
				m2 := baseRegex.FindStringSubmatch(bJ)
				if len(m1) > 2 && len(m2) > 2 {
					if m1[1] != m2[1] {
						return m1[1] < m2[1]
					}
					bits1, _ := strconv.Atoi(m1[2])
					bits2, _ := strconv.Atoi(m2[2])
					return bits1 < bits2
				}
				return bI < bJ
			})

			prefix := ""
			if method.IsMethod {
				prefix = "T."
			}
			fmt.Printf("### `%s%s%s`\n\n", prefix, method.NormName, sig)

			if doc := method.Ex.NormDoc; doc != "" {
				fmt.Printf("%s\n\n", doc)

				// Check for other docs
				var docs []string
				docSet := map[string]bool{doc: true}
				for _, width := range allWidths {
					for _, b := range activeBases {
						m := method.Implementations[b][width]
						if m == nil {
							continue
						}
						if !docSet[m.NormDoc] {
							docSet[m.NormDoc] = true
							docs = append(docs, m.NormDoc)
						}
					}
				}
				if len(docs) > 0 {
					fmt.Printf("<details><summary>(plus %d other doc variants)</summary>\n", len(docs))
					for _, doc := range docs {
						fmt.Printf("<p>%s</p>\n", doc)
					}
					fmt.Printf("</details>\n\n")
				}
			}

			fmt.Print("| Width |")
			for _, b := range activeBases {
				fmt.Printf(" %s |", b)
			}
			fmt.Println()

			fmt.Print("| --- |")
			for range activeBases {
				fmt.Print(" --- |")
			}
			fmt.Println()

			inRounded, outRounded := false, false
			for _, width := range allWidths {
				fmt.Printf("| %d |", width)
				for _, b := range activeBases {
					mGroup := baseRegex.FindStringSubmatch(b)
					bits, _ := strconv.Atoi(mGroup[2])
					lanes := width / bits

					implemented := method.Implementations[b][width]
					if implemented != nil {
						fmt.Printf(" `x%d`", lanes)
						if implemented.InRounded {
							fmt.Printf("†")
							inRounded = true
						}
						if implemented.OutRounded {
							fmt.Printf("‡")
							outRounded = true
						}
						fmt.Printf(" |")
					} else {
						fmt.Print("  |")
					}
				}
				fmt.Println()
			}
			fmt.Println()

			if inRounded {
				fmt.Println("† Argument width rounded up to minimum viable size.")
				fmt.Println()
			}
			if outRounded {
				fmt.Println("‡ Result width rounded up to minimum viable size.")
				fmt.Println()
			}
		}
	}
}

func lessNatural(a, b string) bool {
	chunksA := splitNatural(a)
	chunksB := splitNatural(b)

	n := len(chunksA)
	if len(chunksB) < n {
		n = len(chunksB)
	}

	for i := 0; i < n; i++ {
		cA, cB := chunksA[i], chunksB[i]
		digitA := cA[0] >= '0' && cA[0] <= '9'
		digitB := cB[0] >= '0' && cB[0] <= '9'

		if digitA && digitB {
			intA, _ := strconv.Atoi(cA)
			intB, _ := strconv.Atoi(cB)
			if intA != intB {
				return intA < intB
			}
		} else if cA != cB {
			return cA < cB
		}
	}

	return len(chunksA) < len(chunksB)
}

var chunkRegex = regexp.MustCompile(`(\d+)|(\D+)`)

func splitNatural(s string) []string {
	return chunkRegex.FindAllString(s, -1)
}
