# -*- indent-tabs-mode: nil -*-

# Parse output of "go build -gcflags=all=-S -a cmd/go >& /tmp/go.s" and
# compress register liveness maps in various ways.

import re
import sys
import collections

if True:
    # Register maps
    FUNCDATA = "3"
    PCDATA = "2"
else:
    # Stack maps
    FUNCDATA = "1" # Locals (not args)
    PCDATA = "0"

class Stackmap:
    def __init__(self, dec=None):
        if dec is None:
            self.n = self.nbit = 0
            self.bitmaps = []
        else:
            # Decode Go encoding of a runtime.stackmap.
            n = dec.int32()
            self.nbit = dec.int32()
            self.bitmaps = [dec.bitmap(self.nbit) for i in range(n)]

    def clone(self):
        enc = Encoder()
        self.encode(enc)
        return Stackmap(Decoder(enc.b))

    def add(self, bitmap):
        nbit, b2 = 0, bitmap
        while b2 != 0:
            nbit += 1
            b2 >>= 1
        self.nbit = max(nbit, self.nbit)
        for i, b2 in enumerate(self.bitmaps):
            if bitmap == b2:
                return i
        self.bitmaps.append(bitmap)
        return len(self.bitmaps)-1

    def sort(self):
        s = sorted((b, i) for i, b in enumerate(self.bitmaps))
        self.bitmaps = [b for b, i in s]
        return [i for b, i in s]

    def encode(self, enc, compact=False):
        enc.int32(len(self.bitmaps))
        if compact:
            enc.uint8(self.nbit)
            combined = 0
            for i, b in enumerate(self.bitmaps):
                combined |= b << (i * self.nbit)
            enc.bitmap(combined, len(self.bitmaps) * self.nbit)
        else:
            enc.int32(self.nbit)
            for b in self.bitmaps:
                enc.bitmap(b, self.nbit)

class PCData:
    def __init__(self):
        self.pcdata = []

    def encode(self, enc):
        last = (0, 0)
        for e in self.pcdata:
            enc.uvarint(e[0] - last[0])
            enc.svarint(e[1] - last[1])
            last = e
        enc.uint8(0)

    def huffSize(self, pcHuff, valHuff):
        bits = 0
        lastPC = 0
        for pc, val in self.pcdata:
            bits += pcHuff[pc - lastPC][1] + valHuff[val][1]
            lastPC = pc
        return (bits + 7) // 8

    def grSize(self, pcHuff, n):
        bits = 0
        lastPC = 0
        for pc, val in self.pcdata:
            bits += pcHuff[pc - lastPC][1]
            lastPC = pc
            bits += grSize(val + 1, n)
        return (bits + 7) // 8

def grSize(val, n):
    """The number of bits in the Golomb-Rice coding of val in base 2^n."""
    return 1 + (val >> n) + n

class Decoder:
    def __init__(self, b):
        self.b = memoryview(b)

    def int32(self):
        b = self.b
        self.b = b[4:]
        return b[0] + (b[1] << 8) + (b[2] << 16) + (b[3] << 24)

    def bitmap(self, nbits):
        bitmap = 0
        nbytes = (nbits + 7) // 8
        for i in range(nbytes):
            bitmap = bitmap | (self.b[i] << (i*8))
        self.b = self.b[nbytes:]
        return bitmap

class Encoder:
    def __init__(self):
        self.b = bytearray()

    def uint8(self, i):
        self.b.append(i)

    def int32(self, i):
        self.b.extend([i&0xFF, (i>>8)&0xFF, (i>>16)&0xFF, (i>>24)&0xFF])

    def bitmap(self, bits, nbits):
        for i in range((nbits + 7) // 8):
            self.b.append((bits >> (i*8)) & 0xFF)

    def uvarint(self, v):
        if v < 0:
            raise ValueError("negative unsigned varint", v)
        while v > 0x7f:
            self.b.append((v & 0x7f) | 0x80)
            v >>= 7
        self.b.append(v)

    def svarint(self, v):
        ux = v << 1
        if v < 0:
            ux = ~ux
        self.uvarint(ux)

def parse(stream):
    import parseasm
    objs = parseasm.parse(stream)
    fns = []
    for obj in objs.values():
        if not isinstance(obj, parseasm.Func):
            continue
        fns.append(obj)
        obj.regMaps = []        # [(pc, register bitmap)]
        regMap = None
        for inst in obj.insts:
            if inst.asm.startswith("FUNCDATA\t$"+FUNCDATA+", "):
                regMapSym = inst.asm.split(" ")[1][:-4]
                regMap = Stackmap(Decoder(objs[regMapSym].data))
            elif inst.asm.startswith("PCDATA\t$"+PCDATA+", "):
                idx = int(inst.asm.split(" ")[1][1:])
                obj.regMaps.append((inst.pc, regMap.bitmaps[idx]))
    return fns

def genStackMaps(fns, padToByte=True, dedup=True, sortBitmaps=False):
    regMapSet = {}

    for fn in fns:
        # Create pcdata and register map for fn.
        fn.pcdataRegs = PCData()
        fn.funcdataRegMap = Stackmap()
        for (pc, bitmap) in fn.regMaps:
            fn.pcdataRegs.pcdata.append((pc, fn.funcdataRegMap.add(bitmap)))

        if sortBitmaps:
            remap = regMap.sort()
            pcdata.pcdata = [(pc, remap[idx]) for pc, idx in pcdata.pcdata]

        # Encode and dedup register maps.
        if dedup:
            e = Encoder()
            fn.funcdataRegMap.encode(e, not padToByte)
            regMap = bytes(e.b)
            if regMap in regMapSet:
                fn.funcdataRegMap = regMapSet[regMap]
            else:
                regMapSet[regMap] = fn.funcdataRegMap
        else:
            regMapSet[fn] = fn.funcdataRegMap

    return regMapSet.values()

def likeStackMap(fns, padToByte=True, dedup=True, sortBitmaps=None, huffmanPcdata=False, grPcdata=False):
    regMapSet = set()
    regMaps = bytearray()
    pcdatas = [] #Encoder()
    extra = 0
    for fn in fns:
        # Create pcdata and register map for fn.
        pcdata = PCData()
        regMap = Stackmap()
        if sortBitmaps == "freq":
            # Pre-populate regMap in frequency order.
            regMapFreq = collections.Counter()
            for pc, bitmap in fn.regMaps:
                regMapFreq[bitmap] += 1
            for bitmap, freq in sorted(regMapFreq.items(), key=lambda item: item[1], reverse=True):
                regMap.add(bitmap)
        for pc, bitmap in fn.regMaps:
            pcdata.pcdata.append((pc, regMap.add(bitmap)))

        if sortBitmaps == "value":
            remap = regMap.sort()
            pcdata.pcdata = [(pc, remap[idx]) for pc, idx in pcdata.pcdata]

        pcdatas.append(pcdata)

        # Encode register map and dedup.
        e = Encoder()
        regMap.encode(e, not padToByte)
        regMap = bytes(e.b)
        if not dedup or regMap not in regMapSet:
            regMapSet.add(regMap)
            regMaps.extend(regMap)

        extra += 8 + 4 # funcdata pointer, pcdata table offset

    # Encode pcdata.
    pcdataEnc = Encoder()
    if huffmanPcdata or grPcdata:
        pcDeltas, _ = countDeltas(fns)
        pcdataHist = collections.Counter()
        for pcdata in pcdatas:
            for _, idx in pcdata.pcdata:
                pcdataHist[idx] += 1
        pcHuff = huffman(pcDeltas)
        pcdataHuff = huffman(pcdataHist)
        size = 0
        for pcdata in pcdatas:
            if huffmanPcdata:
                size += pcdata.huffSize(pcHuff, pcdataHuff)
            elif grPcdata:
                size += pcdata.grSize(pcHuff, grPcdata)
        pcdataEnc.b = "\0" * size # Whatever
    else:
        for pcdata in pcdatas:
            pcdata.encode(pcdataEnc)

    return {"gclocals": len(regMaps), "pcdata": len(pcdataEnc.b), "extra": extra}

def filterLiveToDead(fns):
    # Only emit pcdata if something becomes newly-live (this is a
    # lower bound on what the "don't care" optimization could
    # achieve).
    for fn in fns:
        newRegMaps = []
        prevBitmap = 0
        for (pc, bitmap) in fn.regMaps:
            if bitmap is None:
                newRegIdx.append((pc, None))
                prevBitmap = 0
                continue
            if bitmap & ~prevBitmap != 0:
                # New bits set.
                newRegMaps.append((pc, bitmap))
            prevBitmap = bitmap
        fn.regMaps = newRegMaps

def total(dct):
    dct["total"] = 0
    dct["total"] = sum(dct.values())
    return dct

def iterDeltas(regMaps):
    prevPC = prevBitmap = 0
    for (pc, bitmap) in regMaps:
        pcDelta = pc - prevPC
        prevPC = pc

        if bitmap is None:
            bitmapDelta = None
            prevBitmap = 0
        else:
            bitmapDelta = bitmap ^ prevBitmap
            prevBitmap = bitmap

        yield pcDelta, bitmapDelta

def countMaps(fns):
    maps = collections.Counter()
    for fn in fns:
        for _, bitmap in fn.regMaps:
            maps[bitmap] += 1
    return maps

def countDeltas(fns):
    pcDeltas, deltas = collections.Counter(), collections.Counter()
    # This actually spreads out the head of the distribution quite a bit
    # because things are more likely to die in clumps and at the same time
    # as something else becomes live.
    #filterLiveToDead(fns)
    for fn in fns:
        for pcDelta, bitmapDelta in iterDeltas(fn.regMaps):
            pcDeltas[pcDelta] += 1
            deltas[bitmapDelta] += 1
    return pcDeltas, deltas

def huffman(counts, streamAlign=1):
    code = [(count, val) for val, count in counts.items()]
    radix = 2**streamAlign
    while len(code) > 1:
        code.sort(key=lambda x: x[0], reverse=True)
        if len(code) < radix:
            children, code = code, []
        else:
            children, code = code[-radix:], code[:-radix]
        code.append((sum(child[0] for child in children),
                     [child[1] for child in children]))
    tree = {}
    def mktree(node, codeword, bits):
        if isinstance(node, list):
            for i, child in enumerate(node):
                mktree(child, (codeword << streamAlign) + i, bits + streamAlign)
        else:
            tree[node] = (codeword, bits)
    mktree(code[0][1], 0, 0)
    return tree

def huffmanCoded(fns, streamAlign=1):
    pcDeltas, maskDeltas = countDeltas(fns)
    hPCs = huffman(pcDeltas, streamAlign)
    hBitmaps = huffman(maskDeltas, streamAlign)

    pcdataBits = 0
    extra = 0
    for fn in fns:
        for pcDelta, bitmapDelta in iterDeltas(fn.regMaps):
            pcdataBits += hPCs[pcDelta][1] + hBitmaps[bitmapDelta][1]
        pcdataBits = (pcdataBits + 7) &~ 7 # Byte align
        extra += 4                         # PCDATA
    return {"pcdata": (pcdataBits + 7) // 8, "extra": extra}
fns = parse(sys.stdin)

if True:
    print(total(likeStackMap(fns)))
    # Linker dedup of gclocals reduces gclocals by >2X
    #print(total(likeStackMap(fns, dedup=False)))
    #print(total(likeStackMap(fns, sortBitmaps="value")))
    # 'total': 529225, 'pcdata': 292703, 'gclocals': 77558, 'extra': 158964
    print(total(likeStackMap(fns, huffmanPcdata=True)))
    print(total(likeStackMap(fns, huffmanPcdata=True, sortBitmaps="freq")))
    for n in range(0, 8):
        print(n, total(likeStackMap(fns, grPcdata=n, sortBitmaps="freq")))
    #print(total(likeStackMap(fns, compactBitmap=True)))
    # 'total': 407999, 'pcdata': 302023, 'extra': 105976
    print(total(huffmanCoded(fns)))
    print(total(huffmanCoded(fns, streamAlign=8)))
    # Only emitting on newly live reduces pcdata by 42%, gclocals by 10%
    filterLiveToDead(fns)
    print(total(likeStackMap(fns)))

if False:
    # What do the bitmaps look like?
    counts = countMaps(fns)
    for bitmap, count in counts.items():
        print(count, bin(bitmap))

if False:
    # What do the bitmap changes look like?
    _, deltas = countDeltas(fns)
    for delta, count in deltas.items():
        print(count, bin(delta))

if False:
    # PC delta histogram
    pcDeltaHist, _ = countDeltas(fns)
    for delta, count in pcDeltaHist.items():
        print(count, delta)
