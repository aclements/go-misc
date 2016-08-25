// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package varint

import (
	"fmt"
	"math/rand"
	"testing"
)

func TestDecodeVarintAsm(t *testing.T) {
	type fn struct {
		name string
		f    func([]byte) (uint64, int)
	}
	for _, f := range []fn{
		{"decodeVarintAsmLoop", decodeVarintAsmLoop},
		{"decodeVarintAsmBMI2", decodeVarintAsmBMI2},
		{"decodeVarintAsm1", decodeVarintAsm1},
		{"decodeVarintAsm2", decodeVarintAsm2},
	} {
		for _, bmi2 := range []bool{false, true} {
			for _, pad := range []bool{false, true} {
				name := fmt.Sprintf("f:%s/bmi2:%v/pad:%v", f.name, bmi2, pad)
				t.Run(name, func(t *testing.T) {
					testDecodeVarintAsm(t, f.f, bmi2, pad)
				})
			}
		}
	}
}

func testDecodeVarintAsm(t *testing.T, f func([]byte) (uint64, int), bmi2, pad bool) {
	if bmi2 && !hasBMI2 {
		t.Skip("BMI2 not supported on this CPU")
	}

	oldHasBMI2 := hasBMI2
	defer func() { hasBMI2 = oldHasBMI2 }()
	hasBMI2 = bmi2

	padding := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}
	for x1 := uint(0); x1 < 64; x1++ {
		for x2 := uint(0); x2 < x1; x2++ {
			var v uint64 = (1 << x1) | (1 << x2)
			buf := EncodeVarint(v)
			vlen := len(buf)
			if pad {
				buf = append(buf, padding...)
			}
			x, n := f(buf)
			if x != v || n != vlen {
				t.Errorf("decode(encode(%#x)) = %#x, %d; want %#x, %d %x", v, x, n, v, vlen, buf)
			}
		}
	}
}

var testBuf []byte

var testLengths [11][]byte

func init() {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 1000; i++ {
		val := uint64(r.Uint32())
		testBuf = append(testBuf, EncodeVarint(val)...)
	}

	for length := 1; length <= 10; length++ {
		encoded := EncodeVarint(1 << uint(7*(length-1)))
		if len(encoded) != length {
			panic("unexpected encoded length")
		}
		for i := 0; i < 1000; i++ {
			testLengths[length] = append(testLengths[length], encoded...)
		}
	}
}

func BenchmarkDecodeVarint(b *testing.B) {
	for i := 0; i < b.N; i++ {
		buf := testBuf
		for len(buf) > 0 {
			_, n := DecodeVarint(buf)
			buf = buf[n:]
		}
	}
}

func BenchmarkDecodeVarintN(b *testing.B) {
	for length := 1; length < len(testLengths); length++ {
		name := fmt.Sprintf("bytes:%d", length)
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				buf := testLengths[length]
				for len(buf) > 0 {
					_, n := DecodeVarint(buf)
					buf = buf[n:]
				}
			}
		})
	}
}

func BenchmarkDecodeVarintAsmLoop(b *testing.B) {
	for i := 0; i < b.N; i++ {
		buf := testBuf
		for len(buf) > 0 {
			_, n := decodeVarintAsmLoop(buf)
			buf = buf[n:]
		}
	}
}

func BenchmarkDecodeVarintAsmLoopN(b *testing.B) {
	for length := 1; length < len(testLengths); length++ {
		name := fmt.Sprintf("bytes:%d", length)
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				buf := testLengths[length]
				for len(buf) > 0 {
					_, n := decodeVarintAsmLoop(buf)
					buf = buf[n:]
				}
			}
		})
	}
}

func BenchmarkDecodeVarintAsmBMI2(b *testing.B) {
	for i := 0; i < b.N; i++ {
		buf := testBuf
		for len(buf) > 0 {
			_, n := decodeVarintAsmBMI2(buf)
			buf = buf[n:]
		}
	}
}

func BenchmarkDecodeVarintAsmBMI2N(b *testing.B) {
	for length := 1; length < len(testLengths); length++ {
		name := fmt.Sprintf("bytes:%d", length)
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				buf := testLengths[length]
				for len(buf) > 0 {
					_, n := decodeVarintAsmBMI2(buf)
					buf = buf[n:]
				}
			}
		})
	}
}

func BenchmarkDecodeVarintAsm1(b *testing.B) {
	for i := 0; i < b.N; i++ {
		buf := testBuf
		for len(buf) > 0 {
			_, n := decodeVarintAsm1(buf)
			buf = buf[n:]
		}
	}
}

func BenchmarkDecodeVarintAsm2(b *testing.B) {
	for i := 0; i < b.N; i++ {
		buf := testBuf
		for len(buf) > 0 {
			_, n := decodeVarintAsm2(buf)
			buf = buf[n:]
		}
	}
}
