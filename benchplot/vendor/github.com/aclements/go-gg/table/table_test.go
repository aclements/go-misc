// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package table

import (
	"fmt"
	"reflect"
	"regexp"
	"testing"
)

var xgid = RootGroupID.Extend("xgid")
var ygid = RootGroupID.Extend("ygid")

func isEmpty(g Grouping) bool {
	if t, _ := g.(*Table); t != nil && t.Len() != 0 {
		return false
	}
	return g.Columns() == nil && len(g.Tables()) == 0
}

func de(x, y interface{}) bool {
	return reflect.DeepEqual(x, y)
}

func equal(g1, g2 Grouping) bool {
	if !de(g1.Columns(), g2.Columns()) ||
		!de(g1.Tables(), g2.Tables()) {
		return false
	}
	for _, gid := range g1.Tables() {
		for _, col := range g1.Columns() {
			if !de(g1.Table(gid).Column(col), g2.Table(gid).Column(col)) {
				return false
			}
		}
	}
	return true
}

func shouldPanic(t *testing.T, re string, f func()) {
	r := regexp.MustCompile(re)
	defer func() {
		err := recover()
		if err == nil {
			t.Fatalf("want panic matching %q; got no panic", re)
		} else if !r.MatchString(fmt.Sprintf("%s", err)) {
			t.Fatalf("panic %q does not match %q", err, re)
		}
	}()
	f()
}

func TestEmptyTable(t *testing.T) {
	tab := new(Table)
	if !isEmpty(tab) {
		t.Fatalf("Table{} is not empty")
	}
	if v := tab.Len(); v != 0 {
		t.Fatalf("Table{}.Len() should be 0; got %v", v)
	}
	if v := tab.Columns(); v != nil {
		t.Fatalf("Table{}.Columns() should be nil; got %v", v)
	}
	if v := tab.Column("x"); v != nil {
		t.Fatalf("Table{}.Column(\"x\") should be nil; got %v", v)
	}
	shouldPanic(t, "unknown column", func() {
		tab.MustColumn("x")
	})
	if v, w := tab.Tables(), []GroupID{}; !de(v, w) {
		t.Fatalf("Table{}.Tables should be %v; got %v", w, v)
	}
	if v := tab.Table(RootGroupID); v != nil {
		t.Fatalf("Table{}.Table(RootGroupID) should be nil; got %v", v)
	}
	if v := tab.Table(xgid); v != nil {
		t.Fatalf("Table{}.Table(xgid) should be nil; got %v", v)
	}
}

func TestBuilder(t *testing.T) {
	nb := NewBuilder

	var b Builder
	if !isEmpty(b.Done()) {
		t.Fatal("Empty builder is not empty")
	}
	if !isEmpty(nb(nil).Done()) {
		t.Fatal("Empty builder is not empty")
	}
	nb(nil).Add("x", []int{}).Done()
	nb(nil).Add("x", []int{1, 2, 3}).Done()
	shouldPanic(t, "not a slice", func() {
		nb(nil).Add("x", 1)
	})

	tab0 := new(Builder).Add("x", []int{}).Done()
	nb(tab0).Add("x", []int{1}) // Can override only column.
	shouldPanic(t, "column \"y\".* with 1 .* 0 rows", func() {
		nb(tab0).Add("y", []int{1})
	})
	nb(tab0).Add("y", []int{})
	if v := nb(tab0).Add("x", nil).Done(); !isEmpty(v) {
		t.Fatalf("tab.Add(\"x\", nil) should be the empty table; got %v", v)
	}
	if v := nb(tab0).Add("y", nil).Done(); !equal(v, tab0) {
		t.Fatalf("tab.Add(\"y\", nil) should be %v; got %v", tab0, v)
	}
}

func TestTable0(t *testing.T) {
	col := []int{}
	tab := new(Builder).Add("x", col).Done()
	if isEmpty(tab) {
		t.Fatalf("tab is empty")
	}
	if v := tab.Len(); v != 0 {
		t.Fatalf("tab.Len() should be 0; got %v", v)
	}
	if v, w := tab.Columns(), []string{"x"}; !de(v, w) {
		t.Fatalf("tab.Columns() should be %v; got %v", w, v)
	}
	if v := tab.Column("x"); !de(v, col) {
		t.Fatalf("tab.Column(\"x\") should be %v; got %v", col, v)
	}
	if v := tab.Column("y"); v != nil {
		t.Fatalf("tab.Column(\"y\") should be nil; got %v", v)
	}
	if v := tab.MustColumn("x"); !de(v, col) {
		t.Fatalf("tab.MustColumn(\"x\") should be %v; got %v", col, v)
	}
	shouldPanic(t, "unknown column", func() {
		tab.MustColumn("y")
	})
	if v, w := tab.Tables(), []GroupID{RootGroupID}; !de(v, w) {
		t.Fatalf("tab.Tables() should be %v; got %v", w, v)
	}
	if v := tab.Table(RootGroupID); v != tab {
		t.Fatalf("tab.Table(RootGroupID) should be %v; got %v", tab, v)
	}
	if v := tab.Table(xgid); v != nil {
		t.Fatalf("tab.Table(xgid) should be nil; got %v", v)
	}
}

func TestTable1(t *testing.T) {
	col := []int{1}
	tab := new(Builder).Add("x", col).Done()
	if isEmpty(tab) {
		t.Fatalf("tab is empty")
	}
	if v := tab.Len(); v != 1 {
		t.Fatalf("tab.Len() should be 1; got %v", v)
	}
	if v, w := tab.Columns(), []string{"x"}; !de(v, w) {
		t.Fatalf("tab.Columns() should be %v; got %v", w, v)
	}
	if v := tab.Column("x"); !de(v, col) {
		t.Fatalf("tab.Column(\"x\") should be %v; got %v", col, v)
	}
	if v := tab.Column("y"); v != nil {
		t.Fatalf("tab.Column(\"y\") should be nil; got %v", v)
	}
	if v := tab.MustColumn("x"); !de(v, col) {
		t.Fatalf("tab.MustColumn(\"x\") should be %v; got %v", col, v)
	}
	shouldPanic(t, "unknown column", func() {
		tab.MustColumn("y")
	})
	if v, w := tab.Tables(), []GroupID{RootGroupID}; !de(v, w) {
		t.Fatalf("tab.Tables() should be %v; got %v", w, v)
	}
	if v := tab.Table(RootGroupID); v != tab {
		t.Fatalf("tab.Table(RootGroupID) should be %v; got %v", tab, v)
	}
	if v := tab.Table(xgid); v != nil {
		t.Fatalf("tab.Table(xgid) should be nil; got %v", v)
	}
}

func TestGroupingBuilder(t *testing.T) {
	etab := new(Table)
	tab0 := new(Builder).Add("x", []int{}).Done()
	tab1 := new(Builder).Add("x", []int{1}).Done()
	tabY := new(Builder).Add("y", []int{}).Done()
	tabXY := new(Builder).Add("x", []int{}).Add("y", []int{}).Done()

	ngb := NewGroupingBuilder
	if v := ngb(etab).Add(RootGroupID, etab).Done(); !isEmpty(v) {
		t.Fatalf("etab+etab should be empty; got %v", v)
	}
	if v := ngb(etab).Add(RootGroupID, tab1).Done(); !equal(tab1, v) {
		t.Fatalf("etab+(RootGroupID, tab1) should be %v; got %", tab1, v)
	}
	if v := ngb(tab1).Add(RootGroupID, etab).Done(); !equal(tab1, v) {
		t.Fatalf("(RootGroupID, tab1)+etab should be %v; got %", tab1, v)
	}

	if v := ngb(tab0).Add(RootGroupID, tab0).Done(); !equal(tab0, v) {
		t.Fatalf("tab0+(RootGroupID, tab0) should be %v; got %v", tab0, v)
	}
	if v := ngb(tab0).Add(RootGroupID, tab1).Done(); !equal(tab1, v) {
		t.Fatalf("tab0+(RootGroupID, tab1) should be %v; got %v", tab0, v)
	}
	if v := ngb(tab0).Add(RootGroupID, tabY).Done(); !equal(tabY, v) {
		t.Fatalf("tab0+(RootGroupID, tabY) should be %v; got %v", tab0, v)
	}
	shouldPanic(t, `table columns \["y"\] do not match group columns \["x"\]`, func() {
		ngb(tab0).Add(xgid, tabY)
	})
	shouldPanic(t, `table columns \["x" "y"\] do not match group columns \["x"\]`, func() {
		ngb(tab0).Add(xgid, tabXY)
	})

	tab01 := ngb(tab0).Add(xgid, tab1).Done()
	if v, w := tab01.Columns(), []string{"x"}; !de(v, w) {
		t.Fatalf("tab01.Columns() should be %v; got %v", w, v)
	}
	if v, w := tab01.Tables(), []GroupID{RootGroupID, xgid}; !de(v, w) {
		t.Fatalf("tab01.Tables() should be %v; got %v", w, v)
	}
	if v := tab01.Table(RootGroupID); v != tab0 {
		t.Fatalf("tab01.Table(RootGroupID) should be tab0; got %v", v)
	}
	if v := tab01.Table(xgid); v != tab1 {
		t.Fatalf("tab01.Table(xgid) should be tab1; got %v", v)
	}
	if v := tab01.Table(RootGroupID.Extend("ygid")); v != nil {
		t.Fatalf("tab01.Table(ygid) should be nil; got %v", v)
	}
	if v := ngb(tab01).Add(RootGroupID, new(Table)).Done(); !equal(tab01, v) {
		t.Fatalf("tab01+(RootGroupID, empty) should be tab01; got %v", v)
	}

	if v := ngb(tab0).Add(RootGroupID, nil).Done(); !isEmpty(v) {
		t.Fatalf("tab0+(RootGroupID, nil) should be empty; got %v", v)
	}
	if v := ngb(tab0).Add(xgid, nil).Done(); !equal(tab0, v) {
		t.Fatalf("tab0+(xgid, nil) should be tab0; got %v", v)
	}

	tab0x := ngb(tab01).Add(xgid, nil).Done()
	if !equal(tab0x, tab0) {
		t.Fatalf("tab01+(xgid, nil) should be tab0; got %v", tab0x)
	}
	if v := ngb(tab0x).Add(RootGroupID, nil).Done(); !isEmpty(v) {
		t.Fatalf("tab0x+(RootGroupID, nil) should be empty; got %v", v)
	}

	tab2 := ngb(nil).Add(xgid, tab0).Add(ygid, tab1).Done()
	if want := []GroupID{xgid, ygid}; !de(want, tab2.Tables()) {
		t.Fatalf("tables should be %v; got %v", want, tab2.Tables())
	}

	shouldPanic(t, `int and float64 for column "x"`, func() {
		ngb(tab0).Add(xgid, new(Builder).Add("x", []float64{}).Done())
	})
}

func TestColumnOrder(t *testing.T) {
	// Test that columns stay in order.
	cols := []string{"a", "b", "c", "d"}
	for iter := 0; iter < 10; iter++ {
		var b Builder
		for _, col := range cols {
			b.Add(col, []int{})
		}
		tab := b.Done()
		if !de(cols, tab.Columns()) {
			t.Fatalf("want %v; got %v", cols, tab.Columns())
		}
	}

	// Test that re-adding a column keeps it in place.
	tab := new(Builder).Add("a", []int{}).Add("b", []int{}).Add("a", []int{}).Done()
	if want := []string{"a", "b"}; !de(want, tab.Columns()) {
		t.Fatalf("want %v; got %v", want, tab.Columns())
	}
}

func TestGroupOrder(t *testing.T) {
	// Test that groups stay in order.
	gids := []GroupID{
		RootGroupID.Extend("a"),
		RootGroupID.Extend("b"),
		RootGroupID.Extend("c"),
		RootGroupID.Extend("d"),
	}
	tab := new(Builder).Add("col", []int{}).Done()
	for iter := 0; iter < 10; iter++ {
		var b GroupingBuilder
		for _, gid := range gids {
			b.Add(gid, tab)
		}
		g := b.Done()
		if !de(gids, g.Tables()) {
			t.Fatalf("want %v; got %v", gids, g.Tables())
		}
	}

	// Test that re-adding a group keeps it in place.
	var b GroupingBuilder
	g := b.Add(gids[0], tab).Add(gids[1], tab).Add(gids[0], tab).Done()
	if want := []GroupID{gids[0], gids[1]}; !de(want, g.Tables()) {
		t.Fatalf("want %v; got %v", want, g.Tables())
	}
}
