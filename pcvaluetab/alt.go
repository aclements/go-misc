// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

// Alternate PCDATA encoding

import (
	"encoding/binary"
	"fmt"
	"log"
	"math/bits"
	"slices"
	"unsafe"
)

type indexScheme int

const (
	// All index entries are the same width, which depends on the function
	// size.
	indexFixedWidth indexScheme = iota
	// The index is encoded as a group varint.
	//
	// This saves a fair amount over indexFixedWidth, but is pretty hard to
	// decode.
	indexGroupVarint
	// The index is encoded as 1-byte, 2-byte, or 4-byte offsets. The
	// default is 1-byte, but two reserved values for the first byte
	// indicate 2- or 4-byte encoding.
	//
	// This is the clear winner on size, and is pretty easy to decode.
	indexByteOrHeader
)

const useIndex = indexByteOrHeader

type biasScheme int

const (
	// No value biasing
	biasNone biasScheme = iota
	// Bias all values by a fixed value (fixedBias). Values use signed
	// encoding, but in practice very few values are negative and we only
	// use small negative values, so this lets us encode more positive
	// values in one byte.
	biasFixed
	// Compute a per-chunk bias value and encode that in each chunk. This
	// saves a *tiny* bit, but is probably not worth the added complexity.
	biasPerChunk
	// Use the start value of a chunk as the bias for the rest of the
	// values.
	//
	// This is the pretty clear winner, and fairly easy to decode.
	biasStartValue
)

const useBias = biasStartValue

// For biasFixed, the bias to add to each value. Generally the minimum value
// is -2, so this lets us fit more values in a signed 8-bit number.
const fixedBias = -120

// linearIndex encodes tab in the alternate "linear index" format.
func linearIndex(tab *VarintPCData) []byte {
	const debug = false

	var indexVals []int32
	var pcdata []byte

	chunks := uint32((tab.TextLen + 255) >> 8)

	encodeUint16 := func(buf *[]byte, val uint64) {
		if val > 0xffff {
			panic("value too large")
		}
		*buf = append(*buf, byte(val), byte(val>>8))
	}
	encodeUint32 := func(buf *[]byte, val uint64) {
		if val > 0xffffffff {
			panic("value too large")
		}
		*buf = append(*buf, byte(val), byte(val>>8), byte(val>>16), byte(val>>24))
	}
	encodeValue := func(buf *[]byte, val int32) uint8 {
		if int32(int8(uint8(val))) == val {
			*buf = append(*buf, uint8(val))
			return 0b01
		} else if int32(int16(uint16(val))) == val {
			*buf = append(*buf, uint8(val), uint8(val>>8))
			return 0b10
		} else {
			encodeUint32(buf, uint64(uint32(val)))
			return 0b11
		}
	}
	encodeGroup := func(buf *[]byte, vals []int32) {
		// Encode group header, at two bits per value.
		bits := 2 * len(vals)
		bytes := (bits + 7) / 8
		headerOff := len(*buf)
		*buf = append(*buf, make([]uint8, bytes)...)
		for i, val := range vals {
			valLen := encodeValue(buf, val)
			(*buf)[headerOff+i/4] |= (valLen << ((i % 4) * 2))
		}
		// TODO: Also try variant where the group header is in the high bits of
		// the PCs.
	}

	// Encode each chunk
	pcIndex := 0
	// For constant-valued chunked, map from value to starting offset.
	constChunkOffs := make(map[int32]int32)
	for chunk := uint32(0); chunk < chunks; chunk++ {
		// Find range of PCs in this chunk.
		startPCIndex := pcIndex
		for pcIndex < len(tab.PCs) && tab.PCs[pcIndex]>>8 == chunk {
			pcIndex++
		}
		// Each chunk implicitly starts with PC 0, which means there's no need
		// to encode an explicit PC 0.
		if startPCIndex < pcIndex && tab.PCs[startPCIndex]&0xff == 0 {
			startPCIndex++
		}
		// Get the starting value of this chunk.
		startValue := int32(-1) // PCDATA tables start with -1.
		if startPCIndex > 0 {
			startValue = tab.Vals[startPCIndex-1]
		}
		if startPCIndex == pcIndex {
			// This is a constant chunk.
			if off, ok := constChunkOffs[startValue]; ok {
				// We can just point to this chunk from the index.
				indexVals = append(indexVals, off)
				continue
			}
			// This is a new constant chunk.
			constChunkOffs[startValue] = int32(len(pcdata))
		}

		// Add to the index.
		if chunk > 0 {
			indexVals = append(indexVals, int32(len(pcdata)))
		}

		// Encode PC count (N). Note that it's important that we never include
		// PC 0 here because that means the maximum count is 255, so it always
		// fits in a byte.
		n := pcIndex - startPCIndex
		if n < 0 {
			panic("skipped past start")
		}
		if n > 255 {
			panic("PC count > 255")
		}
		pcdata = append(pcdata, uint8(n))

		// Encode N PCs.
		for i := startPCIndex; i < pcIndex; i++ {
			pcdata = append(pcdata, uint8(tab.PCs[i]&0xff))
		}

		// Compute bias.
		var vals []int32
		var bias int32
		switch useBias {
		case biasFixed:
			bias = fixedBias
		case biasPerChunk:
			minVal, maxVal := startValue, startValue
			if startPCIndex < pcIndex {
				min2 := slices.Min(tab.Vals[startPCIndex:pcIndex])
				if min2 < minVal {
					minVal = min2
				}
				max2 := slices.Max(tab.Vals[startPCIndex:pcIndex])
				if max2 > maxVal {
					maxVal = max2
				}
			}
			if int32(int8(minVal)) == minVal && int32(int8(maxVal)) == maxVal {
				// No need to bias. We need a way to encode this in the bits.
			} else {
				// Shift the minimum value to -127. min + bias = -127.
				bias = -127 - minVal
				vals = append(vals, bias) // TODO: Could be unsigned.
			}
		case biasStartValue:
			bias = -startValue
		}

		// Encode values, beginning with the value in effect at the start of
		// this chunk's PC range.
		if useBias == biasStartValue {
			vals = append(vals, startValue)
		} else {
			vals = append(vals, startValue+bias)
		}
		for i := startPCIndex; i < pcIndex; i++ {
			vals = append(vals, tab.Vals[i]+bias)
		}

		encodeGroup(&pcdata, vals)
	}
	if pcIndex != len(tab.PCs) {
		log.Fatalf("didn't consume all PCs, pcIndex=%d, len(pcs)=%d", pcIndex, len(tab.PCs))
	}

	// Encode index.
	var index []byte
	switch useIndex {
	case indexFixedWidth:
		if chunks < 32 {
			// Two bytes per entry is enough.
			for _, val := range indexVals {
				index = append(index, byte(val), byte(val>>8))
			}
		} else {
			// Four bytes per entry.
			for _, val := range indexVals {
				encodeUint32(&index, uint64(val))
			}
		}
	case indexGroupVarint:
		// To start with, the offsets are relative to the end of the index, but
		// consumers don't know the length of the index, so we really want them
		// to be relative to the start of the index. But we don't know the
		// length of the index. So reach a fixed point. In practice this almost
		// never requires more than two iterations.
		prevLen := 0
		for {
			index = index[:0]
			encodeGroup(&index, indexVals)
			if len(index) <= prevLen {
				break
			}
			for i := range indexVals {
				indexVals[i] += int32(len(index) - prevLen)
			}
			prevLen = len(index)
		}
	case indexByteOrHeader:
		size := 1
		for i, val := range indexVals {
			if val > 0xffff {
				size = 4
				break
			} else if val > 0xff || (i == 0 && val >= 0xfe) {
				size = 2
			}
		}
		if size == 1 {
			// Everything fits in a byte and the first byte's value isn't a
			// special marker.
			for _, val := range indexVals {
				index = append(index, uint8(val))
			}
		} else if size == 2 {
			// Put an 0xfe marker, followed by 2-byte offsets.
			index = append(index, 0xfe)
			for _, val := range indexVals {
				encodeUint16(&index, uint64(val))
			}
		} else if size == 4 {
			// Put an 0xff marker, followed by 4-byte offsets.
			index = append(index, 0xff)
			for _, val := range indexVals {
				encodeUint32(&index, uint64(val))
			}
		} else {
			panic("bad size")
		}
	}

	if debug {
		fmt.Println("const chunks: ", constChunkOffs)
		fmt.Println("index vals: ", indexVals)
	}

	// Combine index and values.
	pcdata = append(index, pcdata...)

	return pcdata
}

// lookupLinearIndex performs a point query for the value associated with pc in
// PCDATA encoded with in the linear index format.
func lookupLinearIndex(data []byte, textLen, pc uint32) int32 {
	const debug = false

	chunks := uint32((textLen + 255) >> 8)

	if debug {
		fmt.Println("lookup", pc)
		fmt.Println("chunks:", chunks)
	}

	// Lookup the chunk in data
	var chunk []byte
	switch useIndex {
	default:
		panic("index scheme not implemented")
	case indexByteOrHeader:
		// Compute the offset of the chunk from data.
		if chunks == 1 {
			// In this case it's not safe to look at the header byte. We could
			// say "|| chunks==1" in the 1-byte encoding case, but this is so
			// common is seems worth a fast path anyway.
			chunk = data
			break
		}
		var chunkOff uint32
		chunkID := pc >> 8
		if data[0] < 0xfe {
			// 1-byte encoding
			chunkOff = chunks - 1 // Skip index
			if chunkID > 0 {
				chunkOff += uint32(data[chunkID-1])
			}
		} else if data[0] == 0xfe {
			// 2-byte encoding
			chunkOff = 1 + (chunks-1)*2
			if chunkID > 0 {
				chunkOff += uint32(binary.LittleEndian.Uint16(data[1+(chunkID-1)*2:]))
			}
		} else {
			// 4-byte encoding
			chunkOff = 1 + (chunks-1)*4
			if chunkID > 0 {
				chunkOff += binary.LittleEndian.Uint32(data[1+(chunkID-1)*4:])
			}
		}
		if debug {
			fmt.Println("chunk offset:", chunkOff)
		}
		chunk = data[chunkOff:]
	}

	// Separate the chunk fields: N byte, PCs [N]byte, lens [N+1]2bit, vals [N+1]varlen
	n := chunk[0]
	groupBits := 2 * int(n+1)
	groupBytes := (groupBits + 7) / 8
	lens := chunk[1+n:]
	vals := lens[groupBytes:]
	pcs := chunk[1 : 1+n]
	if debug {
		fmt.Println("n:", n, "pcs:", pcs, "lens:", lens[:groupBytes], "vals:", vals)
	}

	// Search for the PC, find the value index.
	index := int(n)
	for i, pc1 := range pcs {
		if pc1 > uint8(pc) {
			index = i
			break
		}
	}
	if debug {
		fmt.Println("index:", index)
	}

	var bias int32

	switch useBias {
	default:
		panic("bias scheme not implemented")
	case biasNone:
		break
	case biasFixed:
		bias = fixedBias
	case biasStartValue:
		// Decode the start value.
		startLen := count0124(lens[0] & 0b11)
		bias = *(*int32)(unsafe.Pointer(&vals[0]))
		shift := (4 - startLen) * 8
		bias = (bias << shift) >> shift
		if debug {
			fmt.Println("start len:", startLen)
			fmt.Println("bias:", bias)
		}
		if index == 0 {
			return bias
		}
	}

	// Find the offset of the value.
	valOff := 0
	for _, v := range lens[:index/4] {
		valOff += count0124(v)
	}
	// TODO: I think on little endian, I can do larger loads with the masking.
	// TODO: Since I'm just counting, can I shift instead of masking?
	valOff += count0124(lens[index/4] & masks[index%4])

	// Load the value.
	valLen := count0124(lens[index/4] & selMask[index%4])
	if debug {
		fmt.Println("valOff:", valOff, "valLen:", valLen)
		fmt.Printf("%02x\n", lens[index/4]&selMask[index%4])
	}
	val := *(*int32)(unsafe.Pointer(&vals[valOff]))
	shift := (4 - valLen) * 8
	val = (val << shift) >> shift
	val += bias

	return val
}

var masks = [...]uint8{0, 0b11, 0b1111, 0b111111}
var selMask = [...]uint8{0b11, 0b1100, 0b110000, 0b11000000}

func count0124Formula(x uint8) int {
	// See also streamvbyte for some ideas.

	// A table is faster than this for 1 byte. That might not be true for larger
	// values, if we switch to using larger values.

	// The first OnesCount maps:
	//   00 => 0
	//   01 => 1
	//   10 => 1 (want 2; need to add 1)
	//   11 => 2 (want 4; need to add 2)
	//
	// Then we map x to a new bitmap where the OnesCount of each field is the
	// amount we want to add to the first OnesCount:
	//   00 => 00 (count 0)
	//   01 => 00 (count 0)
	//   10 => 10 (count 1)
	//   11 => 11 (count 2)
	//
	// We can then add the OnesCount of this residue to get the final count.

	h := x & 0b10101010
	return bits.OnesCount8(x) + bits.OnesCount8(h|((h>>1)&x))
}

func count0124Slow(x uint8) int {
	var sum int
	for i := 0; i < 4; i++ {
		field := (x >> (i * 2)) & 0b11
		if field == 0b11 {
			field = 4
		}
		sum += int(field)
	}
	return sum
}

var count0124Tab = [...]uint8{
	0, 1, 2, 4, 1, 2, 3, 5, 2, 3, 4, 6, 4, 5, 6, 8,
	1, 2, 3, 5, 2, 3, 4, 6, 3, 4, 5, 7, 5, 6, 7, 9,
	2, 3, 4, 6, 3, 4, 5, 7, 4, 5, 6, 8, 6, 7, 8, 10,
	4, 5, 6, 8, 5, 6, 7, 9, 6, 7, 8, 10, 8, 9, 10, 12,
	1, 2, 3, 5, 2, 3, 4, 6, 3, 4, 5, 7, 5, 6, 7, 9,
	2, 3, 4, 6, 3, 4, 5, 7, 4, 5, 6, 8, 6, 7, 8, 10,
	3, 4, 5, 7, 4, 5, 6, 8, 5, 6, 7, 9, 7, 8, 9, 11,
	5, 6, 7, 9, 6, 7, 8, 10, 7, 8, 9, 11, 9, 10, 11, 13,
	2, 3, 4, 6, 3, 4, 5, 7, 4, 5, 6, 8, 6, 7, 8, 10,
	3, 4, 5, 7, 4, 5, 6, 8, 5, 6, 7, 9, 7, 8, 9, 11,
	4, 5, 6, 8, 5, 6, 7, 9, 6, 7, 8, 10, 8, 9, 10, 12,
	6, 7, 8, 10, 7, 8, 9, 11, 8, 9, 10, 12, 10, 11, 12, 14,
	4, 5, 6, 8, 5, 6, 7, 9, 6, 7, 8, 10, 8, 9, 10, 12,
	5, 6, 7, 9, 6, 7, 8, 10, 7, 8, 9, 11, 9, 10, 11, 13,
	6, 7, 8, 10, 7, 8, 9, 11, 8, 9, 10, 12, 10, 11, 12, 14,
	8, 9, 10, 12, 9, 10, 11, 13, 10, 11, 12, 14, 12, 13, 14, 16,
}

// count0124 returns the sum of vector x, where x contains 4 2-bit values where
// 0b00 => 0, 0b01 => 1, 0b10 => 2, 0b11 => 4.
func count0124(x uint8) int {
	return int(count0124Tab[x])
}
