// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package slice

import (
	"fmt"
	"reflect"
	"regexp"
	"testing"
)

func de(x, y interface{}) bool {
	return reflect.DeepEqual(x, y)
}

func shouldPanic(t *testing.T, re string, f func()) {
	r := regexp.MustCompile(re)
	defer func() {
		err := recover()
		if err == nil {
			t.Fatalf("want panic matching %q; got no panic", re)
		} else if !r.MatchString(fmt.Sprintf("%s", err)) {
			t.Fatalf("want panic matching %q; got %s", re, err)
		}
	}()
	f()
}
