// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package table

import (
	"fmt"
	"testing"
)

var stateTemp = TableFromStrings(
	[]string{"state", "high", "low"},
	[][]string{
		{"Alabama", "122", "-27"},
		{"Alaska", "100", "-80"},
	}, true)

func ExampleUnpivot() {
	fmt.Println("Original table")
	Print(stateTemp)
	fmt.Println()
	fmt.Println("Unpivoted table")
	Print(Unpivot(stateTemp, "kind", "temperature", "high", "low"))
	// Output:
	//
	// Original table
	// state    high  low
	// Alabama   122  -27
	// Alaska    100  -80
	//
	// Unpivoted table
	// state    kind  temperature
	// Alabama  high          122
	// Alabama  low           -27
	// Alaska   high          100
	// Alaska   low           -80
}

var stateTempByKind = Unpivot(stateTemp, "kind", "temperature", "high", "low")

func ExamplePivot() {
	fmt.Println("Original table")
	Print(stateTempByKind)
	fmt.Println()
	fmt.Println("Pivoted table")
	Print(Pivot(stateTempByKind, "kind", "temperature"))
	// Output:
	//
	// Original table
	// state    kind  temperature
	// Alabama  high          122
	// Alabama  low           -27
	// Alaska   high          100
	// Alaska   low           -80
	//
	// Pivoted table
	// state    high  low
	// Alabama   122  -27
	// Alaska    100  -80
}

func TestUnpivot(t *testing.T) {
	tab := new(Builder).Add("x", []int{}).Add("y", []float64{}).Done()
	shouldPanic(t, "at least 1 column", func() {
		Unpivot(tab, "a", "b")
	})
	shouldPanic(t, "different types", func() {
		Unpivot(tab, "a", "b", "x", "y")
	})
}
