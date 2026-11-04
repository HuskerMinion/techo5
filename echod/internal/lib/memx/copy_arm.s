//go:build arm && !noasm

#include "textflag.h"

// func copyARM(dst, src *byte, n int)
//
// 32 bytes an iteration, a load and a store of eight registers, the source asked for 256 bytes ahead.
// n is a multiple of 32.
TEXT ·copyARM(SB), NOSPLIT, $0-12
	MOVW	dst+0(FP), R0
	MOVW	src+4(FP), R1
	MOVW	n+8(FP), R12

loop:
	CMP	$32, R12
	BLT	done
	PLD	256(R1)
	MOVM.IA.W	(R1), [R2-R9]
	MOVM.IA.W	[R2-R9], (R0)
	SUB	$32, R12
	B	loop

done:
	RET

// func fillARM(dst *uint32, n int, v uint32)
//
// n words, a multiple of 8: one store of eight registers an iteration.
TEXT ·fillARM(SB), NOSPLIT, $0-12
	MOVW	dst+0(FP), R0
	MOVW	n+4(FP), R1
	MOVW	v+8(FP), R2
	MOVW	R2, R3
	MOVW	R2, R4
	MOVW	R2, R5
	MOVW	R2, R6
	MOVW	R2, R7
	MOVW	R2, R8
	MOVW	R2, R9

floop:
	CMP	$8, R1
	BLT	fdone
	MOVM.IA.W	[R2-R9], (R0)
	SUB	$8, R1
	B	floop

fdone:
	RET
