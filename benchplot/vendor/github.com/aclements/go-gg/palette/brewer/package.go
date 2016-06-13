// Copyright (c) 2002 Cynthia Brewer, Mark Harrower, and The
// Pennsylvania State University.
// Please see license at http://colorbrewer.org/export/LICENSE.txt.

// Package brewer provides color specifications and designs developed
// by Cynthia Brewer (http://colorbrewer.org/).
//
// Please see license at http://colorbrewer.org/export/LICENSE.txt.
//
// This package provides three different types of color palettes.
// Sequential palettes are for ordered data that progresses from low
// to high. Diverging palettes are like sequential palettes, but have
// a defined middle and two extremes. Finally, qualitative palettes
// are for unordered or nominal data. See "Brewer, Cynthia A. 1994.
// Color use guidelines for mapping and visualization. Chapter 7 (pp.
// 123–147) in Visualization in Modern Cartography" for more details.
//
// All palettes provided by this package are discrete, but each comes
// in several variants with different numbers of discrete levels.
// These variants are named <palette>_<n> where n is the number of
// levels.
//
// Each palette also provides a variable named <palette> that is a map
// from the number of levels to the specific variants.
//
// Finally, the global ByName map from string name to palette.
package brewer

//go:generate go run genbrewer.go colorbrewer.json
