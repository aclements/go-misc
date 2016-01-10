// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// HBSC is a HBGenerator that implements sequential consistency.
type HBSC struct{}

func (HBSC) HappensBefore(p *Prog, i, j PC) bool {
	return true
}

func (HBSC) String() string {
	return "SC"
}
