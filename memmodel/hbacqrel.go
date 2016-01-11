// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// HBAcqRel is an HBGenerator that implements acquire/release.
type HBAcqRel struct{}

func (HBAcqRel) HappensBefore(p *Prog, i, j PC) HBType {
	op1, op2 := p.OpAt(i), p.OpAt(j)
	//sameThread := i.TID == j.TID

	// TODO: Is this right?

	switch {
	case op1.Type == OpStore && op2.Type == OpStore && op1.Var == op2.Var:
		// Stores to the same location are totally ordered.
		return HBHappensBefore

	// case sameThread && op1.Type == OpSyncLoad && (op2.Type == OpSyncLoad || op2.Type == OpRegLoad):
	// 	// Loads are not allowed to move above a sync load.
	// 	return HBHappensBefore

	// case sameThread && (op1.Type == OpSyncStore || op1.Type == OpRegStore) && op2.Type == OpSyncStore:
	// 	// Stores are not allowed to move below a sync store.
	// 	return HBHappensBefore

	case op1.Type == OpStore && op2.Type == OpLoad && op1.Var == op2.Var:
		// If the load observes the store, then the store
		// happened before the load.
		return HBConditional
	}

	return HBConcurrent
}

func (HBAcqRel) String() string {
	return "AcqRel"
}
