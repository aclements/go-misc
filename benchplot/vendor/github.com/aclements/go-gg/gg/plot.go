// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gg

import (
	"fmt"
	"log"
	"os"

	"github.com/aclements/go-gg/table"
)

// TODO: Split transforms, scalers, and layers into their own packages
// to clean up the name spaces and un-prefix their names?

// Warning is a logger for reporting conditions that don't prevent the
// production of a plot, but may lead to unexpected results.
var Warning = log.New(os.Stderr, "[gg] ", log.Lshortfile)

// Plot represents a single (potentially faceted) plot.
type Plot struct {
	env    *plotEnv
	scales map[string]scalerTree

	scaledData map[scaledDataKey]*scaledData
	scaleSet   map[scaleKey]bool
	marks      []plotMark

	axisLabels     map[string]string
	autoAxisLabels map[string][]string

	title string

	constNonce int
}

// NewPlot returns a new Plot backed by data. It has no layers, one
// facet, and all scales are default.
func NewPlot(data table.Grouping) *Plot {
	p := &Plot{
		env: &plotEnv{
			data: data,
		},
		scales:         make(map[string]scalerTree),
		scaledData:     make(map[scaledDataKey]*scaledData),
		scaleSet:       make(map[scaleKey]bool),
		axisLabels:     make(map[string]string),
		autoAxisLabels: make(map[string][]string),
	}
	return p
}

type plotEnv struct {
	parent *plotEnv
	data   table.Grouping
}

type scaleKey struct {
	gid   table.GroupID
	aes   string
	scale Scaler
}

// SetData sets p's current data table. The caller must not modify
// data in this table after this point.
func (p *Plot) SetData(data table.Grouping) *Plot {
	p.env.data = data
	return p
}

// Data returns p's current data table.
func (p *Plot) Data() table.Grouping {
	return p.env.data
}

// Const creates a new constant column bound to val in all groups and
// returns the generated column name. This is a convenient way to pass
// constant values to layers as columns.
//
// TODO: Typically this should be used with PreScaled or physical types.
func (p *Plot) Const(val interface{}) string {
	tab := p.Data()

retry:
	col := fmt.Sprintf("[gg-const-%d]", p.constNonce)
	p.constNonce++
	for _, col2 := range tab.Columns() {
		if col == col2 {
			goto retry
		}
	}

	p.SetData(table.MapTables(tab, func(_ table.GroupID, t *table.Table) *table.Table {
		return table.NewBuilder(t).AddConst(col, val).Done()
	}))

	return col
}

type scalerTree struct {
	scales map[table.GroupID]Scaler
}

func newScalerTree() scalerTree {
	return scalerTree{map[table.GroupID]Scaler{
		table.RootGroupID: &defaultScale{},
	}}
}

func (t scalerTree) bind(gid table.GroupID, s Scaler) {
	// Unbind scales under gid.
	for ogid := range t.scales {
		if gid == table.RootGroupID {
			// Optimize binding the root GID.
			delete(t.scales, ogid)
			continue
		}

		for p := ogid; ; p = p.Parent() {
			if p == gid {
				delete(t.scales, ogid)
				break
			}
			if p == table.RootGroupID {
				break
			}
		}
	}
	t.scales[gid] = s
}

func (t scalerTree) find(gid table.GroupID) Scaler {
	for {
		if s, ok := t.scales[gid]; ok {
			return s
		}
		if gid == table.RootGroupID {
			// This should never happen.
			panic("no scale for group " + gid.String())
		}
		gid = gid.Parent()
	}
}

func (p *Plot) getScales(aes string) scalerTree {
	st, ok := p.scales[aes]
	if !ok {
		st = newScalerTree()
		p.scales[aes] = st
	}
	return st
}

// SetScale binds a scale to the given visual aesthetic. SetScale is
// shorthand for SetScaleAt(aes, s, table.RootGroupID).
//
// SetScale returns p for ease of chaining.
func (p *Plot) SetScale(aes string, s Scaler) *Plot {
	return p.SetScaleAt(aes, s, table.RootGroupID)
}

// SetScaleAt binds a scale to the given visual aesthetic for all data
// in group gid or descendants of gid.
func (p *Plot) SetScaleAt(aes string, s Scaler, gid table.GroupID) *Plot {
	// TODO: Should aes be an enum so you can't mix up aesthetics
	// and column names?
	p.getScales(aes).bind(gid, s)
	return p
}

// GetScale returns the scale for the given visual aesthetic used for
// data in the root group.
func (p *Plot) GetScale(aes string) Scaler {
	return p.GetScaleAt(aes, table.RootGroupID)
}

// GetScaleAt returns the scale for the given visual aesthetic used
// for data in group gid.
func (p *Plot) GetScaleAt(aes string, gid table.GroupID) Scaler {
	return p.getScales(aes).find(gid)
}

type scaledDataKey struct {
	aes  string
	data table.Grouping
	col  string
}

// use binds a column of data to an aesthetic. It expands the domain
// of the aesthetic's scale to include the data in col, and returns
// the scaled data.
//
// col may be "", in which case it simply returns nil.
//
// TODO: Should aes be an enum?
func (p *Plot) use(aes string, col string) *scaledData {
	if col == "" {
		return nil
	}

	// TODO: This is wrong. If the scale tree for aes changes,
	// this may return a stale scaledData bound to the wrong
	// scalers. If I got rid of scale trees, I could just put the
	// scaler in the key. Or I could clean up the cache when the
	// scale tree changes.

	sd := p.scaledData[scaledDataKey{aes, p.Data(), col}]
	if sd == nil {
		// Construct the scaledData.
		sd = &scaledData{
			seqs: make(map[table.GroupID]scaledSeq),
		}

		// Get the scale tree.
		st := p.getScales(aes)

		for _, gid := range p.Data().Tables() {
			table := p.Data().Table(gid)

			// Get the data.
			seq := table.MustColumn(col)

			// Find the scale.
			scaler := st.find(gid)

			// Add the scale to the scale set.
			p.scaleSet[scaleKey{gid, aes, scaler}] = true

			// Train the scale.
			if _, ok := seq.([]Unscaled); !ok {
				scaler.ExpandDomain(seq)
			}

			// Add it to the scaledData.
			sd.seqs[gid] = scaledSeq{seq, scaler}
		}

		p.scaledData[scaledDataKey{aes, p.Data(), col}] = sd
	}

	// Update axis labels.
	if aes == "x" || aes == "y" {
		p.autoAxisLabels[aes] = append(p.autoAxisLabels[aes], col)
	}

	return sd
}

// Save saves the current data table of p to a stack.
func (p *Plot) Save() *Plot {
	p.env = &plotEnv{
		parent: p.env,
		data:   p.env.data,
	}
	return p
}

// Restore restores the data table of p from the save stack.
func (p *Plot) Restore() *Plot {
	if p.env.parent == nil {
		panic("unbalanced Save/Restore")
	}
	p.env = p.env.parent
	return p
}

// A Plotter is an operation that can modify a Plot.
type Plotter interface {
	Apply(*Plot)
}

// Add applies each of plotters to Plot in order.
func (p *Plot) Add(plotters ...Plotter) *Plot {
	for _, plotter := range plotters {
		plotter.Apply(p)
	}
	return p
}

// AxisLabel returns a Plotter that sets the label of an axis on a
// Plot. By default, Plot constructs automatic axis labels from column
// names, but AxisLabel lets callers override these.
//
// TODO: Should labels be attached to aesthetics, generally?
//
// TODO: Should this really be a Plotter or just a method of Plot?
func AxisLabel(axis, label string) Plotter {
	return axisLabel{axis, label}
}

type axisLabel struct {
	axis, label string
}

func (a axisLabel) Apply(p *Plot) {
	p.axisLabels[a.axis] = a.label
}

// Title returns a Plotter that sets the title of a Plot.
func Title(label string) Plotter {
	return titlePlotter{label}
}

type titlePlotter struct {
	label string
}

func (t titlePlotter) Apply(p *Plot) {
	p.title = t.label
}

// A Stat transforms a table.Grouping.
type Stat interface {
	F(table.Grouping) table.Grouping
}

// Stat applies each of stats in order to p's data.
//
// TODO: Perform scale transforms before applying stats.
func (p *Plot) Stat(stats ...Stat) *Plot {
	data := p.Data()
	for _, stat := range stats {
		data = stat.F(data)
	}
	return p.SetData(data)
}
