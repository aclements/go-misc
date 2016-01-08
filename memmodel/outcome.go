// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// An Outcome represents the outcomes of all instructions in an
// execution. Bit i of an Outcome is set to the result of the load
// with ID i.
type Outcome uint64

func init() {
	if Outcome(1<<MaxTotalOps) == 0 {
		panic("MaxTotalOps is too large to fit in Outcome")
	}
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
	b.bits[o/64] |= uint64(1 << (o % 64))
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

func (b *OutcomeSet) String() string {
	out := []byte{}
	for l := 0; l < b.numLoads; l++ {
		out = append(out, 'a'+byte(l))
	}
	out = append(out, '\n')
	for i, bits := range b.bits {
		if bits == 0 {
			continue
		}
		for off := uint(0); off < 64; off++ {
			if bits&(1<<off) == 0 {
				continue
			}
			outcome := Outcome(i*64 + int(off))
			for l := uint(0); l < uint(b.numLoads); l++ {
				out = append(out, '0'+byte((outcome>>l)&1))
			}
			out = append(out, '\n')
		}
	}
	return string(out[:len(out)-1])
}
