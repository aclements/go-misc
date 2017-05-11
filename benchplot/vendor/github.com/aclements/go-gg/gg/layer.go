// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gg

import (
	"fmt"

	"github.com/aclements/go-gg/table"
)

func defaultCols(p *Plot, cols ...*string) {
	dcols := p.Data().Columns()
	for i, colp := range cols {
		if *colp == "" {
			if i >= len(dcols) {
				panic(fmt.Sprintf("cannot get default column %d; table has only %d columns", i, len(dcols)))
			}
			*colp = dcols[i]
		}
	}
}

// LayerLines is like LayerPaths, but connects data points in order by
// the "x" property.
type LayerLines LayerPaths

func (l LayerLines) Apply(p *Plot) {
	LayerPaths(l).apply(p, true)
}

//go:generate stringer -type StepMode

// StepMode controls how LayerSteps connects subsequent points.
type StepMode int

const (
	// StepHV makes LayerSteps connect subsequent points with a
	// horizontal segment and then a vertical segment.
	StepHV StepMode = iota

	// StepVH makes LayerSteps connect subsequent points with a
	// vertical segment and then a horizontal segment.
	StepVH

	// StepHMid makes LayerSteps connect subsequent points A and B
	// with three segments: a horizontal segment from A to the
	// midpoint between A and B, followed by vertical segment,
	// followed by a horizontal segment from the midpoint to B.
	StepHMid

	// StepVMid makes LayerSteps connect subsequent points A and B
	// with three segments: a vertical segment from A to the
	// midpoint between A and B, followed by horizontal segment,
	// followed by a vertical segment from the midpoint to B.
	StepVMid
)

// LayerSteps is like LayerPaths, but connects data points with a path
// consisting only of horizontal and vertical segments.
type LayerSteps struct {
	LayerPaths

	Step StepMode
}

func (l LayerSteps) Apply(p *Plot) {
	// TODO: Should this also support only showing horizontal or
	// vertical segments?
	//
	// TODO: This could be a data transform instead of a layer.
	// Then it could be used in conjunction with, for example,
	// ribbons.

	defaultCols(p, &l.X, &l.Y)
	p.marks = append(p.marks, plotMark{&markSteps{
		l.Step,
		p.use("x", l.X),
		p.use("y", l.Y),
		p.use("stroke", l.Color),
		p.use("fill", l.Fill),
	}, p.Data().Tables()})
}

// LayerPaths groups by Color and Fill, and then connects successive
// data points in each group with a path and/or a filled polygon.
type LayerPaths struct {
	// X and Y name columns that define the input and response of
	// each point on the path. If these are empty, they default to
	// the first and second columns, respectively.
	X, Y string

	// Color names a column that defines the stroke color of each
	// path. If Color is "", it defaults to constant black.
	// Otherwise, the data is grouped by Color.
	Color string

	// Fill names a column that defines the fill color of each
	// path. If Fill is "", it defaults to none. Otherwise, the
	// data is grouped by Fill.
	Fill string

	// XXX Perhaps the theme should provide default values for
	// things like "color". That would suggest we need to resolve
	// defaults like that at render time. Possibly a special scale
	// that gets values from the theme could be used to resolve
	// them.
	//
	// XXX strokeOpacity, fillOpacity, strokeWidth, what other
	// properties do SVG strokes have?
	//
	// XXX Should the set of known styling bindings be fixed, and
	// all possible rendering targets have to know what to do with
	// them, or should the rendering target be able to have
	// different styling bindings they understand (presumably with
	// some reasonable base set)? If the renderer can determine
	// the known bindings, we would probably just capture the
	// environment here (and make it so a captured environment
	// does not change) and hand that to the renderer later.
}

func (l LayerPaths) Apply(p *Plot) {
	l.apply(p, false)
}

func (l LayerPaths) apply(p *Plot, sort bool) {
	defaultCols(p, &l.X, &l.Y)
	if l.Color != "" {
		p.GroupBy(l.Color)
	}
	if l.Fill != "" {
		p.GroupBy(l.Fill)
	}
	if sort {
		defer p.Save().Restore()
		p = p.SortBy(l.X)
	}

	p.marks = append(p.marks, plotMark{&markPath{
		p.use("x", l.X),
		p.use("y", l.Y),
		p.use("stroke", l.Color),
		p.use("fill", l.Fill),
	}, p.Data().Tables()})
}

// LayerArea shades the area between two columns with a polygon. It is
// useful in conjunction with ggstat.AggMax and ggstat.AggMin for
// drawing the extents of data.
type LayerArea struct {
	// X names the column that defines the input of each point. If
	// this is empty, it defaults to the first column.
	X string

	// Upper and Lower name columns that define the vertical
	// bounds of the shaded area. If either is "", it defaults to
	// 0.
	Upper, Lower string

	// Fill names a column that defines the fill color of each
	// path. If Fill is "", it defaults to none. Otherwise, the
	// data is grouped by Fill.
	Fill string
}

func (l LayerArea) Apply(p *Plot) {
	defaultCols(p, &l.X)
	if l.Fill != "" {
		p.GroupBy(l.Fill)
	}
	defer p.Save().Restore()
	p = p.SortBy(l.X)
	p.marks = append(p.marks, plotMark{&markArea{
		p.use("x", l.X),
		p.use("y", l.Upper),
		p.use("y", l.Lower),
		p.use("fill", l.Fill),
	}, p.Data().Tables()})
}

// LayerPoints layers a point mark at each data point.
type LayerPoints struct {
	// X and Y name columns that define input and response of each
	// point. If these are empty, they default to the first and
	// second columns, respectively.
	X, Y string

	// Color names the column that defines the fill color of each
	// point. If Color is "", it defaults to constant black.
	Color string

	// Opacity names the column that defines the opacity of each
	// point. If Opacity is "", it defaults to fully opaque. This
	// is multiplied by any alpha value specified by Color.
	Opacity string

	// Size names the column that defines the size of each point.
	// If Size is "", it defaults to 1% of the smallest plot
	// dimension.
	Size string

	// XXX fill vs stroke, shape
}

func (l LayerPoints) Apply(p *Plot) {
	defaultCols(p, &l.X, &l.Y)
	p.marks = append(p.marks, plotMark{&markPoint{
		p.use("x", l.X),
		p.use("y", l.Y),
		// TODO: It's actually the fill color, but I generally
		// want it to match things that are stroke colors.
		// Maybe I should have a "color" aesthetic for the
		// "primary" color? Or I could have a hierarchy of
		// aesthetics, in which this uses "stroke" if it has a
		// scale, but otherwise uses "color".
		p.use("stroke", l.Color),
		// TODO: What scale for opacity? Or should I assume
		// callers will use PreScaled values if they want
		// specific opacities? What's the physical type?
		p.use("opacity", l.Opacity),
		p.use("size", l.Size),
	}, p.Data().Tables()})
}

// LayerTiles layers a rectangle at each data point. The rectangle is
// specified by its center, width, and height.
type LayerTiles struct {
	// X and Y name columns that define the input and response at
	// the center of each rectangle. If they are "", they default
	// to the first and second columns, respectively.
	X, Y string

	// Width and Height name columns that define the width and
	// height of each rectangle. If they are "", the width and/or
	// height are automatically determined from the smallest
	// spacing between distinct X and Y points.
	Width, Height string

	// Fill names a column that defines the fill color of each
	// rectangle. If it is "", the default fill is black.
	Fill string

	// XXX Stroke color/width, opacity, center adjustment.
}

func (l LayerTiles) Apply(p *Plot) {
	defaultCols(p, &l.X, &l.Y)
	if l.Width != "" || l.Height != "" {
		// TODO: What scale are these in? (x+width) is in the
		// X scale, but width itself is not. It doesn't make
		// sense to train the X scale on width, and if there's
		// a scale transform, (x+width) has to happen before
		// the transform. OTOH, if x is discrete, I can't do
		// (x+width); maybe in that case you just can't
		// specify a width. OTOOH, if width is specified and
		// the value is unscaled, I could still do something
		// reasonable with that if x is discrete.
		panic("not implemented: non-default width/height")
	}
	p.marks = append(p.marks, plotMark{&markTiles{
		p.use("x", l.X),
		p.use("y", l.Y),
		p.use("fill", l.Fill),
	}, p.Data().Tables()})
}

// LayerTags attaches text annotations to data points.
//
// TODO: Currently this groups by label and makes one annotation per
// group. This should be a controllable.
type LayerTags struct {
	// X and Y name columns that define the input and response
	// each tag is attached to. If they are "", they default to
	// the first and second columns, respectively.
	X, Y string

	// Label names the column that gives the text to put in the
	// tag at X, Y. Label is required.
	Label string
}

func (l LayerTags) Apply(p *Plot) {
	// TODO: Should there be special "annotation marks" that are
	// always on top and can perhaps extend outside the plot area?

	defaultCols(p, &l.X, &l.Y)
	defer p.Save().Restore()
	p.GroupBy(l.Label)
	// TODO: I keep wanting an abstraction for a column across
	// groups like this.
	labels := make(map[table.GroupID]table.Slice)
	for _, gid := range p.Data().Tables() {
		labels[gid] = p.Data().Table(gid).MustColumn(l.Label)
	}

	p.marks = append(p.marks, plotMark{&markTags{
		p.use("x", l.X),
		p.use("y", l.Y),
		labels,
	}, p.Data().Tables()})
}

// LayerTooltips attaches hover tooltips to data points.
type LayerTooltips struct {
	// X and Y name columns that define locations of tooltips. If
	// they are "", they default to the first and second columns,
	// respectively.
	X, Y string

	// Label names the column that gives the text of the tooltip.
	Label string

	// TODO: Text styling, closest X or closest point, multiple
	// tooltips if there are multiple points at the same X with
	// different Ys?
}

func (l LayerTooltips) Apply(p *Plot) {
	defer p.Save().Restore()

	defaultCols(p, &l.X, &l.Y)

	// Split up by subplot and flatten each subplot.
	tables := map[*subplot][]*table.Table{}
	gids := map[*subplot]table.GroupID{}
	for _, gid := range p.Data().Tables() {
		s := subplotOf(gid)
		tables[s] = append(tables[s], p.Data().Table(gid))
		gids[s] = gid
	}
	var ng table.GroupingBuilder
	for k, ts := range tables {
		var subg table.GroupingBuilder
		for i, t := range ts {
			subg.Add(table.RootGroupID.Extend(i), t)
		}
		ngid := table.RootGroupID.Extend(k)
		ng.Add(ngid, table.Flatten(subg.Done()))
		p.copyScales(gids[k], ngid)
	}
	p.SetData(ng.Done())

	labels := make(map[table.GroupID]table.Slice)
	for _, gid := range p.Data().Tables() {
		labels[gid] = p.Data().Table(gid).MustColumn(l.Label)
	}
	p.marks = append(p.marks, plotMark{&markTooltips{
		p.use("x", l.X),
		p.use("y", l.Y),
		labels,
	}, p.Data().Tables()})
}
