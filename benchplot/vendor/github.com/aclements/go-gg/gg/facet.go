// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gg

import (
	"fmt"
	"math"
	"reflect"

	"github.com/aclements/go-gg/generic"
	"github.com/aclements/go-gg/generic/slice"
	"github.com/aclements/go-gg/table"
)

// TODO: What if there are already layers? Maybe they should be
// repeated in all facets. ggplot2 apparently does this when the
// faceting variable isn't in one of the data frames.

// TODO: Subplot is getting rather complicated. If I want to make
// facets only use public APIs, perhaps gg itself should only know
// about some interface for table group labels that provides a layout
// manager and the layout logic should live with the facets.

// TODO: This is very nearly flexible enough to make pairwise plots.

// TODO: Is this flexible enough to make marginal distribution plots?

// TODO: There's logical overlap between how a facet chooses to
// position and label a subplot and a discrete-ranged scalar. Perhaps
// facets should use scalars to chose positions and labels?

// FacetCommon is the base type for plot faceting operations. Faceting
// is a grouping operation that subdivides a plot into subplots based
// on the values in data column. Faceting operations may be composed:
// if a faceting operation has already divided the plot into subplots,
// a further faceting operation will subdivide each of those subplots.
type FacetCommon struct {
	// Col names the column to facet by. Each distinct value of
	// this column will become a separate plot. If Col is
	// orderable, the facets will be in value order; otherwise,
	// they will be in index order.
	Col string

	// SplitXScales indicates that each band (column for FacetX;
	// row for FacetY) created by this faceting operation should
	// have separate X axis scales. The default, false, indicates
	// that subplots should continue to share X scales.
	//
	// SplitXScales and SplitYScales, combined with facet
	// composition, give a great deal of control over how scales
	// are shared. Suppose you want to create an X/Y facet grid by
	// first performing a FacetX and then a FacetY. Here are some
	// common ways to share or split the scales:
	//
	// * To share the same scales between all subplots, set both
	// flags to false in both facet operations.
	//
	// * To have independent scales in all subplots, set both
	// flags to true in the FacetY (and it doesn't matter what
	// they are in the FacetX).
	//
	// * To share the X scale within each column and the Y scale
	// within each row, set SplitXScales in the FacetX and
	// SplitYScales in the FacetY.
	SplitXScales bool

	// SplitYScales is the equivalent of SplitXScales for Y axis
	// scales.
	SplitYScales bool

	// Labeler is a function that constructs facet labels from
	// data values. If this is nil, the default is fmt.Sprint.
	//
	// TODO: Call this through reflect to get the argument type
	// right?
	Labeler func(interface{}) string

	// Rows and Cols specify the number of rows or columns for
	// FacetWrap. If both are zero, FacetWrap chooses reasonable
	// defaults. Otherwise, one or the other should be zero.
	Rows, Cols int

	// TODO: Wrap order and label side for FacetWrap.
}

// FacetX splits a plot into columns.
type FacetX FacetCommon

// FacetY splits a plot into rows.
type FacetY FacetCommon

// FacetWrap splits a plot into a grid of rows and columns.
type FacetWrap FacetCommon

func (f FacetX) Apply(p *Plot) {
	(*FacetCommon)(&f).apply(p, "x")
}

func (f FacetY) Apply(p *Plot) {
	(*FacetCommon)(&f).apply(p, "y")
}

func (f FacetWrap) Apply(p *Plot) {
	(*FacetCommon)(&f).apply(p, "-")
}

func (f *FacetCommon) apply(p *Plot, dir string) {
	if f.Labeler == nil {
		f.Labeler = func(x interface{}) string { return fmt.Sprint(x) }
	}

	grouped := table.GroupBy(p.Data(), f.Col)

	// TODO: What should this do if there are multiple faceting
	// operations and the results aren't a complete cross-product?
	// Using GroupBy to form the initial faceting groups will
	// leave out subplots with no data. Alternatively, I could
	// base this on the total set of values and force there to be
	// a complete cross-product.

	// TODO: If this is, say, and X faceting and different
	// existing columns have different sets of values, should I
	// only split a column on the values it has? Doing that right
	// would require grouping existing subplots in potentially
	// complex ways (for example, if I do a FacetWrap and then a
	// FacetX, grouping subplots by column alone will be wrong.)

	// Collect grouped values. If there was already grouping
	// structure, it's possible we'll have multiple groups with
	// the same value for Col.
	type valInfo struct {
		index int
		label string
	}
	var valType reflect.Type
	vals := make(map[interface{}]*valInfo)
	for i, gid := range grouped.Tables() {
		val := gid.Label()
		if _, ok := vals[val]; !ok {
			vals[val] = &valInfo{len(vals), f.Labeler(val)}
		}
		if i == 0 {
			valType = reflect.TypeOf(val)
		}
	}

	// If f.Col is orderable, order and re-index values.
	if generic.CanOrderR(valType.Kind()) {
		valSeq := reflect.MakeSlice(reflect.SliceOf(valType), 0, len(vals))
		for val := range vals {
			valSeq = reflect.Append(valSeq, reflect.ValueOf(val))
		}
		slice.Sort(valSeq.Interface())
		for i := 0; i < valSeq.Len(); i++ {
			vals[valSeq.Index(i).Interface()].index = i
		}
	}

	// Compute FacetWrap rows and cols.
	if dir == "-" {
		cells := float64(len(vals))
		if f.Cols == 0 {
			if f.Rows == 0 {
				// Chose default Rows and Cols.
				f.Rows = int(math.Ceil(math.Sqrt(cells)))
			}
			// Compute Cols from Rows.
			f.Cols = int(math.Ceil(cells / float64(f.Rows)))
		} else {
			// Compute Rows from Cols.
			f.Rows = int(math.Ceil(cells / float64(f.Cols)))
		}
	}

	// Find existing subplots, split existing subplots and bands
	// into len(vals) new subplots and bands, and transform each
	// GroupBy group into its new subplot.
	type bandKey struct {
		// band1 is the primary band. band2 is only used by
		// FacetWrap.
		band1, band2 *subplotBand

		// X and Y of band. This is a necessary part of the
		// key because FacetWrap creates rows but does not
		// create distant horizontal bands for them.
		x, y int
	}
	type bandScale struct {
		band  *subplotBand
		scale Scaler
	}
	subplots := make(map[*subplot][]*subplot)
	bands := make(map[bandKey][]*subplotBand)
	scales := make(map[bandScale]Scaler)
	var ndata table.GroupingBuilder
	for _, gid := range grouped.Tables() {
		// Find subplot by walking up group hierarchy.
		sub := subplotOf(gid)

		// Split old band into len(vals) new bands in the
		// orthogonal axis.
		var obandKey bandKey
		if dir == "x" {
			obandKey = bandKey{band1: sub.vBand, x: sub.x}
		} else if dir == "y" {
			obandKey = bandKey{band1: sub.hBand, y: sub.y}
		} else {
			obandKey = bandKey{sub.vBand, sub.hBand, sub.x, sub.y}
		}
		nbands := bands[obandKey]
		if nbands == nil {
			nbands = make([]*subplotBand, len(vals))
			for _, val := range vals {
				nb := &subplotBand{parent: obandKey.band1, label: val.label}
				nbands[val.index] = nb
			}
			bands[obandKey] = nbands
		}

		// Split old subplot into len(vals) new subplots.
		nsubplots := subplots[sub]
		if nsubplots == nil {
			nsubplots = make([]*subplot, len(vals))
			for _, val := range vals {
				ns := &subplot{parent: sub, x: sub.x, y: sub.y,
					vBand: sub.vBand, hBand: sub.hBand}
				if dir == "x" {
					ns.x = sub.x*len(vals) + val.index
					ns.vBand = nbands[val.index]
				} else if dir == "y" {
					ns.y = sub.y*len(vals) + val.index
					ns.hBand = nbands[val.index]
				} else {
					ns.x = sub.x*f.Cols + val.index%f.Cols
					ns.y = sub.y*f.Rows + val.index/f.Cols
					ns.vBand = nbands[val.index]
				}
				nsubplots[val.index] = ns
			}
			subplots[sub] = nsubplots
		}

		// Map this group to its new subplot.
		nsubplot := nsubplots[vals[gid.Label()].index]
		ngid := gid.Parent().Extend(nsubplot)
		ndata.Add(ngid, grouped.Table(gid))

		// Split scales if requested. At a high level, we want
		// to give each band a new scale, but there may
		// already be multiple scales within a band, so we
		// find the set of scales within a band and split each
		// distinct scale up.
		var nband *subplotBand
		if dir == "x" {
			nband = nsubplot.vBand
		} else if dir == "y" {
			nband = nsubplot.hBand
		} else {
			if f.SplitXScales || f.SplitYScales {
				// TODO: I probably need to rephrase
				// this whole scale splitting
				// operation in terms of subplot X and
				// Y and possibly do it as a second
				// pass once all of the subplots are
				// created.
				panic("not implemented: scale splitting for FacetWrap")
			}
		}
		if f.SplitXScales {
			scaler := p.GetScaleAt("x", gid)
			nscaler := scales[bandScale{nband, scaler}]
			if nscaler == nil {
				nscaler = scaler.CloneScaler()
				scales[bandScale{nband, scaler}] = nscaler
			}
			p.SetScaleAt("x", nscaler, ngid)
		}
		if f.SplitYScales {
			scaler := p.GetScaleAt("y", gid)
			nscaler := scales[bandScale{nband, scaler}]
			if nscaler == nil {
				nscaler = scaler.CloneScaler()
				scales[bandScale{nband, scaler}] = nscaler
			}
			p.SetScaleAt("y", nscaler, ngid)
		}
	}

	p.SetData(ndata.Done())
}

// subplotBand represents a rectangular group of subplots in either a
// vertical group (with a label on top) or a horizontal group (with a
// label to the right).
type subplotBand struct {
	parent *subplotBand
	label  string
}

type subplot struct {
	parent *subplot

	// x and y are the position of this subplot, where 0, 0 is the
	// top left.
	x, y int

	vBand, hBand *subplotBand
}

var rootSubplot = &subplot{}

func subplotOf(gid table.GroupID) *subplot {
	for ; gid != table.RootGroupID; gid = gid.Parent() {
		sub, ok := gid.Label().(*subplot)
		if ok {
			return sub
		}
	}
	return rootSubplot
}

func (s subplot) String() string {
	return fmt.Sprintf("[%d %d]", s.x, s.y)
}
