// Copyright 2016 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

#include "textflag.h"

GLOBL	·hasBMI2(SB),NOPTR,$1

TEXT ·queryBMI2(SB),NOSPLIT,$0-1
	// TODO: Check validity of query.
	MOVQ	$0x07, AX
	MOVQ	$0, CX
	CPUID
	// Bit 8 of EBX indicates BMI2 support.
	BTQ	$8, BX
	SETCS	ret+0(FP)
	RET

// Hand-coded byte decoding loop with some clever tricks.
TEXT ·decodeVarintAsmLoop(SB),NOSPLIT,$0-40
	MOVQ	buf_base+0(FP), BX	// Pointer
	MOVQ	buf_len+8(FP), AX	// Length
	MOVL	$10, CX
	CMPQ	AX, CX
	CMOVLGT	CX, AX		// Length is at most 10
	XORL	SI, SI		// Index
	XORL	CX, CX		// Shift
	XORL	DX, DX		// Value

loop:
	CMPL	SI, AX
	JEQ	bad		// Reached end of buffer or >10 bytes

	MOVBLZX	(SI)(BX*1), DI	// Load next byte
	INCL	SI
	BTRL	$7, DI		// Is bit 7 set? Clear bit 7.
	JNC	last		// If not set, this is the final byte
	SHLQ	CL, DI		// value |= value << shift
	ORQ	DI, DX
	ADDL	$7, CX		// shift += 7
	JMP	loop

last:
	SHLQ	CL, DI		// Final value |= value << shift
	ORQ	DI, DX
	// Return decoded value and length.
	MOVQ	DX, x+24(FP)
	MOVQ	SI, n+32(FP)
	RET

bad:
	MOVQ	$0, x+24(FP)
	MOVQ	$0, n+32(FP)
	RET

// decodeVarintAsmBMI2 uses the BMI2 PEXT instruction to extract 7
// bits from each byte in one instruction.
TEXT ·decodeVarintAsmBMI2(SB),NOSPLIT,$0-40
	MOVQ	buf_base+0(FP), BX
	MOVQ	buf_len+8(FP), CX

	// Take the slow path if there's no BMI2 or there are fewer
	// than 8 bytes available.
	MOVBLZX	·hasBMI2(SB), AX
	TESTB	AL, AL
	JEQ	slowpath
	CMPQ	CX, $8
	JLT	slowpath

	// Load 8 bytes from buf.
	MOVQ	(BX), AX

	// Extract the continuation bits into BX.
	MOVQ	AX, M0
	PMOVMSKB	M0, BX
	// Compute byte length - 1 of varint into BX.
	NOTL	BX
	BSFL	BX, BX
	// If it's more than 8 bytes, take the slow path.
	CMPL	BX, $8
	JGE	slowpath
	// Extract the relevant bytes from the input.
	INCL	BX
	MOVQ	BX, CX
	SHLQ	$(3+8), CX	// CX[15:8] = (byte len * 8); CX[7:0] = 0
	BEXTRQ	CX, AX, AX	// Requires BMI1
	// Extract the low 7 bits from each byte of the input.
	MOVQ	$0x7f7f7f7f7f7f7f7f, DI
	PEXTQ	DI, AX, DX	// Requires BMI2
	// Return decoded value and length.
	MOVQ	DX, x+24(FP)
	MOVQ	BX, n+32(FP)
	RET

slowpath:
	// Consume buffer one byte at a time.
	// TODO: Could merge with some of the above registers better.
	MOVQ	buf_base+0(FP), BX	// Pointer
	MOVQ	buf_len+8(FP), AX	// Length
	MOVQ	$10, CX
	CMPQ	AX, CX
	CMOVQGT	CX, AX		// Length is at most 10
	XORQ	SI, SI		// Index
	XORQ	CX, CX		// Shift
	XORQ	DX, DX		// Value

loop:
	CMPQ	SI, AX
	JEQ	bad		// Reached end of buffer or >10 bytes

	MOVBLZX	(SI)(BX*1), DI	// Load next byte
	INCQ	SI
	BTRL	$7, DI		// Is bit 7 set? Clear bit 7.
	JNC	last		// If not set, this is the final byte
	SHLQ	CL, DI		// value |= value << shift
	ORQ	DI, DX
	ADDQ	$7, CX		// shift += 7
	JMP	loop

last:
	SHLQ	CL, DI		// value |= value << shift
	ORQ	DI, DX
	// Return decoded value and length.
	MOVQ	DX, x+24(FP)
	MOVQ	SI, n+32(FP)
	RET

bad:
	MOVQ	$0, x+24(FP)
	MOVQ	$0, n+32(FP)
	RET

// The other two also use PEXT, but use different tricks to extract
// the length and set up the mask. They turned out to be slower than
// the one above, but are historically interesting.

DATA extract<>+0x00(SB)/8,$0x000000000000007f
DATA extract<>+0x08(SB)/8,$0x0000000000007f7f
DATA extract<>+0x10(SB)/8,$0x00000000007f7f7f
DATA extract<>+0x18(SB)/8,$0x000000007f7f7f7f
DATA extract<>+0x20(SB)/8,$0x0000007f7f7f7f7f
DATA extract<>+0x28(SB)/8,$0x00007f7f7f7f7f7f
DATA extract<>+0x30(SB)/8,$0x007f7f7f7f7f7f7f
DATA extract<>+0x38(SB)/8,$0x7f7f7f7f7f7f7f7f
GLOBL extract<>(SB),(NOPTR+RODATA),$(8*8)

TEXT ·decodeVarintAsm1(SB),NOSPLIT,$0-40
	// Take the slow path if there's no BMI2 or there are fewer
	// than 8 bytes available.
	MOVBLZX	·hasBMI2(SB), AX
	TESTB	AL, AL
	JEQ	slowpath
	MOVQ	buf_len+8(FP), AX
	CMPQ	AX, $8
	JLT	slowpath

	// Load 8 bytes from buf.
	MOVQ	buf_base+0(FP), AX
	MOVQ	(AX), AX

	// Extract the continuation bits into BX.
	MOVQ	AX, M0
	PMOVMSKB	M0, BX
	// Compute byte length - 1 of varint into BX.
	NOTL	BX
	BSFL	BX, BX
	// If it's more than 8 bytes, take the slow path.
	CMPL	BX, $8
	JGE	slowpath
	// Extract the value into DX using a mask lookup table.
	MOVQ	$extract<>(SB), CX
	MOVQ	(CX)(BX*8), DX
	PEXTQ	DX, AX, DX	// Requires BMI2
	// Return decoded value and length.
	MOVQ	DX, x+24(FP)
	INCL	BX
	MOVQ	BX, n+32(FP)
	RET

slowpath:
	// Consume buffer one byte at a time.
	// TODO: Could merge with some of the above registers better.
	MOVQ	buf_base+0(FP), BX	// Pointer
	MOVQ	buf_len+8(FP), AX	// Length
	MOVQ	$10, CX
	CMPQ	AX, CX
	CMOVQGT	CX, AX		// Length is at most 10
	XORQ	SI, SI		// Index
	XORQ	CX, CX		// Shift
	XORQ	DX, DX		// Value

loop:
	CMPQ	SI, AX
	JEQ	bad		// Reached end of buffer or >10 bytes

	MOVBLZX	(SI)(BX*1), DI	// Load next byte
	INCQ	SI
	BTRL	$7, DI		// Is bit 7 set? Clear bit 7.
	JNC	last		// If not set, this is the final byte
	SHLQ	CL, DI		// value |= value << shift
	ORQ	DI, DX
	ADDQ	$7, CX		// shift += 7
	JMP	loop

last:
	SHLQ	CL, DI		// value |= value << shift
	ORQ	DI, DX
	// Return decoded value and length.
	MOVQ	DX, x+24(FP)
	MOVQ	SI, n+32(FP)
	RET

bad:
	MOVQ	$0, x+24(FP)
	MOVQ	$0, n+32(FP)
	RET

TEXT ·decodeVarintAsm2(SB),NOSPLIT,$0-40
	MOVQ	buf_base+0(FP), BX
	MOVQ	buf_len+8(FP), CX

	// Take the slow path if there's no BMI2 or there are fewer
	// than 8 bytes available.
	MOVBLZX	·hasBMI2(SB), AX
	TESTB	AL, AL
	JEQ	slowpath
	CMPQ	CX, $8
	JLT	slowpath

	// Load 8 bytes from buf.
	MOVQ	(BX), AX

	// Get continuation bit mask into DX.
	MOVQ	$0x7f7f7f7f7f7f7f7f, DI
	MOVQ	AX, DX
	ORQ	DI, DX
	// Compute bit length of varint into CX.
	NOTQ	DX
	BSFQ	DX, CX
	// If all continuation bits are set, take the slow path.
	JZ	slowpath
	// Compute bit extraction mask into R14.
	//BLSMSKQ	DX, R14		// Requires BMI1
	BYTE $0xc4; BYTE $0xe2; BYTE $0x88; BYTE $0xf3; BYTE $0xd2
	// Mask the value.
	ANDQ	R14, AX
	// Extract the bits.
	PEXTQ	DI, AX, DX	// Requires BMI2

	// Compute byte length. 7=>1, 15=>2, etc.
	INCQ	CX
	SHRQ	$3, CX

	// Return decoded value and length.
	MOVQ	DX, x+24(FP)
	MOVQ	CX, n+32(FP)
	RET

slowpath:
	// Consume buffer one byte at a time.
	// TODO: Could merge with some of the above registers better.
	MOVQ	buf_base+0(FP), BX	// Pointer
	MOVQ	buf_len+8(FP), AX	// Length
	MOVQ	$10, CX
	CMPQ	AX, CX
	CMOVQGT	CX, AX		// Length is at most 10
	XORQ	SI, SI		// Index
	XORQ	CX, CX		// Shift
	XORQ	DX, DX		// Value

loop:
	CMPQ	SI, AX
	JEQ	bad		// Reached end of buffer or >10 bytes

	MOVBLZX	(SI)(BX*1), DI	// Load next byte
	INCQ	SI
	BTRL	$7, DI		// Is bit 7 set? Clear bit 7.
	JNC	last		// If not set, this is the final byte
	SHLQ	CL, DI		// value |= value << shift
	ORQ	DI, DX
	ADDQ	$7, CX		// shift += 7
	JMP	loop

last:
	SHLQ	CL, DI		// value |= value << shift
	ORQ	DI, DX
	// Return decoded value and length.
	MOVQ	DX, x+24(FP)
	MOVQ	SI, n+32(FP)
	RET

bad:
	MOVQ	$0, x+24(FP)
	MOVQ	$0, n+32(FP)
	RET
