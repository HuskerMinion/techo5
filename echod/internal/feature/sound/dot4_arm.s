//go:build arm && !noasm

#include "textflag.h"

// func dot4I8ASM(x, w0, w1, w2, w3 *int8, n int, out *[4]int32)
//
// Accumulates four int8 dot products (each a multiple of 8 bytes) in parallel so x is read once for
// four output channels. Uses standard ARMv7 integer instructions (PLD, MOVBS, MULA) so the single
// GOARCH=arm daemon runs at speed on both 32-bit (Echo Spot) and 64-bit (Show 5/8, Dot 3) boards.
// Never touches R10 (g) or R11 (assembler temp).
TEXT ·dot4I8ASM(SB), NOSPLIT, $0-28
	MOVW	x+0(FP), R0
	MOVW	w0+4(FP), R1
	MOVW	w1+8(FP), R2
	MOVW	w2+12(FP), R3
	MOVW	w3+16(FP), R4
	MOVW	n+20(FP), R5
	ADD	R0, R5
	MOVW	R5, n+20(FP)	// reuse argument slot as x_end

	MOVW	out+24(FP), R12
	MOVW	0(R12), R6
	MOVW	4(R12), R7
	MOVW	8(R12), R8
	MOVW	12(R12), R9

loop8:
	PLD	128(R1)
	PLD	128(R2)
	PLD	128(R3)
	PLD	128(R4)

	MOVBS	0(R0), R5
	MOVBS	0(R1), R12; MULA R12, R5, R6, R6
	MOVBS	0(R2), R12; MULA R12, R5, R7, R7
	MOVBS	0(R3), R12; MULA R12, R5, R8, R8
	MOVBS	0(R4), R12; MULA R12, R5, R9, R9

	MOVBS	1(R0), R5
	MOVBS	1(R1), R12; MULA R12, R5, R6, R6
	MOVBS	1(R2), R12; MULA R12, R5, R7, R7
	MOVBS	1(R3), R12; MULA R12, R5, R8, R8
	MOVBS	1(R4), R12; MULA R12, R5, R9, R9

	MOVBS	2(R0), R5
	MOVBS	2(R1), R12; MULA R12, R5, R6, R6
	MOVBS	2(R2), R12; MULA R12, R5, R7, R7
	MOVBS	2(R3), R12; MULA R12, R5, R8, R8
	MOVBS	2(R4), R12; MULA R12, R5, R9, R9

	MOVBS	3(R0), R5
	MOVBS	3(R1), R12; MULA R12, R5, R6, R6
	MOVBS	3(R2), R12; MULA R12, R5, R7, R7
	MOVBS	3(R3), R12; MULA R12, R5, R8, R8
	MOVBS	3(R4), R12; MULA R12, R5, R9, R9

	MOVBS	4(R0), R5
	MOVBS	4(R1), R12; MULA R12, R5, R6, R6
	MOVBS	4(R2), R12; MULA R12, R5, R7, R7
	MOVBS	4(R3), R12; MULA R12, R5, R8, R8
	MOVBS	4(R4), R12; MULA R12, R5, R9, R9

	MOVBS	5(R0), R5
	MOVBS	5(R1), R12; MULA R12, R5, R6, R6
	MOVBS	5(R2), R12; MULA R12, R5, R7, R7
	MOVBS	5(R3), R12; MULA R12, R5, R8, R8
	MOVBS	5(R4), R12; MULA R12, R5, R9, R9

	MOVBS	6(R0), R5
	MOVBS	6(R1), R12; MULA R12, R5, R6, R6
	MOVBS	6(R2), R12; MULA R12, R5, R7, R7
	MOVBS	6(R3), R12; MULA R12, R5, R8, R8
	MOVBS	6(R4), R12; MULA R12, R5, R9, R9

	MOVBS	7(R0), R5
	MOVBS	7(R1), R12; MULA R12, R5, R6, R6
	MOVBS	7(R2), R12; MULA R12, R5, R7, R7
	MOVBS	7(R3), R12; MULA R12, R5, R8, R8
	MOVBS	7(R4), R12; MULA R12, R5, R9, R9

	ADD	$8, R0
	ADD	$8, R1
	ADD	$8, R2
	ADD	$8, R3
	ADD	$8, R4

	MOVW	n+20(FP), R5
	CMP	R5, R0
	BLT	loop8

	MOVW	out+24(FP), R12
	MOVW	R6, 0(R12)
	MOVW	R7, 4(R12)
	MOVW	R8, 8(R12)
	MOVW	R9, 12(R12)
	RET
