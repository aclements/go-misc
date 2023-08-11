// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"encoding/binary"
	"fmt"
	"reflect"
)

type Decoder struct {
	Order   binary.ByteOrder
	IntSize int // 4 or 8
	PtrSize int // 4 or 8
}

func (d *Decoder) Read(data []byte, out any) (int, error) {
	rv := reflect.ValueOf(out)
	switch rv.Kind() {
	case reflect.Pointer:
		return d.read1(data, rv.Elem())
	case reflect.Slice:
		pos := 0
		n := rv.Len()
		for i := 0; i < n; i++ {
			len, err := d.read1(data[pos:], rv.Index(i))
			if err != nil {
				return pos, err
			}
			pos += len
		}
		return pos, nil
	default:
		return 0, fmt.Errorf("out must be a pointer, got %T", out)
	}
}

func (d *Decoder) read1(data []byte, out reflect.Value) (int, error) {
	kind := out.Kind()
	var size int

	switch kind {
	case reflect.Struct:
		pos := 0
		nf := out.NumField()
		for i := 0; i < nf; i++ {
			len, err := d.read1(data[pos:], out.Field(i))
			if err != nil {
				return pos, err
			}
			pos += len
		}
		return pos, nil

	case reflect.Array:
		pos := 0
		n := out.Len()
		for i := 0; i < n; i++ {
			len, err := d.read1(data[pos:], out.Index(i))
			if err != nil {
				return pos, err
			}
			pos += len
		}
		return pos, nil

	// Flatten kinds.
	case reflect.Uintptr:
		switch d.PtrSize {
		case 4:
			kind = reflect.Uint32
		case 8:
			kind = reflect.Uint64
		default:
			return 0, fmt.Errorf("cannot decode into uintptr before PtrSize is set")
		}
	case reflect.Int:
		switch d.IntSize {
		case 4:
			kind = reflect.Int32
		case 8:
			kind = reflect.Int64
		default:
			return 0, fmt.Errorf("cannot decode into int before IntSize is set")
		}
	case reflect.Uint:
		switch d.IntSize {
		case 4:
			kind = reflect.Uint32
		case 8:
			kind = reflect.Uint64
		default:
			return 0, fmt.Errorf("cannot decode into uint before IntSize is set")
		}
	}

	// Decode basic types
	switch kind {
	default:
		return 0, fmt.Errorf("unimplemented kind %s", kind)
	case reflect.Uint8:
		size = 1
		out.SetUint(uint64(data[0]))
	case reflect.Uint16:
		size = 2
		out.SetUint(uint64(d.Order.Uint16(data)))
	case reflect.Uint32:
		size = 4
		out.SetUint(uint64(d.Order.Uint32(data)))
	case reflect.Uint64:
		size = 8
		out.SetUint(uint64(d.Order.Uint64(data)))
	case reflect.Int8:
		size = 1
		out.SetInt(int64(data[0]))
	case reflect.Int16:
		size = 2
		out.SetInt(int64(d.Order.Uint16(data)))
	case reflect.Int32:
		size = 4
		out.SetInt(int64(d.Order.Uint32(data)))
	case reflect.Int64:
		size = 8
		out.SetInt(int64(d.Order.Uint64(data)))
	}
	return size, nil
}
