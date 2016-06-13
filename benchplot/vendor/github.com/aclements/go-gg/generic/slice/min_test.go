// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slice

import (
	"math"
	"testing"
	"time"
)

func TestMin(t *testing.T) {
	shouldPanic(t, "no min", func() { Min([]float64{}) })
	shouldPanic(t, "no min", func() { ArgMin([]float64{}) })
	shouldPanic(t, "no max", func() { Max([]float64{}) })
	shouldPanic(t, "no max", func() { ArgMax([]float64{}) })

	xs := []float64{5, 1, 8, 1, 8, 3}
	if x := Min(xs); x != 1.0 {
		t.Errorf("Min should be 1, got %v", x)
	}
	if x := ArgMin(xs); x != 1 {
		t.Errorf("ArgMin should be 1, got %v", x)
	}
	if x := Max(xs); x != 8.0 {
		t.Errorf("Max should be 8, got %v", x)
	}
	if x := ArgMax(xs); x != 2 {
		t.Errorf("ArgMax should be 2, got %v", x)
	}

	xs = []float64{1, 5, math.NaN()}
	if x := Min(xs); x != 1.0 {
		t.Errorf("Min should be 1, got %v", x)
	}
	if x := Max(xs); x != 5.0 {
		t.Errorf("Max should be 5, got %v", x)
	}
}

type fakeSortInterface struct {
	len int
}

func (f fakeSortInterface) Len() int {
	return f.len
}

func (f fakeSortInterface) Swap(i, j int) {
	panic("can't")
}

func (f fakeSortInterface) Less(i, j int) bool {
	return i < j
}

type timeSlice []time.Time

func (s timeSlice) Len() int {
	return len(s)
}

func (s timeSlice) Less(i, j int) bool {
	return s[i].Before(s[j])
}

func (s timeSlice) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func TestMinSort(t *testing.T) {
	shouldPanic(t, "no min", func() { Min(fakeSortInterface{0}) })
	shouldPanic(t, "no min", func() { ArgMin(fakeSortInterface{0}) })
	shouldPanic(t, "no max", func() { Max(fakeSortInterface{0}) })
	shouldPanic(t, "no max", func() { ArgMax(fakeSortInterface{0}) })

	f := fakeSortInterface{5}
	if x := ArgMin(f); x != 0 {
		t.Errorf("ArgMin should be 0, got %v", x)
	}
	if x := ArgMax(f); x != 4 {
		t.Errorf("ArgMax should be 4, got %v", x)
	}

	z := time.Unix(0, 0)
	ts := timeSlice{z.Add(time.Hour), z, z.Add(2 * time.Hour), z.Add(time.Hour)}
	if x := Min(ts); x != ts[1] {
		t.Errorf("Min should be %v, got %v", ts[1], x)
	}
	if x := ArgMin(ts); x != 1 {
		t.Errorf("ArgMin should be 1, got %v", x)
	}
	if x := Max(ts); x != ts[2] {
		t.Errorf("Max should be %v, got %v", ts[2], x)
	}
	if x := ArgMax(ts); x != 2 {
		t.Errorf("ArgMax should be 2, got %v", x)
	}
}
