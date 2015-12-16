// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

type stringList []string

func (x *stringList) String() string {
	s := ""
	for i, s1 := range *x {
		if i != 0 {
			s += ","
		}
		s += s1
	}
	return s
}

func (x *stringList) Set(s string) error {
	*x = append(*x, s)
	return nil
}
