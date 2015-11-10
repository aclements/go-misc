// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
)

func round(x float64) int {
	return int(x + 0.5)
}

func pct(x float64) string {
	p := 100 * x
	if p >= 9.5 {
		return fmt.Sprintf("%.0f%%", p)
	} else if p > 0.95 {
		return fmt.Sprintf("%.1f%%", p)
	} else {
		return fmt.Sprintf("%.2f%%", p)
	}
}

func printTextReport(w io.Writer, classes []*failureClass) {
	for _, fc := range classes {
		fmt.Fprintf(w, "%s\n", fc.Class)
		printTextFlakeReport(w, fc)
		fmt.Fprintln(w)
	}
}

func printTextFlakeReport(w io.Writer, fc *failureClass) {
	// TODO: Report deterministic failures better.
	//
	// TODO: Report observed OSs/Arches

	fmt.Fprintf(w, "First observed %s (%d commits ago)\n", fc.Revs[fc.Latest.First], len(fc.Revs)-fc.Latest.First-1)
	fmt.Fprintf(w, "Last observed  %s (%d commits ago)\n", fc.Revs[fc.Latest.Last], len(fc.Revs)-fc.Latest.Last-1)
	fmt.Fprintf(w, "%s chance failure is still happening\n", pct(fc.Current))

	if fc.Latest.First == fc.Latest.Last {
		fmt.Fprintf(w, "Isolated failure\n")
	} else {
		fmt.Fprintf(w, "%s failure probability (%d of %d commits)\n", pct(fc.Latest.FailureProbability), fc.Latest.Failures, fc.Latest.Last-fc.Latest.First+1)
		fmt.Fprintf(w, "Likely culprits:\n")
		for _, c := range fc.Latest.Culprits(0.9, 10) {
			fmt.Fprintf(w, "  %3d%% %s\n", round(100*c.P), fc.Revs[c.T].OneLine())
		}
	}

	if len(fc.Test.All) > 1 {
		fmt.Fprintf(w, "Past failures:\n")
		for _, reg := range fc.Test.All[1:] {
			if reg.First == reg.Last {
				rev := fc.Revs[reg.First]
				fmt.Fprintf(w, "  %s (isolated failure)\n", rev)
			} else {
				fmt.Fprintf(w, "  %s to %s\n", fc.Revs[reg.First], fc.Revs[reg.Last])
				fmt.Fprintf(w, "    %s failure probability (%d of %d commits)\n", pct(reg.FailureProbability), reg.Failures, reg.Last-reg.First+1)
			}
		}
	} else {
		fmt.Fprintf(w, "No known past failures\n")
	}
}
