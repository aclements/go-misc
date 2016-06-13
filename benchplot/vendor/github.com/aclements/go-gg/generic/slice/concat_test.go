// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slice

import "testing"

func TestConcat(t *testing.T) {
	if g := Concat(); g != nil {
		t.Errorf("Concat() should be nil; got %v", g)
	}

	if g, w := Concat([]int{}), []int{}; !de(w, g) {
		t.Errorf("want %v; got %v", w, g)
	}

	if g, w := Concat([]int(nil)), []int{}; !de(w, g) {
		t.Errorf("want %v; got %v", w, g)
	}

	if g, w := Concat([]int{1, 2}, []int{3, 4}), []int{1, 2, 3, 4}; !de(w, g) {
		t.Errorf("want %v; got %v", w, g)
	}

	shouldPanic(t, "have different types", func() {
		Concat([]int{}, []string{})
	})
}
