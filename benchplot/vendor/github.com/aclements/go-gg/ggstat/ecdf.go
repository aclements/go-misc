// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ggstat

import (
	"github.com/aclements/go-gg/generic/slice"
	"github.com/aclements/go-gg/table"
	"github.com/aclements/go-moremath/vec"
)

// ECDF constructs an empirical CDF from a set of samples.
//
// X is the only required field. All other fields have reasonable
// default zero values.
//
// The result of ECDF has three columns in addition to constant
// columns from the input. The names of the columns depend on whether
// Label is "".
//
// - Column X is the points at which the CDF changes (a subset of the
// samples).
//
// - Column "cumulative density" or "cumulative density of <label>" is
// the cumulative density estimate.
//
// - Column "cumulative count" (if W and Label are ""), "cumulative
// weight" (if W is not "", but Label is "") or "cumulative <label>"
// (if Label is not "") is the cumulative count or weight of samples.
// That is, cumulative density times the total weight of the samples.
type ECDF struct {
	// X is the name of the column to use for samples.
	X string

	// W is the optional name of the column to use for sample
	// weights. It may be "" to uniformly weight samples.
	W string

	// Label, if not "", gives a label for the samples. It is used
	// to construct more specific names for the output columns. It
	// should be a plural noun.
	Label string

	// Widen adjusts the domain of the returned ECDF. If Widen is
	// not 1.0, ECDF will add a point below the smallest sample
	// and above the largest sample to make the 0 and 1 levels
	// clear. If Widen is 0, it is treated as 1.1 (that is, widen
	// the domain by 10%, or 5% on the left and 5% on the right).
	//
	// TODO: Have a way to specify a specific range?
	Widen float64

	// SplitGroups indicates that each group in the table should
	// have separate bounds based on the data in that group alone.
	// The default, false, indicates that the bounds should be
	// based on all of the data in the table combined. This makes
	// it possible to stack ECDFs and easier to compare them
	// across groups.
	SplitGroups bool
}

func (s ECDF) F(g table.Grouping) table.Grouping {
	// Construct output column names.
	dname, cname := "cumulative density", "cumulative count"
	if s.Label != "" {
		dname += " of " + s.Label
		cname = "cumulative " + s.Label
	} else if s.W != "" {
		cname = "cumulative weight"
	}

	g = table.SortBy(g, s.X)
	if s.Widen <= 1.0 && s.Widen != 0 {
		// Disallow narrowing, since this isn't a continuous
		// function.
		s.Widen = 1.0
	}
	col := getCol(g, s.X, s.Widen, s.SplitGroups)
	return table.MapTables(g, func(gid table.GroupID, t *table.Table) *table.Table {
		// Get input columns.
		var ws []float64
		xs := col[gid].data
		if s.W != "" {
			slice.Convert(&ws, t.MustColumn(s.W))
		}

		// Ignore empty tables.
		if len(xs) == 0 {
			return new(table.Builder).Add(s.X, []float64{}).Add(cname, []float64{}).Add(dname, []float64{}).Done()
		}

		// Create output columns.
		xo, do, co := make([]float64, 0), make([]float64, 0), make([]float64, 0)
		if s.Widen != 1.0 {
			// Extend to the left.
			xo = append(xo, col[gid].min)
			do = append(do, 0)
			co = append(co, 0)
		}

		// Compute total weight.
		var total float64
		if ws == nil {
			total = float64(t.Len())
		} else {
			total = vec.Sum(ws)
		}

		// Create ECDF.
		cum := 0.0
		for i := 0; i < len(xs); {
			j := i
			for j < len(xs) && xs[i] == xs[j] {
				if ws == nil {
					cum += 1
				} else {
					cum += ws[j]
				}
				j++
			}

			xo = append(xo, xs[i])
			do = append(do, cum/total)
			co = append(co, cum)

			i = j
		}

		if s.Widen != 1.0 {
			// Extend to the right.
			xo = append(xo, col[gid].max)
			do = append(do, 1)
			co = append(co, cum)
		}

		// Construct results table.
		nt := new(table.Builder).Add(s.X, xo).Add(dname, do).Add(cname, co)
		preserveConsts(nt, t)
		return nt.Done()
	})
}
