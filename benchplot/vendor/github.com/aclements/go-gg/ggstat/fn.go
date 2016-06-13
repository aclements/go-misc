// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ggstat

import (
	"math"

	"github.com/aclements/go-gg/generic/slice"
	"github.com/aclements/go-gg/table"
	"github.com/aclements/go-moremath/stats"
)

type colInfo struct {
	data     []float64
	min, max float64
}

// getCol extracts column x from each group, converts it to []float64,
// and finds its bounds.
//
// TODO: Maybe this should be a callback interface to avoid building
// the map and holding on to so much allocation?
func getCol(g table.Grouping, x string, widen float64, splitGroups bool) map[table.GroupID]colInfo {
	if widen <= 0 {
		widen = 1.1
	}

	col := make(map[table.GroupID]colInfo)

	if !splitGroups {
		// Compute combined bounds.
		min, max := math.NaN(), math.NaN()
		for _, gid := range g.Tables() {
			var xs []float64
			t := g.Table(gid)
			slice.Convert(&xs, t.MustColumn(x))
			xmin, xmax := stats.Bounds(xs)
			if xmin < min || math.IsNaN(min) {
				min = xmin
			}
			if xmax > max || math.IsNaN(max) {
				max = xmax
			}
			col[gid] = colInfo{xs, 0, 0}
		}

		// Widen bounds.
		span := max - min
		min, max = min-span*(widen-1)/2, max+span*(widen-1)/2

		for gid, info := range col {
			info.min, info.max = min, max
			col[gid] = info
		}

		return col
	}

	// Find bounds for each group separately.
	for _, gid := range g.Tables() {
		t := g.Table(gid)

		// Compute bounds.
		var xs []float64
		slice.Convert(&xs, t.MustColumn(x))
		min, max := stats.Bounds(xs)

		// Widen bounds.
		span := max - min
		min, max = min-span*(widen-1)/2, max+span*(widen-1)/2

		col[gid] = colInfo{xs, min, max}
	}
	return col
}
