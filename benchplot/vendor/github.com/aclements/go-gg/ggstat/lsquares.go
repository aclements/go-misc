// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ggstat

import (
	"github.com/aclements/go-gg/generic/slice"
	"github.com/aclements/go-gg/table"
	"github.com/aclements/go-moremath/fit"
	"github.com/aclements/go-moremath/vec"
)

// TODO: Should this keep the type of X and Y the same if they aren't
// just []float64?

// LeastSquares constructs a least squares polynomial regression for
// the data (X, Y).
//
// X and Y are required. All other fields have reasonable default zero
// values.
//
// The result of LeastSquares has two columns in addition to constant
// columns from the input:
//
// - Column X is the points at which the fit function is sampled.
//
// - Column Y is the result of the fit function.
//
// TODO: Confidence internals/bootstrap distributions?
type LeastSquares struct {
	// X and Y are the names of the columns to use for X and Y
	// values of data points, respectively.
	X, Y string

	// N is the number of points to sample the regression at. If N
	// is 0, a reasonable default is used.
	N int

	// Widen sets the domain of the returned LOESS sample points
	// to Widen times the span of the data. If Widen is 0, it is
	// treated as 1.1 (that is, widen the domain by 10%, or 5% on
	// the left and 5% on the right).
	//
	// TODO: Have a way to specify a specific range?
	Widen float64

	// SplitGroups indicates that each group in the table should
	// have separate bounds based on the data in that group alone.
	// The default, false, indicates that the bounds should be
	// based on all of the data in the table combined. This makes
	// it possible to stack LOESS fits and easier to compare them
	// across groups.
	SplitGroups bool

	// Degree specifies the degree of the fit polynomial. If it is
	// 0, it is treated as 1.
	Degree int
}

func (s LeastSquares) F(g table.Grouping) table.Grouping {
	if s.Degree <= 0 {
		s.Degree = 1
	}

	evals := evalPoints(g, s.X, s.N, s.Widen, s.SplitGroups)

	var xs, ys []float64
	return table.MapTables(g, func(gid table.GroupID, t *table.Table) *table.Table {
		if t.Len() == 0 {
			nt := new(table.Builder).Add(s.X, []float64{}).Add(s.Y, []float64{})
			preserveConsts(nt, t)
			return nt.Done()
		}

		// TODO: We potentially convert each X column twice,
		// since evalPoints also has to convert them.
		slice.Convert(&xs, t.MustColumn(s.X))
		slice.Convert(&ys, t.MustColumn(s.Y))
		eval := evals[gid]

		r := fit.PolynomialRegression(xs, ys, nil, s.Degree)
		nt := new(table.Builder).Add(s.X, eval).Add(s.Y, vec.Map(r.F, eval))
		preserveConsts(nt, t)
		return nt.Done()
	})
}
