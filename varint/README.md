This directory contains experiments with varint decoding using
hand-coded assembly.

The simple assembly loop is 15–30% faster than the Go loop. The loop
is somewhat clever, but in principle the compiler could probably
produce this code.

Most of the code experiments with BMI2 instructions. This requires
Haswell or newer, which the benchmark will detect. This approach is
constant time up to 8 byte varints (56 bit values). It's 50% faster
than the Go code for 8 byte varints, but 80% slower for one byte
varints.
