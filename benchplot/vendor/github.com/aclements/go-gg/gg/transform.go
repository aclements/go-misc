// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gg

import "github.com/aclements/go-gg/table"

// SortBy sorts each group by the named columns. If a column's type
// implements sort.Interface, rows will be sorted according to that
// order. Otherwise, the values in the column must be naturally
// ordered (their types must be orderable by the Go specification). If
// neither is true, SortBy panics with a *generic.TypeError. If more
// than one column is given, SortBy sorts by the tuple of the columns;
// that is, if two values in the first column are equal, they are
// sorted by the second column, and so on.
func (p *Plot) SortBy(cols ...string) *Plot {
	return p.SetData(table.SortBy(p.Data(), cols...))
}
