// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package dashquery

import "testing"

func TestEval(t *testing.T) {
	try := func(expr string, want bool) {
		t.Helper()
		q, err := Compile(expr)
		if err != nil {
			t.Errorf("%s: unexpected compile error %s", expr, err)
			return
		}
		if have := q.fn(pathInfo{}); have != want {
			t.Errorf("%s: want %v, have %v", expr, want, have)
		}
	}

	try(`true`, true)
	try(`false`, false)
	try(`1 == 1`, true)
	try(`1 == 2`, false)
	try(`"a" == "a"`, true)
	try(`"a" == "b"`, false)
	try(`1+1 == 2`, true)
	try(`"a"+"b" == "ab"`, true)
	try(`1-1 == 0`, true)
	try(`1==1 && 2==2`, true)
	try(`1==1 && 2==3`, false)
	try(`1==1 || 2==2`, true)
	try(`1==2 || 1==2`, false)
	try(`1 < 2`, true)
	try(`1 > 2`, false)
	try(`(1==1) == (2==2)`, true)
	try(`(1==1) == (1==2)`, false)
	try(`-1 == 1-2`, true)
	try(`+1 == 0+1`, true)
	try(`!(1==1) == (1==2)`, true)
}
