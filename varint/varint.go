// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package varint

const maxVarintBytes = 10

// EncodeVarint and DecodeVarint from https://github.com/golang/protobuf

func EncodeVarint(x uint64) []byte {
	var buf [maxVarintBytes]byte
	var n int
	for n = 0; x > 127; n++ {
		buf[n] = 0x80 | uint8(x&0x7F)
		x >>= 7
	}
	buf[n] = uint8(x)
	n++
	return buf[0:n]
}

func DecodeVarint(buf []byte) (x uint64, n int) {
	// x, n already 0
	for shift := uint(0); shift < 64; shift += 7 {
		if n >= len(buf) {
			return 0, 0
		}
		b := uint64(buf[n])
		n++
		x |= (b & 0x7F) << shift
		if (b & 0x80) == 0 {
			return x, n
		}
	}

	// The number is too large to represent in a 64-bit value.
	return 0, 0
}

func queryBMI2() bool

var hasBMI2 = queryBMI2()

func decodeVarintAsmLoop(buf []byte) (x uint64, n int)
func decodeVarintAsmBMI2(buf []byte) (x uint64, n int)
func decodeVarintAsm1(buf []byte) (x uint64, n int)
func decodeVarintAsm2(buf []byte) (x uint64, n int)
