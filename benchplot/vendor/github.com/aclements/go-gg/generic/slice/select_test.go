// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slice

import (
	"reflect"
	"testing"
)

func TestSelect(t *testing.T) {
	x1 := []int{1, 2, 3}
	got := Select(x1, []int{2, 1, 0})
	if want := []int{3, 2, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	got = Select(x1, []int{1, 1, 1, 1})
	if want := []int{2, 2, 2, 2}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}

	type T struct{ x int }
	x2 := []T{{1}, {2}, {3}}
	got = Select(x2, []int{2, 1, 0})
	if want := []T{{3}, {2}, {1}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestSelectType(t *testing.T) {
	type T []float64
	x1 := T{1, 2, 3}
	y1 := Select(x1, []int{})
	if _, ok := y1.(T); !ok {
		t.Fatalf("result has wrong type; expected T, got %T", y1)
	}

	type U int
	x2 := []U{1, 2, 3}
	y2 := Select(x2, []int{})
	if _, ok := y2.([]U); !ok {
		t.Fatalf("result has wrong type; expected []U, got %T", y2)
	}
}
