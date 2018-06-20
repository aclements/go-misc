// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"reflect"
	"testing"
)

func TestIDom(t *testing.T) {
	idom := IDom(graphMuchnick, 0)
	want := []int{0: -1, 1: 0, 2: 1, 3: 2, 4: 2, 5: 4, 6: 4, 7: 4}
	if !reflect.DeepEqual(want, idom) {
		t.Errorf("graphMuchnick: want %v, got %v", want, idom)
	}

	idom = IDom(graphCS252, 0)
	want = []int{0: -1, 1: 0, 2: 1, 3: 2, 4: 2, 5: 1, 6: 2, 7: 1, 8: 7}
	if !reflect.DeepEqual(want, idom) {
		t.Errorf("graphCS252: want %v, got %v", want, idom)
	}
}

func TestDomFrontier(t *testing.T) {
	df := DomFrontier(graphCS252, 0, nil)
	want := [][]int{
		0: {},
		1: {1},
		2: {7},
		3: {6},
		4: {6},
		5: {1, 7},
		6: {7},
		7: {},
		8: {},
	}
	if !reflect.DeepEqual(want, df) {
		t.Errorf("want %v, got %v", want, df)
	}
}
