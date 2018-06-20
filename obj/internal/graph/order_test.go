// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package graph

import (
	"reflect"
	"testing"
)

func TestPreOrder(t *testing.T) {
	po := PreOrder(graphMuchnick, 0)
	want := []int{0, 1, 2, 3, 4, 5, 7, 6}
	if !reflect.DeepEqual(want, po) {
		t.Errorf("want %v, got %v", want, po)
	}
}

func TestPostOrder(t *testing.T) {
	po := PostOrder(graphMuchnick, 0)
	want := []int{3, 7, 5, 6, 4, 2, 1, 0}
	if !reflect.DeepEqual(want, po) {
		t.Errorf("want %v, got %v", want, po)
	}
}
