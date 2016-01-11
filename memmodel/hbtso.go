// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// HBTSO is an HBGenerator that implements TSO.
type HBTSO struct{}

func (HBTSO) HappensBefore(p *Prog, i, j PC) HBType {
	op1, op2 := p.OpAt(i), p.OpAt(j)
	sameThread := i.TID == j.TID

	switch {
	case op1.Type == OpStore && op2.Type == OpStore:
		// Stores are totally ordered.
		return HBHappensBefore

	case sameThread && op1.Type == OpLoad && op2.Type == OpLoad:
		// Loads are program ordered.
		return HBHappensBefore

	case sameThread && op1.Type == OpLoad && op2.Type == OpStore:
		// Loads are program ordered before stores. (But *not*
		// the other way around.)
		return HBHappensBefore

	case op1.Type == OpStore && op2.Type == OpLoad && op1.Var == op2.Var:
		// If the load observes the store, then the store
		// happened before the load.
		return HBConditional
	}

	return HBConcurrent
}

func (HBTSO) String() string {
	return "TSO"
}
