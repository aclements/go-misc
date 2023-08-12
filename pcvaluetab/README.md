This implements an alternate Go PCDATA encoding that's designed to be much
faster to decode, at the cost of a slight increase in size.

# Overview

Each function in Go has several PCDATA tables. Each table logically maps each PC
in the function to an int32 value. Typically, many PCs in a row will have the
same value, and the encoding optimizes for this. Almost all uses of PCDATA
tables at run time involving looking up the value in a table for a specific PC
(versus traversing the entire table).

# Go 1.21 varint delta format

The current format is simple and compact, but very inefficient to decode. It
consists of a repeated sequence of:

    valueDelta Varint
    runLen     Uvarint

followed by a 0 byte.

The decoder implicitly starts with a "current" value of -1. Each record gives a
delta to add to the current value and the length of the run of PCs the have that
value.

Note that if the value at PC 0 is -1, this encoding will start with a 0 byte.
Thus, decoders must not treat a 0 byte at the beginning of the encoding as a
terminator byte. After this, a 0 valueDelta unambiguously indicates the end of
the encoded stream.

This format is simple and quite compact. It takes advantage of the typically
long runs of identical values, and the fact the values may be large but tend to
be clustered (for example, line numbers). It also implicitly encodes the size of
the function, so a decoder can detect requests for PCs outside the range of the
table.

But this format has several downsides. Varints are expensive to decode, and
finding the value for a particular PC requires decoding the table from the very
beginning until we pass the requested PC. Decoding is so expensive that the Go
runtime uses a cache on top of these tables, which helps even though this cache
has a fairly low hit rate.

# Alternate "linear index" format

This package implements an alternate encoding. The central goal of this format
is to support point queries with minimal scanning.

We break the PC range of a function into 256 byte "chunks". For example, if a
function is 900 bytes long, it will consist of four chunks, one for each 256
bytes of the function. (For architectures with a PC quantum of greater than 1
byte, we multiply all of this by the PC quantum.)

The overall encoding consists of a chunk index, followed by the encoding of each
chunk.

Unlike the varint delta encoding, the linear index format does not encode the
size of functions. In fact, decoding this format requires knowing the size of
the function. Thus, it must be encoded out of band. It's possible to rearrange
the `func_` structure to make room for this, so there's no space overhead for
this.

## Chunk index

The chunk index encodes the byte offset of each chunk relative to the end of the
chunk index. Given n chunk offsets, the chunk index has three possible layouts:

    [n-1]uint8        If all offsets are <= 0xff
    0xfe [n-1]uint16  If all offsets are <= 0xffff
    0xff [n-1]uint32  Otherwise

The relative offset of chunk 0 is always 0, so it's not represented in the
index. This also means that if a function is less than 256 bytes and thus has
only one chunk, the chunk index will be 0 bytes long.

The vast majority of tables can represent all chunk offsets in one byte, so they
will use the first form. If the first byte would be 0xfe or 0xff, we fall back
to the second form, since otherwise this would be ambiguous.

A decoder first looks up `pc>>8` in the chunk index to find the offset of the
chunk for target PC.

## Chunk encoding

Each chunk covers a 256 byte range of the function and is encoded as follows:

    n    uint8
    pcs  [n]byte
    vlen [n+1]uint2 // padded to a byte
    vals [n+1]vint

The `pcs` field is a list of `pc&0xff` for each PC at which the value differs
from the previous PC in the chunk, in ascending order. The first PC in the chunk
(`pc&0xff == 0`) is never listed in `pcs`, since there is no previous PC in the
chunk. This means there are at most 255 PCs in `pcs`, so the length of `pcs` can
fit in the single byte `n` field.

The PC list is followed by n+1 values in a variable-length encoding. `vals[0]`
is the value of the first PC in the chunk, as well as the "bias" value for all
other values in this chunk. The value from `pcs[i]` to `pcs[i+1]` (or to the end
of the block) is `bias + vals[i+1]`. Since values are often large by tend to be
clustered, this bias value often makes it possible to encode the remaining
values in fewer bytes.

The value list starts with the byte lengths of all values, encoded in the `vlen`
array as packed 2-bit values. In this encoding, 0b01 corresponds to a 1-byte
value, 0b10 to a 2-byte value, and 0b11 to a 4-byte value. This is padded out to
a byte with "0" bits. This is followed by the values themselves in the `vals`
field, where each value is encoded in an int8, int16, or int32. A decoder can
find the offset of the i'th value by summing the lengths of values 0 through
i-1.

The particular encoding of `vlen` makes it possible to mask unused fields to
0b00 and then use a single "sum" operation to compute both cumulative sums of
`vlen` and individual fields.

A decoder scans `pcs` to find the index `i` of the last PC <= the target PC, or
else -1. It reads the bias value `bias` from  `vals[0]`, with length `vlens[0]`.
If `i` is 0, it returns `bias`. Otherwise, it then gets `vlens[i]` and computes
the sum of `vlens[:i]` to get the byte size and offset of `vals[i]`. Finally, it
returns `bias + vals[i]`.

## Constant chunk deduplication

The encoder deduplicates chunks where all 256 PCs have the same value, which is
common in large functions. Since the index contains offsets to the encoding of
each chunk, the encoder simply uses the same offset for later duplicates of
constant chunks. In principle, this could be used for non-constant chunks that
encode identically, but this doesn't happen often outside of constant chunks.

This optimization is transparent to decoders.

