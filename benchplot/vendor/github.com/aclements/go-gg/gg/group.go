// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gg

import "github.com/aclements/go-gg/table"

// TODO: GroupByKey? Would the key function only work on one binding?
// With a first-class row representation we could pass that.

// GroupBy sub-divides all groups such that all of the rows in each
// group have equal values for all of the named columns.
func (p *Plot) GroupBy(cols ...string) *Plot {
	// TODO: Should this accept column expressions, like layers?
	return p.SetData(table.GroupBy(p.Data(), cols...))
}

// GroupAuto groups p's data table on all columns that are comparable
// but are not numeric (that is, all categorical columns).
//
// TODO: Maybe there should be a CategoricalBindings that returns the
// set of categorical bindings, which callers could just pass to
// GroupBy, possibly after manipulating.
//
// TODO: Does implementing sort.Interface make an otherwise cardinal
// column ordinal?
func (p *Plot) GroupAuto() *Plot {
	// Find the categorical columns.
	categorical := []string{}
	g := p.Data()
	for _, col := range g.Columns() {
		et := table.ColType(g, col).Elem()
		if et.Comparable() && !isCardinal(et.Kind()) {
			categorical = append(categorical, col)
		}
	}

	return p.GroupBy(categorical...)
}
