// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package table

import (
	"bytes"
	"encoding/csv"
	"testing"
)

func ExampleTableFromStructs() {
	type prez struct {
		Name  string
		Terms int
	}
	data := []prez{{"Washington", 2}, {"Adams", 1}, {"Jefferson", 2}}
	Print(TableFromStructs(data))
	// Output:
	// Name        Terms
	// Washington      2
	// Adams           1
	// Jefferson       2
}

func TestTableFromStructs(t *testing.T) {
	// The example already tests basic functionality.
	shouldPanic(t, "not a slice", func() {
		TableFromStructs(42)
	})
	shouldPanic(t, "not a slice of struct", func() {
		TableFromStructs([]int{42})
	})
}

func TestTableFromStructsEmbedded(t *testing.T) {
	type T struct {
		A int
	}
	type U struct {
		T
	}
	data := []U{{T{1}}}
	tab := TableFromStructs(data)
	if want := []string{"A"}; !de(want, tab.Columns()) {
		t.Errorf("columns should be %v; got %v", want, tab.Columns())
	}
}

func TestTableFromStructsUnexported(t *testing.T) {
	type T struct {
		a int
		A int
	}
	data := []T{{1, 2}}
	tab := TableFromStructs(data)
	if want := []string{"A"}; !de(want, tab.Columns()) {
		t.Errorf("columns should be %v; got %v", want, tab.Columns())
	}
}

func TestTableFromStructsEmbeddedUnexported(t *testing.T) {
	type private struct {
		A int
		b int
	}
	type U struct {
		private
		C int
	}
	data := []U{{private{1, 2}, 3}}
	tab := TableFromStructs(data)
	if want := []string{"A", "C"}; !de(want, tab.Columns()) {
		t.Errorf("columns should be %v; got %v", want, tab.Columns())
	}
}

func ExampleTableFromStrings() {
	const csvData = `name,terms
Washington,2
Adams,1
Jefferson,2`
	rows, _ := csv.NewReader(bytes.NewBufferString(csvData)).ReadAll()
	Print(TableFromStrings(rows[0], rows[1:], true))
	// Output:
	// name        terms
	// Washington      2
	// Adams           1
	// Jefferson       2
}

func TestTableFromStrings(t *testing.T) {
	csvData := `a,b,c
A,1,1.0
B,2,2.0
`
	rows, _ := csv.NewReader(bytes.NewBufferString(csvData)).ReadAll()

	// No coercion.
	tab := TableFromStrings(rows[0], rows[1:], false)
	want := new(Builder).
		Add("a", []string{"A", "B"}).
		Add("b", []string{"1", "2"}).
		Add("c", []string{"1.0", "2.0"}).
		Done()
	if !equal(want, tab) {
		t.Errorf("want:\n%sgot:\n%s", groupString(want), groupString(tab))
	}

	// Coercion.
	tab = TableFromStrings(rows[0], rows[1:], true)
	want = new(Builder).
		Add("a", []string{"A", "B"}).
		Add("b", []int{1, 2}).
		Add("c", []float64{1, 2}).
		Done()
	if !equal(want, tab) {
		t.Errorf("want:\n%sgot:\n%s", groupString(want), groupString(tab))
	}

	// Coercion inhibited by last row.
	csvData += `C,x,x`
	rows, _ = csv.NewReader(bytes.NewBufferString(csvData)).ReadAll()

	tab = TableFromStrings(rows[0], rows[1:], true)
	want = new(Builder).
		Add("a", []string{"A", "B", "C"}).
		Add("b", []string{"1", "2", "x"}).
		Add("c", []string{"1.0", "2.0", "x"}).
		Done()
	if !equal(want, tab) {
		t.Errorf("want:\n%sgot:\n%s", groupString(want), groupString(tab))
	}
}
