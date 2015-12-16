// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import "regexp"

type regexpList []*regexp.Regexp

func (x *regexpList) String() string {
	s := ""
	for i, r := range *x {
		if i != 0 {
			s += ","
		}
		s += r.String()
	}
	return s
}

func (x *regexpList) Set(s string) error {
	re, err := regexp.Compile("(?m)" + s)
	if err != nil {
		// Get an error without our modifications.
		_, err2 := regexp.Compile(s)
		if err2 != nil {
			err = err2
		}
		return err
	}
	*x = append(*x, re)
	return nil
}
