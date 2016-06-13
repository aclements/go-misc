// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ggstat

import "github.com/aclements/go-gg/table"

// XXX Maybe these should all be structs that satisfy the same basic
// interface{F(table.Grouping) table.Grouping}. Then optional
// arguments are easy and gg.Plot could have a Stat method that
// applies a ggstat (what would it do with the bindings?). E.g., it
// would be nice if you could just say
// plot.Stat(ggstat.ECDF{}).Add(gglayer.Steps{}).

// XXX If this is just based on the number of bins, it can come up
// with really ugly boundary numbers. If the bin width is specified,
// then you could also specify the left edge and bins will be placed
// at [align+width*N, align+width*(N+1)]. ggplot2 also lets you
// specify the center alignment.
//
// XXX In Matlab and NumPy, bins are open on the right *except* for
// the last bin, which is closed on both.
//
// XXX Number of bins/bin width/specify boundaries, same bins across
// all groups/separate for each group/based on shared scales (don't
// have that information here), relative or absolute histogram (Matlab
// has lots more).
//
// XXX Scale transform.
func Bin(g table.Grouping, xcol, wcol string) table.Grouping {
	return nil
}

// TODO: Count for categorical data.
