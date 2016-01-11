// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"fmt"
	"io"
)

// An Outcome represents the outcomes of all instructions in an
// execution. Bit i of an Outcome is set to the result of the load
// with ID i.
type Outcome uint64

func init() {
	if Outcome(1<<MaxTotalOps) == 0 {
		panic("MaxTotalOps is too large to fit in Outcome")
	}
}

func (o *Outcome) Set(op Op, result int) {
	if result != 0 {
		(*o) |= 1 << op.ID
	}
}

func (o Outcome) Format(width int) string {
	var out []byte
	for l := 0; l < width; l++ {
		out = append(out, '0'+byte((o>>uint(l))&1))
	}
	return string(out)
}

// An OutcomeSet records the set of permissible outcomes of an
// execution.
type OutcomeSet struct {
	bits     [(1<<MaxTotalOps + 63) / 64]uint64
	numLoads int
}

func (b *OutcomeSet) Reset(p *Prog) {
	b.bits = [(1<<MaxTotalOps + 63) / 64]uint64{}
	b.numLoads = p.NumLoads
}

func (b *OutcomeSet) Add(o Outcome) {
	b.bits[o/64] |= 1 << (o % 64)
}

func (b *OutcomeSet) Has(o Outcome) bool {
	return b.bits[o/64]&(1<<(o%64)) != 0
}

// Contains returns true if every outcome in s2 is contained in s.
func (s *OutcomeSet) Contains(s2 *OutcomeSet) bool {
	for i, bits := range s.bits {
		if s2.bits[i]&^bits != 0 {
			return false
		}
	}
	return true
}

// AddAll adds all outcomes in s2 to s.
func (s *OutcomeSet) AddAll(s2 *OutcomeSet) {
	if s.numLoads != s2.numLoads {
		panic("cannot union OutcomeSets with differing numbers of loads")
	}
	for i, bits := range s2.bits {
		s.bits[i] |= bits
	}
}

func (b *OutcomeSet) OutcomeIter() <-chan Outcome {
	ch := make(chan Outcome)
	go func() {
		for i, bits := range b.bits {
			if bits == 0 {
				continue
			}
			for off := uint(0); off < 64; off++ {
				if bits&(1<<off) == 0 {
					continue
				}
				outcome := Outcome(i*64 + int(off))
				ch <- outcome
			}
		}
		close(ch)
	}()
	return ch
}

func (b *OutcomeSet) String() string {
	out := []byte{}
	for l := 0; l < b.numLoads; l++ {
		out = append(out, 'a'+byte(l))
	}
	out = append(out, '\n')
	for outcome := range b.OutcomeIter() {
		out = append(out, []byte(outcome.Format(b.numLoads))...)
	}
	return string(out[:len(out)-1])
}

func printOutcomeTable(w io.Writer, cols []string, outcomes []OutcomeSet) error {
	allOutcomes := outcomes[0]
	for _, o := range outcomes {
		allOutcomes.AddAll(&o)
	}

	// Print header.
	out := []byte{}
	for l := 0; l < allOutcomes.numLoads; l++ {
		out = append(out, 'a'+byte(l))
	}
	widths := make([]int, 0, len(cols))
	for _, col := range cols {
		out = append(out, ' ', ' ')
		out = append(out, []byte(col)...)
		widths = append(widths, len(col))
	}
	out = append(out, '\n')
	if _, err := w.Write(out); err != nil {
		return err
	}

	// Print rows.
	for outcome := range allOutcomes.OutcomeIter() {
		if _, err := fmt.Fprint(w, outcome.Format(allOutcomes.numLoads)); err != nil {
			return err
		}
		var haveY, haveN bool
		for i := range cols {
			var ch rune
			if outcomes[i].Has(outcome) {
				ch = 'Y'
				haveY = true
			} else {
				ch = 'N'
				haveN = true
			}
			if _, err := fmt.Fprintf(w, "  %-*c", widths[i], ch); err != nil {
				return err
			}
		}
		if haveY && haveN {
			if _, err := fmt.Fprintf(w, " *"); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "\n"); err != nil {
			return err
		}
	}

	return nil
}
