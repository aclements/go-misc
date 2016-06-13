// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package table

import (
	"bytes"
	"os"
	"testing"
)

func groupString(g Grouping) string {
	var b bytes.Buffer
	Fprint(&b, g, "%#v", "%#v", "%#v", "%#v")
	return b.String()
}

func ExampleFprint() {
	tab := new(Builder).
		Add("name", []string{"Washington", "Adams", "Jefferson"}).
		Add("terms", []int{2, 1, 2}).
		Done()
	Fprint(os.Stdout, tab)
	// Output:
	// name        terms
	// Washington      2
	// Adams           1
	// Jefferson       2
}

func ExampleFprint_Formats() {
	tab := new(Builder).
		Add("name", []string{"Washington", "Adams", "Jefferson"}).
		Add("terms", []int{2, 1, 2}).
		Done()
	Fprint(os.Stdout, tab, "President %s", "%#x")
	// Output:
	// name                  terms
	// President Washington    0x2
	// President Adams         0x1
	// President Jefferson     0x2
}

func ExampleFprint_Groups() {
	tab := new(Builder).
		Add("name", []string{"Washington", "Adams", "Jefferson"}).
		Add("terms", []int{2, 1, 2}).
		Add("state", []string{"Virginia", "Massachusetts", "Virginia"}).
		Done()
	g := GroupBy(tab, "state")
	Fprint(os.Stdout, g)
	// Output:
	// name        terms  state
	// -- /Virginia
	// Washington      2  Virginia
	// Jefferson       2  Virginia
	// -- /Massachusetts
	// Adams           1  Massachusetts
}

func TestFprintEmpty(t *testing.T) {
	var b bytes.Buffer
	Fprint(&b, new(Table))
	if b.String() != "" {
		t.Fatalf("want %q; got %q", "", b.String())
	}
}
