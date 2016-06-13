// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gg creates plots using the Grammar of Graphics.
//
// WARNING: This API is highly unstable. For now, please vendor this
// package.
//
// gg creates statistical visualizations. It's designed to help users
// quickly navigate and explore complex data in different ways, both
// in terms of what they're plotting and how they're plotting it. This
// focus on rapid exploration of complex data leads to a very
// different design than typical plotting packages.
//
// gg is heavily inspired by Wilkinson's Grammar of Graphics [1]. A
// key observation of the Grammar of Graphics is that there are many
// motifs across different types of plots. The Grammar of Graphics
// separates these motifs into orthogonal concerns that can be
// manipulated and extended independently, enabling the creation of
// traditional plot types from their fundamental components as well as
// the creation of entirely new plot types.
//
// Data model
//
// Central to gg is its data model. At the most basic level, the input
// data consists of a table with a set of named columns, with the rows
// organized into one or more groups. At a higher level, because gg
// makes it easy to restructure data before plotting it, it expects to
// start with regularized input data, where each column represents a
// distinct independent or dependent variable. In other words, any two
// values that make sense to plot on the same axis should be in the
// same column.
//
// For example, to express a line graph with several series of
// different colors in gg, you would say "plot column A against column
// B, grouped into series and colored according to column C". In
// contrast, typical plotting packages use a "spreadsheet" model,
// where each data series is a separate column, so expressing the same
// graph requires saying "plot column A against column B in color 1
// and plot column A against column C in color 2" and so on.
//
// gg's approach is suited to exploratory data analysis because you
// don't have to restructure the data to see it in a different way. In
// the traditional spreadsheet model, you have to structure the data
// to match the plot. In gg, you tell the plot what structure to
// extract from the data.
//
// Layers and scales
//
// To visualize data, gg provides a set of composable plot building
// blocks. There are no fixed "plot types" in gg. The main building
// block is a "layer", which transforms a data set into a set of
// visual marks, such as lines, points, or rectangles. Each layer is
// configured by mapping columns of the data set to different
// "aesthetics". An aesthetic is a generalization of a dimension: X
// and Y are aesthetics, but so are color and stroke width and point
// shape. Unlike typical plotting packages, these various aesthetics
// are treated symmetrically and any aesthetic can be fed from any
// column of the data.
//
// Layers work in close concert with "scales", which map from values
// in the data space to values in the visual space. Scales can map
// from continuous or discrete data values (such as numbers or
// strings) to continuous or discrete visual values (such as pixel
// offsets or point shapes). Each aesthetic has an associated scale.
// If the user hasn't provided a specific scale for an aesthetic, gg
// uses a default scale that guesses what to do based on the data type
// and aesthetic.
//
// Stats
//
// Data can be pre-processed prior to rendering it with a layer using
// a "stat". A stat can be an arbitrary data transformation, but it's
// typically used to compute statistical summaries, such as the
// five-number summary (e.g., for a box plot), a linear regression, or
// a density estimate.
//
// TODO: "Compound" layers?
//
// Facets
//
// TODO.
//
// Aesthetics
//
// gg understands the following aesthetics.
//
// "x" and "y" give the offset from the lower-left corner of a plot.
// Their ranges are always set to the pixel coordinates of the X and Y
// axes, respectively, and cannot be overridden.
//
// "stroke" and "fill" give the stroke and fill colors of paths and
// points. Their ranger must have type color.Color. The default ranger
// returns a single-hue gradient for continuous data, or a categorical
// palette for discrete data.
//
// "opacity" gives the overall opacity of a mark. Its ranger must have
// type float64 and give values between 0 and 1, inclusive. The
// default ranger ranges from 10% opaque (0.1) to fully opaque (1.0).
//
// "size" gives the size of marks. Its ranger must have type float64
// and yields values that are relative to the smallest dimension of
// the plot area (e.g., a value of 0.5 creates a point that cover half
// of the plot width or height, whichever is smaller). The default
// ranger ranges from 1% (0.01) to 10% (0.1).
//
// Related work
//
// gg draws ideas and inspiration from many sources. The core
// principle of a Grammar of Graphics was introduced by Wiklinson [1].
// There have been many implementations in many languages. The most
// popular is certainly Wickham's ggplot2 for R [2]. gg draws most
// heavily on Wickham's follow-up work on ggvis for R [3].
//
// [1] Leland Wilkinson, The Grammar of Graphics, Springer, 1999.
//
// [2] Hadley Wickham, ggplot2: Elegant Graphics for Data Analysis,
// Springer, 2009.
//
// [3] Hadley Wickham, ggvis, http://ggvis.rstudio.com/.
//
// TODO: Scale transforms, coordinate spaces.
package gg
