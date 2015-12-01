// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"fmt"
	"io"
	"reflect"
	"strings"
)

type Array interface{}

type Table struct {
	ColNames []string
	Cols     map[string]Array
}

func NewTable() *Table {
	return &Table{ColNames: []string{}, Cols: make(map[string]Array)}
}

func (t *Table) AddColumn(name string, data Array) {
	if t.Cols[name] != nil {
		panic("table already has a column named " + name)
	}
	if reflect.ValueOf(data).Kind() != reflect.Slice {
		panic("column must be a slice")
	}
	t.ColNames = append(t.ColNames, name)
	t.Cols[name] = data
}

func (t *Table) Len() int {
	minLen, haveLen := 0, false
	for _, arr := range t.Cols {
		l := reflect.ValueOf(arr).Len()
		if !haveLen || l < minLen {
			minLen = l
		}
	}
	return minLen
}

func (t *Table) ToRows(withHeader bool) [][]interface{} {
	outRows, outCols := t.Len(), len(t.ColNames)
	if withHeader {
		outRows++
	}
	out := make([][]interface{}, outRows)
	for i := 0; i < len(out); i++ {
		out[i] = make([]interface{}, outCols)
	}

	for col, name := range t.ColNames {
		outRow := 0
		if withHeader {
			out[0][col] = name
			outRow++
		}
		arr := reflect.ValueOf(t.Cols[name])
		for i := 0; outRow < outRows; i, outRow = i+1, outRow+1 {
			out[outRow][col] = arr.Index(i).Interface()
		}
	}
	return out
}

func (t *Table) WriteTSV(w io.Writer, withHeader bool) (err error) {
	buf := bufio.NewWriter(w)
	defer func() {
		err = buf.Flush()
	}()

	// Write header.
	if withHeader {
		fmt.Fprintf(buf, "%s\n", strings.Join(t.ColNames, "\t"))
	}

	// Write body.
	rows := t.Len()
	if rows == 0 {
		return
	}
	vs := make([]reflect.Value, len(t.Cols))
	for i, name := range t.ColNames {
		vs[i] = reflect.ValueOf(t.Cols[name])
	}
	for i := 0; i < rows; i++ {
		for j, v := range vs {
			if j > 0 {
				buf.WriteString("\t")
			}
			fmt.Fprint(buf, v.Index(i))
		}
		buf.WriteString("\n")
	}
	return
}
