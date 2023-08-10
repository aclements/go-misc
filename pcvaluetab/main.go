// pcvaluetab is an experiment with alternate pcvalue encodings.
//
// Usage: pcvaluetab {binary}
package main

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"sort"

	"golang.org/x/exp/maps"
)

type SymTab struct {
	dec Decoder

	header rawPCHeader

	funcNameData []byte
	pctabData    []byte
	pclnData     []byte

	Funcs  []Func
	PCTabs map[PCTabKey]*VarintPCData
}

type Func struct {
	Name    string
	EncSize int // Bytes in rawFunc
	TextLen int
	PCTabs  []PCTabKey
}

type PCTabKey uint32

type VarintPCData struct {
	Raw     []byte
	PCs     []uint32
	Vals    []int32
	TextLen uint32
}

func LoadSymTab(path string) *SymTab {
	f, err := elf.Open(flag.Arg(0))
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	sec := f.Section(".gopclntab")
	if sec == nil {
		log.Fatal("missing .gopclntab")
	}
	data, err := sec.Data()
	if err != nil {
		log.Fatal("reading .gopclntab: ", err)
	}

	dec := Decoder{Order: binary.LittleEndian}
	var header rawPCHeader
	// Read the pre-header, which gives us order and size information.
	if _, err := dec.Read(data, &header.rawPCHeader1); err != nil {
		log.Fatal("reading header: ", err)
	}
	switch header.Magic {
	case 0xFFFFFFF1:
		// Go 1.20 little endian
	case 0xF1FFFFFF:
		// Go 1.20 big endian
		dec.Order = binary.BigEndian
	default:
		log.Fatalf("bad magic in header: %x", header.Magic)
	}
	dec.IntSize = int(header.PtrSize)
	dec.PtrSize = int(header.PtrSize)
	// Read the full header
	if _, err := dec.Read(data, &header); err != nil {
		log.Fatal("reading header: ", err)
	}

	// Extract data from the header.
	symtab := new(SymTab)
	symtab.dec = dec
	symtab.header = header
	symtab.funcNameData = data[header.FuncnameOffset:]
	symtab.pctabData = data[header.PctabOffset:]
	symtab.pclnData = data[header.PclnOffset:]

	// Read the func offsets table. Alternating PC, func_ offset. Ends with a
	// single "last PC".
	funcOffsets := make([]uint32, 2*header.Nfunc+1)
	if _, err := dec.Read(symtab.pclnData, funcOffsets); err != nil {
		log.Fatal("reading func offsets: ", err)
	}

	// Load functions.
	symtab.PCTabs = map[PCTabKey]*VarintPCData{}
	for i := 0; i < header.Nfunc; i++ {
		off := funcOffsets[2*i+1]

		var raw rawFunc
		rawLen, err := dec.Read(symtab.pclnData[off:], &raw)
		if err != nil {
			log.Fatal("reading func: ", err)
		}
		nameLen := bytes.IndexByte(symtab.funcNameData[raw.NameOff:], 0)
		name := string(symtab.funcNameData[raw.NameOff:][:nameLen])

		encSize := rawLen + 4*int(raw.Npcdata) + 4*int(raw.Nfuncdata)
		// Round to pointer size
		encSize = (encSize + dec.PtrSize - 1) &^ (dec.PtrSize - 1)

		// raw.Npcdata PCDATA offsets follow the header.
		pcTabs := make([]PCTabKey, 3+raw.Npcdata)
		pcTabs[0] = PCTabKey(raw.Pcfile)
		pcTabs[1] = PCTabKey(raw.Pcln)
		pcTabs[2] = PCTabKey(raw.Pcsp)
		if _, err := dec.Read(symtab.pclnData[off+uint32(rawLen):], pcTabs[3:]); err != nil {
			log.Fatal("reading pcdata offsets: ", err)
		}

		// Load PCDATA tables.
		textLen := 0
		for _, off := range pcTabs {
			if off == 0 {
				// Unused
				continue
			}
			pctab := symtab.PCTabs[off]
			if pctab == nil {
				pctab = decodeVarintPCData(symtab.pctabData[off:])
				symtab.PCTabs[off] = pctab
			}

			if textLen == 0 {
				textLen = int(pctab.TextLen)
			} else if textLen != int(pctab.TextLen) {
				log.Printf("function %s has both length %d and length %d", name, textLen, pctab.TextLen)
			}

		}

		fn := Func{Name: name, EncSize: encSize, TextLen: textLen, PCTabs: pcTabs}
		symtab.Funcs = append(symtab.Funcs, fn)
	}

	return symtab
}

func main() {
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(1)
	}
	binPath := flag.Arg(0)

	var fileBytes int
	if stat, err := os.Stat(binPath); err != nil {
		log.Fatal(err)
	} else {
		fileBytes = int(stat.Size())
	}

	symtab := LoadSymTab(binPath)

	// Walk the funcs.
	var fnSizes Dist
	var funcBytes int
	var tabOffsetDist Dist
	refBytes := 0
	type tabInfo struct {
		tab   *VarintPCData
		alt   []byte
		count int
	}
	dups := make(map[PCTabKey]*tabInfo)
	altDups := make(map[string]int)
	for _, fn := range symtab.Funcs {
		fmt.Printf("%+v\n", fn)

		for _, pcTabKey := range fn.PCTabs {
			tabOffsetDist.Add(int(pcTabKey))

			refBytes += 4
			if pcTabKey == 0 {
				// Unused.
				continue
			}

			info := dups[pcTabKey]
			if info == nil {
				info = new(tabInfo)
				dups[pcTabKey] = info

				info.tab = symtab.PCTabs[pcTabKey]
				info.alt = linearIndex(info.tab)
				if len(info.alt) > len(info.tab.Raw) {
					fmt.Println("LONGER", len(info.alt), len(info.tab.Raw))
				}
			}
			info.count++

			// Add to the altDups table. The alternate encoding might
			// deduplicate better than the varint encoding, so we count this
			// separately.
			altDups[string(info.alt)]++
		}

		fnSizes.Add(fn.TextLen)
	}

	diffPct := func(before, after int) float64 {
		return float64(100*after)/float64(before) - 100
	}
	fmt.Printf("file: %d bytes\n", fileBytes)
	fmt.Printf("functab: %d bytes\n", funcBytes)
	fmt.Printf("refs: %d bytes\n", refBytes)
	fmt.Printf("function sizes:\n%s\n", fnSizes.StringSummary())
	fmt.Printf("pcdata table offsets:\n%s\n", tabOffsetDist.StringSummary())
	fmt.Println()

	fmt.Printf("## varint encoding\n")
	postDedupBytes := 0
	preDedupBytes := 0
	var sizes Dist
	for _, info := range dups {
		size := len(info.tab.Raw)
		postDedupBytes += size
		preDedupBytes += size * info.count
		sizes.Add(size)
	}
	fmt.Printf("tabs: %d bytes post-dedup\n%s\n", postDedupBytes, sizes.StringSummary())
	fmt.Printf("tabs: %d bytes pre-dedup\n", preDedupBytes)
	fmt.Printf("dedup saves: %d bytes\n", preDedupBytes-postDedupBytes)
	if true {
		dedupCountBySize := make(map[int]int)
		for _, info := range dups {
			dedupCountBySize[len(info.tab.Raw)] += info.count
		}

		fmt.Printf("duplicates by size:\n")
		fmt.Printf("%7s %7s %7s:\n", "size", "#dups", "saving")
		sizes := maps.Keys(dedupCountBySize)
		sort.Ints(sizes)
		for _, size := range sizes {
			fmt.Printf("%7d %7d %7d\n", size, dedupCountBySize[size], size*dedupCountBySize[size])
		}
	}
	fmt.Println()

	fmt.Printf("## alternate encoding\n")
	altPostDedupBytes := 0
	altPreDedupBytes := 0
	var altSizes Dist
	for alt, count := range altDups {
		altPostDedupBytes += len(alt)
		altPreDedupBytes += len(alt) * count
		altSizes.Add(len(alt))
	}
	fmt.Printf("tabs: %d bytes post-dedup (%+f%% vs varint)\n%s\n", altPostDedupBytes, diffPct(postDedupBytes, altPostDedupBytes), altSizes.StringSummary())
	fmt.Printf("tabs: %d bytes pre-dedup\n", altPreDedupBytes)
	fmt.Printf("dedup saves: %d bytes\n", altPreDedupBytes-altPostDedupBytes)
	fmt.Printf("file size change: %+f%%\n", diffPct(fileBytes, fileBytes-postDedupBytes+altPostDedupBytes))

	fmt.Printf("group header waste bits:\n%s\n", valueGroupWaste.StringSummary())

}

func decodeVarintPCData(data []byte) *VarintPCData {
	tab := new(VarintPCData)
	pc, val := uint32(0), int32(-1)
	pos := 0
	for pos < len(data) && (data[pos] != 0 || pos == 0) {
		uvdelta, l := binary.Varint(data[pos:])
		if l <= 0 {
			panic("bad varint")
		}
		pos += l
		val += int32(uvdelta)

		tab.PCs = append(tab.PCs, pc)
		tab.Vals = append(tab.Vals, val)

		pcdelta, l := binary.Uvarint(data[pos:])
		if l <= 0 {
			panic("bad uvarint")
		}
		pos += l
		pc += uint32(pcdelta)
	}
	if pos == len(data) {
		log.Fatalf("PCDATA truncated")
	}
	tab.Raw = data[:pos]
	tab.TextLen = pc
	return tab
}

var valueGroupWaste Dist

func linearIndex(tab *VarintPCData) []byte {
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

	var indexVals []int32
	var pcdata []byte

	chunks := uint32((tab.TextLen + 255) >> 8)

	fmt.Printf("%d chunks\n", chunks)

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
			return 0
		} else if int32(int16(uint16(val))) == val {
			*buf = append(*buf, uint8(val), uint8(val>>8))
			return 1
		} else if val<<8>>8 == val {
			*buf = append(*buf, uint8(val), uint8(val>>8), uint8(val>>16))
			return 2
		} else {
			encodeUint32(buf, uint64(uint32(val)))
			return 3
		}
	}
	wastedBits := 0
	encodeGroup := func(buf *[]byte, vals []int32) {
		// Encode group header, at two bits per value.
		bits := 2 * len(vals)
		bytes := (bits + 7) / 8
		wastedBits += bytes*8 - bits
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
	// Offsets of constant chunks, by constant value.
	constChunkOffs := make(map[int32]int)
	for chunk := uint32(0); chunk < chunks; chunk++ {
		if chunk > 0 {
			indexVals = append(indexVals, int32(len(pcdata)))
		}

		// Find range of PCs in this chunk.
		startPCIndex := pcIndex
		for pcIndex < len(tab.PCs) && tab.PCs[pcIndex]>>8 == chunk {
			pcIndex++
		}
		// Each chunk implicitly starts with PC 0, which means there's no need
		// to encode an explicit PC 0.
		if startPCIndex < len(tab.PCs) && tab.PCs[startPCIndex]&0xff == 0 {
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
				// TODO: Update the index.
				_ = off
				continue
			}
			// This is a new constant chunk.
			constChunkOffs[startValue] = len(pcdata)
		}

		// Encode PC count (N). Note that it's important that we never include
		// PC 0 here because that means the maximum count is 255, so it always
		// fits in a byte.
		n := pcIndex - startPCIndex
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
			// TODO: Populate group header.
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
	valueGroupWaste.Add(wastedBits)
	fmt.Println("WASTED BITS", wastedBits)

	// Encode index.
	//
	// TODO: We could use 0 bytes for the offset of chunk 0, then 2 bytes for
	// the next 32, then 4 bytes. Maybe 4 bytes is rare enough it doesn't
	// matter.
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
			fmt.Println("ADJUST", len(index)-prevLen)
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

	// Combine index and values.
	pcdata = append(index, pcdata...)

	fmt.Printf("% 3x\n", pcdata)

	return pcdata
}
