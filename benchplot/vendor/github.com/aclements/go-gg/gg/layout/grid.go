// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package layout

import "sort"

// Grid lays out elements in a two dimensional table. Each child is
// assigned to a cell in the table and may optionally span multiple
// rows and/or columns.
type Grid struct {
	elts       []*gridElement
	cols, rows int
	x, y, w, h float64
}

type gridElement struct {
	e                      Element
	x, y, colSpan, rowSpan int
}

// Add adds Element e to Grid g, spanning cells (x,y) up to but not
// including (x+colSpan, y+colSpan).
func (g *Grid) Add(e Element, x, y, colSpan, rowSpan int) {
	if x+colSpan > g.cols {
		g.cols = x + colSpan
	}
	if y+rowSpan > g.rows {
		g.rows = y + rowSpan
	}
	g.elts = append(g.elts, &gridElement{e, x, y, colSpan, rowSpan})
}

func (g *Grid) Children() []Element {
	res := make([]Element, len(g.elts))
	for i, elt := range g.elts {
		res[i] = elt.e
	}
	return res
}

func (g *Grid) doLayout(byRow bool, allocated float64) (dims []float64, flexes []bool) {
	seq := func(n int) []int {
		res := make([]int, n)
		for i := range res {
			res[i] = i
		}
		return res
	}
	max := func(x, y float64) float64 {
		if x > y {
			return x
		}
		return y
	}

	if byRow {
		dims = make([]float64, g.rows)
		flexes = make([]bool, g.rows)
	} else {
		dims = make([]float64, g.cols)
		flexes = make([]bool, g.cols)
	}
	for i := range flexes {
		// TODO: Should empty columns be set to false?
		flexes[i] = true
	}

	// Sort elements by colSpan or rowSpan.
	eltOrder := seq(len(g.elts))
	sort.Sort(&gridElementSorter{g.elts, eltOrder, byRow})

	// Add a fake element that spans everything and uses the
	// allocated space.
	if allocated > 0 {
		eltOrder = append(eltOrder, -1)
	}

	// Process elements by increasing span.
	for _, i := range eltOrder {
		var (
			edim  float64
			eflex bool
			epos  int
			espan int
		)
		if i == -1 {
			// Fake element for final space allocation.
			edim, eflex, epos, espan = allocated, true, 0, len(dims)
		} else {
			e := g.elts[i]
			// TODO: We need to make one pass and get both size
			// hints or this will be exponential.
			if byRow {
				_, edim, _, eflex = e.e.SizeHint()
				epos, espan = e.y, e.rowSpan
			} else {
				edim, _, eflex, _ = e.e.SizeHint()
				epos, espan = e.x, e.colSpan
			}
		}

		if espan == 1 {
			dims[epos] = max(dims[epos], edim)
			if !eflex {
				flexes[epos] = false
			}
		} else if espan > 1 {
			total := edim

			// Expand flexible columns so that the total
			// dim is >= e's dim, and so the rows/columns
			// we do expand get equal dims. We don't
			// shrink any row/column. If all rows/columns
			// are fixed, we treat them all as flexible.
			var subdims []float64
			forceFlex := false
			for i := epos; i < epos+espan; i++ {
				if flexes[i] {
					subdims = append(subdims, dims[i])
				} else {
					// This space is accounted for.
					total -= dims[i]
				}
			}
			if len(subdims) == 0 {
				// All rows/columns are fixed, so treat
				// them all as flexible.
				forceFlex = true
				subdims = append(subdims, dims[epos:epos+espan]...)
				total = edim
			}

			if total <= 0 {
				// Fixed columns already take e's space.
				continue
			}

			// Remove flex columns already wider than
			// total/count from consideration.
			count := len(subdims)
			sort.Sort(sort.Reverse(sort.Float64Slice(subdims)))
			for _, dim := range dims {
				if dim > total/float64(count) {
					total -= dim
					count--
				}
			}

			// Expand remaining rows/columns to total/count.
			if count <= 0 {
				// Flex columns already take e's space.
				continue
			}

			dim := total / float64(count)
			for i := epos; i < epos+espan; i++ {
				if flexes[i] || forceFlex {
					dims[i] = max(dims[i], dim)
				}
			}

			// TODO: What do I do with e's flex? Clearly
			// if a fixed element spans the whole grid,
			// the grid should be fixed, so I shouldn't
			// ignore it.
		}
	}
	return
}

func (g *Grid) SizeHint() (w, h float64, flexw, flexh bool) {
	sum := func(xs []float64) float64 {
		s := 0.0
		for _, x := range xs {
			s += x
		}
		return s
	}
	any := func(xs []bool) bool {
		for _, x := range xs {
			if x {
				return true
			}
		}
		return false
	}

	xdims, xflexes := g.doLayout(false, 0)
	ydims, yflexes := g.doLayout(true, 0)
	return sum(xdims), sum(ydims), any(xflexes), any(yflexes)
}

func (g *Grid) SetLayout(x, y, w, h float64) {
	// Record layout.
	g.x, g.y, g.w, g.h = x, y, w, h

	// Layout children.
	csum := func(xs []float64) []float64 {
		res, csum := make([]float64, len(xs)+1), 0.0
		for i, x := range xs {
			res[i+1] = csum + x
			csum += x
		}
		return res
	}
	xdims, _ := g.doLayout(false, w)
	ydims, _ := g.doLayout(true, h)
	xpos := csum(xdims)
	ypos := csum(ydims)
	for _, elt := range g.elts {
		elt.e.SetLayout(xpos[elt.x], ypos[elt.y], xpos[elt.x+elt.colSpan]-xpos[elt.x], ypos[elt.y+elt.rowSpan]-ypos[elt.y])
	}
}

func (g *Grid) Layout() (x, y, w, h float64) {
	return g.x, g.y, g.w, g.h
}

type gridElementSorter struct {
	elts      []*gridElement
	seq       []int
	byRowSpan bool
}

func (g *gridElementSorter) Len() int {
	return len(g.seq)
}

func (g *gridElementSorter) Less(i, j int) bool {
	e1, e2 := g.elts[g.seq[i]], g.elts[g.seq[j]]
	if g.byRowSpan {
		return e1.rowSpan < e2.rowSpan
	}
	return e1.colSpan < e2.colSpan
}

func (g *gridElementSorter) Swap(i, j int) {
	g.seq[i], g.seq[j] = g.seq[j], g.seq[i]
}
