// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gg

import (
	"fmt"
	"math"
	"sort"

	"github.com/aclements/go-gg/gg/layout"
	"github.com/aclements/go-gg/table"
	"github.com/ajstarks/svgo"
)

// A plotElt is a high-level element of a plot layout.
//
// plotElts are arranged in a 2D grid. Coordinates in the grid are
// specified by a pair of "paths" rather than a simple pair of
// indexes. For example, element A is to the left of element B if A's
// X path is less than B's X path, where paths are compared as tuples
// with an infinite number of trailing 0's. This makes it easy to, for
// example, place an element to the right of another element without
// having to renumber all of the elements that are already to its
// right.
//
// The first level of the hierarchy is simply the coordinate of the
// plot in the grid. Within this, we layout plot elements as follows:
//
//                           +----------------------+
//                           | Label (x, y/-3/-1)   |
//                           +----------------------+
//                           | Label (x, y/-3/0)    |
//                           +----------------------+
//                           | Padding (x, y/-2)    |
//    +-----------+----------+----------------------+----------+------------+
//    | Padding   | YTicks   |                      | Padding  | Label      |
//    | (x/-2, y) | (x/-1,y) | Subplot (x, y)       | (x/2, y) | (x/3/0, y) |
//    |           |          |                      |          |            |
//    +-----------+----------+----------------------+----------+------------+
//                           | XTicks (x, y/1)      |
//                           +----------------------+
//                           | Padding (x, y/2)     |
//                           +----------------------+
//
// TODO: Should I instead think of this as specifying the edges rather
// than the cells?
type plotElt interface {
	layout.Element

	// paths returns the top-left and bottom-right cells of this
	// element. x2Path and y2Path may be nil, indicating that they
	// are the same as xPath and yPath.
	paths() (xPath, yPath, x2Path, y2Path eltPath)

	// render draws this plot element to r.svg.
	render(r *eltRender)
}

type eltRender struct {
	svg *svg.SVG
	id  int
}

func (r *eltRender) genid(prefix string) (id, ref string) {
	id = fmt.Sprintf("%s%d", prefix, r.id)
	ref = "url(#" + id + ")"
	r.id++
	return
}

type eltCommon struct {
	xPath, yPath, x2Path, y2Path eltPath
}

func (c *eltCommon) paths() (xPath, yPath, x2Path, y2Path eltPath) {
	return c.xPath, c.yPath, c.x2Path, c.y2Path
}

type eltSubplot struct {
	eltCommon
	layout.Leaf

	subplot *subplot
	marks   []plotMark
	scales  map[string]map[Scaler]bool

	xTicks, yTicks *eltTicks

	plotMargins struct {
		t, r, b, l float64
	}
}

func newEltSubplot(s *subplot) *eltSubplot {
	return &eltSubplot{
		eltCommon: eltCommon{xPath: eltPath{s.x}, yPath: eltPath{s.y}},
		subplot:   s,
		scales:    make(map[string]map[Scaler]bool),
	}
}

func (e *eltSubplot) SizeHint() (w, h float64, flexw, flexh bool) {
	return 0, 0, true, true
}

func (e *eltSubplot) SetLayout(x, y, w, h float64) {
	e.Leaf.SetLayout(x, y, w, h)
	m := &e.plotMargins
	m.t, m.r, m.b, m.l = plotMargins(w, h)
}

type eltTicks struct {
	eltCommon
	layout.Leaf

	axis     rune        // 'x' or 'y'
	ticksFor *eltSubplot // Subplot to which this is directly attached
	ticks    map[Scaler]plotEltTicks
}

type plotEltTicks struct {
	major  table.Slice
	labels []string
}

func newEltTicks(axis rune, s *eltSubplot) *eltTicks {
	elt := &eltTicks{
		eltCommon: s.eltCommon,
		axis:      axis,
		ticksFor:  s,
	}
	switch axis {
	case 'x':
		elt.yPath = eltPath{s.subplot.y, 1}
	case 'y':
		elt.xPath = eltPath{s.subplot.x, -1}
	default:
		panic("bad axis")
	}
	return elt
}

func (e *eltTicks) scales() map[Scaler]bool {
	switch e.axis {
	case 'x':
		return e.ticksFor.scales["x"]
	case 'y':
		return e.ticksFor.scales["y"]
	default:
		panic("bad axis")
	}
}

func (e *eltTicks) mapTicks(s Scaler, ticks table.Slice) (pixels []float64) {
	x, y, w, h := e.Layout()
	// TODO: This doesn't show ticks in the margin area. This may
	// be fine with niced tick labels, but it tends to look bad
	// with un-niced ticks. Ideally we would expand the input
	// domain instead, but this isn't well-defined for discrete
	// scales. We could use Unmap to try to find the expanded
	// input domain on both sides, but fall back to expanding the
	// ranger if Unmap fails (which it would for a discrete
	// scale).
	m := e.ticksFor.plotMargins
	switch e.axis {
	case 'x':
		s.Ranger(NewFloatRanger(x+m.l, x+w-m.r))
	case 'y':
		s.Ranger(NewFloatRanger(y+h-m.b, y+m.t))
	}
	return mapMany(s, ticks).([]float64)
}

// computeTicks computes the location and labels of the ticks in
// element e based on the dimensions of e.ticksFor (which must have
// been laid out prior to calling this).
func (e *eltTicks) computeTicks() {
	const tickDistance = 30 // TODO: Theme. Min pixels between tick labels.

	_, _, w, h := e.ticksFor.Layout()
	var dim float64
	switch e.axis {
	case 'x':
		dim = w
	case 'y':
		dim = h
	}

	// Compute max ticks assuming the labels are zero sized.
	maxTicks := int(dim / tickDistance)

	// Optimize ticks, keeping labels at least tickDistance apart.
	e.ticks = make(map[Scaler]plotEltTicks)
	for s := range e.scales() {
		pred := func(ticks []float64, labels []string) bool {
			if len(ticks) <= 1 {
				return true
			}
			// Check distance between labels.
			pos := e.mapTicks(s, ticks)
			// Ticks are in value order, but we need them
			// in position order.
			sort.Float64s(pos)
			var last float64
			for i, p := range pos {
				if i > 0 && p-last < tickDistance {
					// Labels i-1 and i are too close.
					return false
				}
				metrics := measureString(fontSize, labels[i])
				switch e.axis {
				case 'x':
					last = p + metrics.width
				case 'y':
					last = p + metrics.leading
				}
			}

			return true
		}
		major, _, labels := s.Ticks(maxTicks, pred)
		e.ticks[s] = plotEltTicks{major, labels}
	}
}

func (e *eltTicks) SizeHint() (w, h float64, flexw, flexh bool) {
	if len(e.ticks) == 0 {
		// Ticks haven't been computed yet or there are none.
		// Assume this takes up no space.
		switch e.axis {
		case 'x':
			return 0, 0, true, false
		case 'y':
			return 0, 0, false, true
		default:
			panic("bad axis")
		}
	}

	var maxWidth, maxHeight float64
	for s := range e.scales() {
		for _, label := range e.ticks[s].labels {
			metrics := measureString(fontSize, label)
			maxHeight = math.Max(maxHeight, metrics.leading)
			maxWidth = math.Max(maxWidth, metrics.width)
		}
	}
	switch e.axis {
	case 'x':
		maxHeight += xTickSep
	case 'y':
		maxWidth += yTickSep
	}
	return maxWidth, maxHeight, e.axis == 'x', e.axis == 'y'
}

type eltLabel struct {
	eltCommon
	layout.Leaf

	side  rune // 't', 'b', 'l', 'r'
	label string
	fill  string
}

func newEltLabelFacet(side rune, label string, x1, y1, x2, y2 int, level int) *eltLabel {
	elt := &eltLabel{
		side:  side,
		label: label,
		fill:  "#ccc", // TODO: Theme.
	}
	switch side {
	case 't':
		elt.eltCommon = eltCommon{
			xPath:  eltPath{x1},
			yPath:  eltPath{y1, -3, -level},
			x2Path: eltPath{x2},
		}
	case 'r':
		elt.eltCommon = eltCommon{
			xPath:  eltPath{x2, 3, level},
			yPath:  eltPath{y1},
			y2Path: eltPath{y2},
		}
	default:
		panic("bad side")
	}
	return elt
}

func newEltLabelAxis(side rune, label string, x, y, span int) *eltLabel {
	elt := &eltLabel{
		eltCommon: eltCommon{xPath: eltPath{x}, yPath: eltPath{y}},
		side:      side,
		label:     label,
		fill:      "none",
	}
	switch side {
	case 'T', 'b':
		elt.x2Path = eltPath{x + span}
	case 'l':
		elt.y2Path = eltPath{y + span}
	default:
		panic("bad side")
	}
	return elt
}

func (e *eltLabel) SizeHint() (w, h float64, flexw, flexh bool) {
	// TODO: We actually want the height of the text, which could
	// be N*leading if there are multiple lines.
	dim := measureString(fontSize, e.label).leading * facetLabelHeight
	switch e.side {
	case 't', 'b':
		return 0, dim, true, false
	case 'T': // Titles
		return 0, 1.5 * dim, true, false
	case 'l', 'r':
		return dim, 0, false, true
	default:
		panic("bad side")
	}
}

type eltPadding struct {
	eltCommon
	layout.Leaf

	side rune // 't', 'b', 'l', 'r'
}

func newEltPadding(side rune, x, y int) *eltPadding {
	elt := &eltPadding{
		eltCommon: eltCommon{xPath: eltPath{x}, yPath: eltPath{y}},
		side:      side,
	}
	switch side {
	case 't':
		elt.yPath = eltPath{y, -2}
	case 'r':
		elt.xPath = eltPath{x, 2}
	case 'b':
		elt.yPath = eltPath{y, 2}
	case 'l':
		elt.xPath = eltPath{x, -2}
	default:
		panic("bad side")
	}
	return elt
}

func (e *eltPadding) SizeHint() (w, h float64, flexw, flexh bool) {
	const padding = 4 // TODO: Theme.

	switch e.side {
	case 't', 'b':
		return 0, padding, true, false
	case 'l', 'r':
		return padding, 0, false, true
	default:
		panic("bad side")
	}
}

func addSubplotLabels(elts []plotElt) []plotElt {
	// Find the regions covered by each subplot band.
	vBands := make(map[*subplotBand]subplotRegion)
	hBands := make(map[*subplotBand]subplotRegion)
	for _, elt := range elts {
		elt, ok := elt.(*eltSubplot)
		if !ok {
			continue
		}
		s := elt.subplot

		level := 0
		for vBand := s.vBand; vBand != nil; vBand = vBand.parent {
			r := vBands[vBand]
			r.update(s, level)
			vBands[vBand] = r
			level++
		}

		level = 0
		for hBand := s.hBand; hBand != nil; hBand = hBand.parent {
			r := hBands[hBand]
			r.update(s, level)
			hBands[hBand] = r
			level++
		}
	}

	// Create ticks.
	//
	// TODO: If the facet grid isn't total, this can add ticks to
	// the side of a plot that's in the middle of the grid and
	// that creates a gap between all of the plots. This seems
	// like a fundamental limitation of treating this as a grid.
	// We could either abandon the grid and instead use a
	// hierarchy of left-of/right-of/above/below relations, or we
	// could make facets produce a total grid.
	var prev *eltSubplot
	var curTicks *eltTicks
	sorter := newSubplotSorter(elts, 'x')
	sort.Sort(sorter)
	for _, elt := range sorter.elts {
		if prev == nil || prev.subplot.y != elt.subplot.y || !eqScales(prev, elt, "y") {
			// Show Y axis ticks.
			curTicks = newEltTicks('y', elt)
			elts = append(elts, curTicks)
		}
		elt.yTicks = curTicks
		prev = elt
	}
	sorter.dir = 'y'
	sort.Sort(sorter)
	prev, curTicks = nil, nil
	for _, elt := range sorter.elts {
		if prev == nil || prev.subplot.x != elt.subplot.x || !eqScales(prev, elt, "x") {
			// Show X axis ticks.
			curTicks = newEltTicks('x', elt)
			elts = append(elts, curTicks)
		}
		elt.xTicks = curTicks
		prev = elt
	}

	// Create labels.
	for vBand, r := range vBands {
		elts = append(elts, newEltLabelFacet('t', vBand.label, r.x1, r.y1, r.x2, r.y2, r.level))
	}
	for hBand, r := range hBands {
		elts = append(elts, newEltLabelFacet('r', hBand.label, r.x1, r.y1, r.x2, r.y2, r.level))
	}
	return elts
}

func addAxisLabels(elts []plotElt, title, xlabel, ylabel string) []plotElt {
	// Find the region covered by subplots.
	var r subplotRegion
	for _, elt := range elts {
		elt, ok := elt.(*eltSubplot)
		if !ok {
			continue
		}
		r.update(elt.subplot, 0)
	}
	if !r.valid {
		return elts
	}

	// Add title.
	// TODO: Make this larger.
	if title != "" {
		elts = append(elts,
			newEltLabelAxis('T', title, r.x1, r.y1-1, r.x2-r.x1))
	}

	// Add labels.
	elts = append(elts,
		newEltLabelAxis('b', xlabel, r.x1, r.y2+1, r.x2-r.x1),
		newEltLabelAxis('l', ylabel, r.x1-1, r.y1, r.y2-r.y1))
	return elts
}

type subplotRegion struct {
	valid                 bool
	x1, x2, y1, y2, level int
}

func (r *subplotRegion) update(s *subplot, level int) {
	if !r.valid {
		r.x1, r.x2, r.y1, r.y2, r.level = s.x, s.x, s.y, s.y, level
		r.valid = true
		return
	}
	if s.x < r.x1 {
		r.x1 = s.x
	} else if s.x > r.x2 {
		r.x2 = s.x
	}
	if s.y < r.y1 {
		r.y1 = s.y
	} else if s.y > r.y2 {
		r.y2 = s.y
	}
	if level > r.level {
		r.level = level
	}
}

// subplotSorter sorts eltSubplots by subplot (x, y) position.
type subplotSorter struct {
	elts []*eltSubplot

	// dir indicates primary sorting direction: 'x' means to sort
	// left-to-right, top-to-bottom; 'y' means to sort
	// bottom-to-top, left-to-right.
	dir rune
}

func newSubplotSorter(elts []plotElt, dir rune) *subplotSorter {
	selts := []*eltSubplot{}
	for _, elt := range elts {
		if s, ok := elt.(*eltSubplot); ok {
			selts = append(selts, s)
		}
	}
	return &subplotSorter{selts, dir}
}

func (s subplotSorter) Len() int {
	return len(s.elts)
}

func (s subplotSorter) Less(i, j int) bool {
	a, b := s.elts[i], s.elts[j]
	if s.dir == 'x' {
		if a.subplot.y != b.subplot.y {
			return a.subplot.y < b.subplot.y
		}
		return a.subplot.x < b.subplot.x
	} else {
		if a.subplot.x != b.subplot.x {
			return a.subplot.x < b.subplot.x
		}
		return a.subplot.y > b.subplot.y
	}
}

func (s subplotSorter) Swap(i, j int) {
	s.elts[i], s.elts[j] = s.elts[j], s.elts[i]
}

func eqScales(a, b *eltSubplot, aes string) bool {
	sa, sb := a.scales[aes], b.scales[aes]
	if len(sa) != len(sb) {
		return false
	}
	for k, v := range sa {
		if sb[k] != v {
			return false
		}
	}
	return true
}

type eltPath []int

func (a eltPath) cmp(b eltPath) int {
	for len(a) > 0 || len(b) > 0 {
		var ax, bx int
		if len(a) > 0 {
			ax, a = a[0], a[1:]
		}
		if len(b) > 0 {
			bx, b = b[0], b[1:]
		}
		if ax != bx {
			if ax < bx {
				return -1
			} else {
				return 1
			}
		}
	}
	return 0
}

type eltPaths []eltPath

func (s eltPaths) Len() int {
	return len(s)
}

func (s eltPaths) Less(i, j int) bool {
	return s[i].cmp(s[j]) < 0
}

func (s eltPaths) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s eltPaths) nub() eltPaths {
	var i, o int
	for i, o = 1, 1; i < len(s); i++ {
		if s[i].cmp(s[i-1]) != 0 {
			s[o] = s[i]
			o++
		}
	}
	return s[:o]
}

func (s eltPaths) find(p eltPath) int {
	return sort.Search(len(s), func(i int) bool {
		return s[i].cmp(p) >= 0
	})
}

// layoutPlotElts returns a layout containing all of the elements in
// elts.
//
// layoutPlotElts flattens the X and Y paths of elts into simple
// coordinate indexes and constructs a layout.Grid.
func layoutPlotElts(elts []plotElt) layout.Element {
	// Add padding elements to each subplot.
	//
	// TODO: Should there be padding between labels and the plot?
	for _, elt := range elts {
		elt, ok := elt.(*eltSubplot)
		if !ok {
			continue
		}
		x, y := elt.xPath[0], elt.yPath[0]
		elts = append(elts,
			newEltPadding('t', x, y),
			newEltPadding('r', x, y),
			newEltPadding('b', x, y),
			newEltPadding('l', x, y),
		)
	}

	// Construct the global element grid from coordinate paths by
	// sorting the sets of X paths and Y paths to each leaf and
	// computing a global (x,y) for each leaf from these orders.
	type eltPos struct {
		x, y, xSpan, ySpan int
	}
	flat := map[plotElt]eltPos{}
	dir := func(get func(plotElt) (p, p2 eltPath), set func(p *eltPos, pos, span int)) {
		var paths eltPaths
		for _, elt := range elts {
			p, p2 := get(elt)
			paths = append(paths, p)
			if p2 != nil {
				paths = append(paths, p2)
			}
		}
		sort.Sort(paths)
		paths = paths.nub()
		for _, elt := range elts {
			p, p2 := get(elt)
			pos, span := paths.find(p), 1
			if p2 != nil {
				span = paths.find(p2) - pos + 1
			}
			eltPos := flat[elt]
			set(&eltPos, pos, span)
			flat[elt] = eltPos
		}
	}
	dir(func(e plotElt) (p, p2 eltPath) {
		p, _, p2, _ = e.paths()
		return
	}, func(p *eltPos, pos, span int) {
		p.x, p.xSpan = pos, span
	})
	dir(func(e plotElt) (p, p2 eltPath) {
		_, p, _, p2 = e.paths()
		return
	}, func(p *eltPos, pos, span int) {
		p.y, p.ySpan = pos, span
	})

	// Construct the grid layout.
	l := new(layout.Grid)
	for elt, pos := range flat {
		l.Add(elt, pos.x, pos.y, pos.xSpan, pos.ySpan)
	}
	return l
}
