// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slice

import "testing"

func TestConvert(t *testing.T) {
	var is []int
	Convert(&is, []int{1, 2, 3})
	if w := []int{1, 2, 3}; !de(w, is) {
		t.Errorf("want %v; got %v", w, is)
	}
	Convert(&is, []float64{1, 2, 3})
	if w := []int{1, 2, 3}; !de(w, is) {
		t.Errorf("want %v; got %v", w, is)
	}

	var fs []float64
	Convert(&fs, []int{1, 2, 3})
	if w := []float64{1, 2, 3}; !de(w, fs) {
		t.Errorf("want %v; got %v", w, fs)
	}
	Convert(&fs, []float64{1, 2, 3})
	if w := []float64{1, 2, 3}; !de(w, fs) {
		t.Errorf("want %v; got %v", w, fs)
	}

	shouldPanic(t, "cannot be converted", func() {
		Convert(&is, []string{"1", "2", "3"})
	})
	shouldPanic(t, `is not a \*\[\]T`, func() {
		Convert(is, []int{1, 2, 3})
	})
	shouldPanic(t, `is not a \*\[\]T`, func() {
		x := 1
		Convert(&x, []int{1, 2, 3})
	})
	shouldPanic(t, "is not a slice", func() {
		Convert(&is, 1)
	})
}
