// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gg

import (
	"bytes"
	"fmt"
	"io"
	"math"
	"reflect"
	"strings"

	"github.com/aclements/go-gg/generic/slice"
	"github.com/aclements/go-gg/table"
	"github.com/ajstarks/svgo"
)

// fontSize is the font size in pixels.
//
// TODO: Theme.
const fontSize float64 = 14

// facetLabelHeight is the height of facet labels, as a multiple of
// the font height.
//
// TODO: Should this be a multiple of fontSize, em height, leading?
// Currently it's leading.
//
// TODO: Theme.
const facetLabelHeight = 1.3

const xTickSep = 5 // TODO: Theme.

const yTickSep = 5 // TODO: Theme.

// plotMargins returns the top, right, bottom, and left margins for a
// plot of the given width and height.
//
// By default, this adds a 5% margin based on the smaller of width and
// height. This ensures that (with automatic scales), the extremes of
// the data and its tick labels don't appear right at the edge of the
// plot area.
//
// TODO: Theme.
var plotMargins = func(w, h float64) (t, r, b, l float64) {
	margin := 0.05 * math.Min(w, h)
	return margin, margin, margin, margin
}

func (p *Plot) WriteSVG(w io.Writer, width, height int) error {
	// TODO: Legend, title.

	// TODO: Check if the same scaler is used for multiple
	// aesthetics with conflicting rangers. Alternatively, if we
	// just computed the scaled data eagerly here, it wouldn't
	// matter if the same Scaler was used for multiple things
	// because we would just change its Ranger between scaling
	// different data. We could still optimize for get/get1 by
	// specifying whether we care about all of the values or just
	// the first when fetching the scaledData (arguably this
	// should also affect scale training, so this is necessary
	// anyway).

	// TODO: Rather than finding these scales here and giving them
	// Ratners, we could use special "Width"/"Height" Rangers and
	// assign them much earlier (e.g., when they are Used). We
	// could then either find all of the scales that have those
	// Rangers and configure them at this point, or we could pass
	// the renderEnv in when ranging.

	// TODO: Default ranges for other things like color.

	// TODO: Expose the layout so a package user can put together
	// multiple Plots.
	//
	// What if the user wants multiple aligned plots, but as
	// *different* images (e.g., flipping from one slide to
	// another)?

	// TODO: Let the user alternatively specify the width and
	// height of the subplots, rather than the whole plot.

	// TODO: Automatic aspect ratio by averaging slopes.

	// TODO: Custom tick breaks.

	// TODO: Make sure *all* Scalers have Rangers or the user will
	// get confusing panics.

	// TODO: If the user restricts, say, the X range, should that
	// only train the Y axis on what's in the X range?

	// Assign default Rangers to scales that don't have them.
	//
	// TODO: Do this on a clone of the scale so this doesn't
	// persist.
	for aes, scales := range p.scales {
		if aes == "x" || aes == "y" {
			// We'll assign these when we render each
			// subplot.
			continue
		}
		for _, scale := range scales.scales {
			if scale.Ranger(nil) == nil {
				scale.Ranger(defaultRanger(aes))
			}
		}
	}

	// Find all of the subplots and subdivide the marks.
	//
	// TODO: If a mark was done in a parent subplot, broadcast it
	// to all child leafs of that subplot.
	subplots := make(map[*subplot]*eltSubplot)
	plotElts := []plotElt{}
	for _, mark := range p.marks {
		submarks := make(map[*eltSubplot]plotMark)
		for _, gid := range mark.groups {
			subplot := subplotOf(gid)
			elt := subplots[subplot]
			if elt == nil {
				elt = newEltSubplot(subplot)
				plotElts = append(plotElts, elt)
				subplots[subplot] = elt
			}

			submark := submarks[elt]
			submark.m = mark.m
			submark.groups = append(submark.groups, gid)
			submarks[elt] = submark
		}
		for subplot, submark := range submarks {
			subplot.marks = append(subplot.marks, submark)
		}
	}
	// Subdivide the scales.
	for sk := range p.scaleSet {
		subplot := subplotOf(sk.gid)
		elt := subplots[subplot]
		if elt == nil {
			continue
		}
		ss := elt.scales[sk.aes]
		if ss == nil {
			ss = make(map[Scaler]bool)
			elt.scales[sk.aes] = ss
		}
		ss[sk.scale] = true
	}

	// Add ticks and facet labels.
	plotElts = addSubplotLabels(plotElts)

	// Add axis labels and title.
	var xlabel, ylabel string
	if l, ok := p.axisLabels["x"]; ok {
		xlabel = l
	} else {
		xlabel = strings.Join(slice.Nub(p.autoAxisLabels["x"]).([]string), "\n")
	}
	if l, ok := p.axisLabels["y"]; ok {
		ylabel = l
	} else {
		ylabel = strings.Join(slice.Nub(p.autoAxisLabels["y"]).([]string), "\n")
	}
	plotElts = addAxisLabels(plotElts, p.title, xlabel, ylabel)

	// Compute plot element layout.
	layout := layoutPlotElts(plotElts)

	// Perform layout. There's a cyclic dependency involving tick
	// labels here: the tick labels depend on how many ticks there
	// are, how many ticks there are depends on the size of the
	// plot, the size of the plot depends on its surrounding
	// content, and the size of the surrounding content depends on
	// the tick labels. There may not be a fixed point here, so we
	// compromise around the number of ticks.
	//
	// 1) Lay out the graphs without ticks.
	layout.SetLayout(0, 0, float64(width), float64(height))
	// 2) Compute the number of ticks and tick labels for each
	// tick element.
	for _, elt := range plotElts {
		if elt, ok := elt.(*eltTicks); ok {
			elt.computeTicks()
		}
	}
	// 3) Re-layout the plot and stick with the ticks we computed.
	layout.SetLayout(0, 0, float64(width), float64(height))

	// Draw.
	svg := svg.New(w)
	svg.Start(width, height, fmt.Sprintf(`font-size="%.6gpx" font-family="Roboto,&quot;Helvetica Neue&quot;,Helvetica,Arial,sans-serif"`, fontSize))
	defer svg.End()

	// Render each plot element.
	r := &eltRender{svg, 0}
	for _, elt := range plotElts {
		elt.render(r)
	}

	return nil
}

func (e *eltSubplot) render(r *eltRender) {
	svg := r.svg
	x, y, w, h := e.Layout()
	m := e.plotMargins

	// Round the bounds rectangle in.
	x2i, y2i := int(x+w), int(y+h)
	xi, yi := int(math.Ceil(x)), int(math.Ceil(y))
	wi, hi := x2i-xi, y2i-yi

	// Create clip region for plot area.
	clipId, clipRef := r.genid("clip")
	svg.ClipPath(`id="` + clipId + `"`)
	svg.Rect(xi, yi, wi, hi)
	svg.ClipEnd()
	svg.Group(`clip-path="` + clipRef + `"`)

	// Set scale ranges.
	xRanger := NewFloatRanger(float64(xi)+m.l, float64(x2i)-m.r)
	yRanger := NewFloatRanger(float64(y2i)-m.b, float64(yi)+m.t)
	for s := range e.scales["x"] {
		s.Ranger(xRanger)
	}
	for s := range e.scales["y"] {
		s.Ranger(yRanger)
	}

	// Render grid.
	renderBackground(svg, xi, yi, wi, hi)
	for s := range e.scales["x"] {
		renderGrid(svg, 'x', s, e.xTicks.ticks[s], yi, y2i)
	}
	for s := range e.scales["y"] {
		renderGrid(svg, 'y', s, e.yTicks.ticks[s], xi, x2i)
	}

	// Create rendering environment.
	env := &renderEnv{
		cache: make(map[renderCacheKey]table.Slice),
		area:  [4]float64{float64(xi), float64(yi), float64(wi), float64(hi)},
	}

	// Render marks.
	for _, mark := range e.marks {
		for _, gid := range mark.groups {
			env.gid = gid
			mark.m.mark(env, svg)
		}
	}

	// End clip region.
	svg.Gend()

	// Draw border and scale ticks.
	//
	// TODO: Theme.

	// Render border.
	svg.Path(fmt.Sprintf("M%d %dV%dH%d", xi, yi, y2i, x2i), "stroke:#888; fill:none; stroke-width:2") // TODO: Theme.

	// Render scale ticks.
	for s := range e.scales["x"] {
		renderScale(svg, 'x', s, e.xTicks.ticks[s], y2i)
	}
	for s := range e.scales["y"] {
		renderScale(svg, 'y', s, e.yTicks.ticks[s], xi)
	}
}

// TODO: Use shape-rendering: crispEdges?

func renderBackground(svg *svg.SVG, x, y, w, h int) {
	svg.Rect(x, y, w, h, "fill:#eee") // TODO: Theme.
}

func renderGrid(svg *svg.SVG, dir rune, scale Scaler, ticks plotEltTicks, start, end int) {
	major := mapMany(scale, ticks.major).([]float64)

	r := func(x float64) float64 {
		// Round to nearest N.
		return math.Floor(x + 0.5)
	}

	var path []string
	for _, p := range major {
		if dir == 'x' {
			path = append(path, fmt.Sprintf("M%.6g %dv%d", r(p), start, end-start))
		} else {
			path = append(path, fmt.Sprintf("M%d %.6gh%d", start, r(p), end-start))
		}
	}

	svg.Path(wrapPath(strings.Join(path, "")), "stroke: #fff; stroke-width:2") // TODO: Theme.
}

func renderScale(svg *svg.SVG, dir rune, scale Scaler, ticks plotEltTicks, pos int) {
	const length float64 = 4 // TODO: Theme

	var path bytes.Buffer
	have := map[float64]bool{}
	for _, t := range []struct {
		length float64
		s      table.Slice
	}{
		{length * 2, ticks.major},
		{length, ticks.minor},
	} {
		ticks := mapMany(scale, t.s).([]float64)

		r := func(x float64) float64 {
			// Round to nearest N.
			return math.Floor(x + 0.5)
		}
		for _, p := range ticks {
			p = r(p)
			if have[p] {
				// Avoid overplotting the same tick
				// marks.
				continue
			}
			have[p] = true
			if dir == 'x' {
				fmt.Fprintf(&path, "M%.6g %dv%.6g", p, pos, -t.length)
			} else {
				fmt.Fprintf(&path, "M%d %.6gh%.6g", pos, p, t.length)
			}
		}

	}
	svg.Path(wrapPath(path.String()), "stroke:#888; stroke-width:2") // TODO: Theme
}

func (e *eltTicks) render(r *eltRender) {
	svg := r.svg
	x, y, w, _ := e.Layout()
	for s := range e.scales() {
		pos := e.mapTicks(s, e.ticks[s].major)
		for i, label := range e.ticks[s].labels {
			tick := pos[i]
			if e.axis == 'x' {
				svg.Text(int(tick), int(y+xTickSep), label, `text-anchor="middle" dy="1em" fill="#666"`) // TODO: Theme.
			} else {
				svg.Text(int(x+w-yTickSep), int(tick), label, `text-anchor="end" dy=".3em" fill="#666"`)
			}
		}
	}
}

func (e *eltLabel) render(r *eltRender) {
	svg := r.svg
	x, y, w, h := e.Layout()

	// Clip to label region.
	clipId, clipRef := r.genid("clip")
	svg.ClipPath(`id="` + clipId + `"`)
	svg.Rect(int(x), int(y), int(w), int(h))
	svg.ClipEnd()
	svg.Group(`clip-path="` + clipRef + `"`)
	defer svg.Gend()

	if e.fill != "none" {
		svg.Rect(int(x), int(y), int(w), int(h), "fill: "+e.fill)
	}
	// Vertical centering is very poorly
	// supported. dy is the best chance.
	style := `text-anchor="middle" dy=".3em"`
	switch e.side {
	case 'l':
		style += fmt.Sprintf(` transform="rotate(-90 %d %d)"`, int(x+w/2), int(y+h/2))
	case 'r':
		style += fmt.Sprintf(` transform="rotate(90 %d %d)"`, int(x+w/2), int(y+h/2))
	}
	svg.Text(int(x+w/2), int(y+h/2), e.label, style)
}

func (e *eltPadding) render(r *eltRender) {
}

type renderEnv struct {
	gid   table.GroupID
	cache map[renderCacheKey]table.Slice
	area  [4]float64
}

type renderCacheKey struct {
	sd  *scaledData
	gid table.GroupID
}

// scaledData is a key for retrieving scaled data from a renderEnv. It
// is the result of using a binding and can be thought of as a lazy
// representation of the visually-mapped data that becomes available
// once all of the scales have been trained.
type scaledData struct {
	seqs map[table.GroupID]scaledSeq
}

type scaledSeq struct {
	seq    table.Slice
	scaler Scaler
}

func (env *renderEnv) get(sd *scaledData) table.Slice {
	cacheKey := renderCacheKey{sd, env.gid}
	if mapped := env.cache[cacheKey]; mapped != nil {
		return mapped
	}

	v := sd.seqs[env.gid]
	mapped := mapMany(v.scaler, v.seq)
	env.cache[cacheKey] = mapped
	return mapped
}

func (env *renderEnv) getFirst(sd *scaledData) interface{} {
	if mapped := env.cache[renderCacheKey{sd, env.gid}]; mapped != nil {
		mv := reflect.ValueOf(mapped)
		if mv.Len() == 0 {
			return nil
		}
		return mv.Index(0).Interface()
	}

	v := sd.seqs[env.gid]
	rv := reflect.ValueOf(v.seq)
	if rv.Len() == 0 {
		return nil
	}
	return v.scaler.Map(rv.Index(0).Interface())
}

func (env *renderEnv) Area() (x, y, w, h float64) {
	return env.area[0], env.area[1], env.area[2], env.area[3]
}

func (env *renderEnv) Size() (w, h float64) {
	return env.area[2], env.area[3]
}

func round(x float64) int {
	return int(math.Floor(x + 0.5))
}

// wrapPath wraps path data p to avoid exceeding SVG's recommended
// line length limit of 255 characters.
func wrapPath(p string) string {
	const width = 70
	if len(p) <= width {
		return p
	}
	// Chop up p until we get below the width limit.
	parts := make([]string, 0, 16)
	for len(p) > width {
		// Find the last command or space before exceeding width.
		lastCmd, lastSpace := 0, 0
		for i, ch := range p {
			if i >= width && (lastCmd != 0 || lastSpace != 0) {
				break
			}
			if 'a' <= ch && ch <= 'z' || 'A' <= ch && ch <= 'Z' {
				lastCmd = i
			} else if ch == ' ' {
				lastSpace = i
			}
		}
		split := len(p)
		// Prefer splitting at commands, but take spaces in
		// case it's a huge command.
		if lastCmd != 0 {
			split = lastCmd
		} else if lastSpace != 0 {
			split = lastSpace
		}
		parts, p = append(parts, p[:split]), p[split:]
	}
	if len(p) > 0 {
		parts = append(parts, p)
	}
	return strings.Join(parts, "\n")
}
