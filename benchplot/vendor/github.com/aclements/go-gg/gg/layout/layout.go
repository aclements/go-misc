// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package layout provides helpers for laying out hierarchies of
// rectangular elements in two dimensional space.
package layout

// TODO: If I want to handle wrapped text, this API is insufficient.
// In that case, I may need something more like Android where the
// parent can pass in Unspecified, (Exactly x), or (AtMost x) for both
// dimensions and make multiple calls. I would probably start out with
// AtMost the allocated dimension for everything and if the total came
// back too large, I would cut back space (possibly causing the other
// dimension to grow if text wraps).

// An Element is a rectangular feature in a layout.
type Element interface {
	// SizeHint returns this Element's desired size and whether it
	// can expand from that size in either direction.
	SizeHint() (w, h float64, flexw, flexh bool)

	// SetLayout sets this Element's layout relative to its parent
	// and, if this Element is a container, recursively lays out
	// this Element's children.
	//
	// w and h may be smaller than SizeHint() if the space is
	// constrained. They may also be larger, even if the element
	// isn't flexible, in which case the Element will position
	// itself within the assigned size using some gravity.
	//
	// TODO: Or should the parent be responsible for gravity if it
	// allocates too much space to a fixed element?
	//
	// TODO: Since an Element doesn't know its parent, it's
	// difficult to turn local coordinates into absolute
	// coordinates. These should either be absolute coordinates,
	// or Element should have a parent and it should be easy to
	// get absolute coordinates.
	SetLayout(x, y, w, h float64)

	// Layout returns this Element's layout.
	Layout() (x, y, w, h float64)
}

// A Group is an Element that manages the layout of child Elements.
type Group interface {
	Element

	// Children returns the child Elements laid out by this Group.
	Children() []Element
}

// Leaf is a leaf in a layout hierarchy. It is meant for embedding: it
// partially implements Element, leaving SizeHint to the embedding
// type.
type Leaf struct {
	x, y, w, h float64
}

func (l *Leaf) SetLayout(x, y, w, h float64) {
	l.x, l.y, l.w, l.h = x, y, w, h
}

func (l *Leaf) Layout() (x, y, w, h float64) {
	return l.x, l.y, l.w, l.h
}
