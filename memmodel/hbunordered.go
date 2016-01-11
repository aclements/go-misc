// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// HBUnordered is an HBGenerator that implements fully relaxed memory
// order where the *only* order observed is local program order. This
// corresponds to how programming languages typically specify
// non-atomic loads and stores, which is even weaker than typical
// hardware RMO because loads aren't coherent (loads of the same
// location may be reordered).
type HBUnordered struct{}

func (HBUnordered) HappensBefore(p *Prog, i, j PC) HBType {
	return HBConcurrent
}

func (HBUnordered) String() string {
	return "Unordered"
}
