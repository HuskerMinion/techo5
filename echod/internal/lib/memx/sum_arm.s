//go:build arm && !noasm

#include "textflag.h"

// func sumARM(p *uint32, n int) uint64
//
// sumGo with the lines ahead asked for (PLD): the Tegra 3's cores do not prefetch on their own (L1
// prefetch is off in ACTLR, which only the secure world may set), and a read that waits on RAM line by
// line runs at about half the speed of one asked for 256 bytes ahead (surface_rt docs/hw/ram.md).
// Eight words an iteration, two loads of four; the two multiplying lanes alternate so each multiply
// has the other's to hide behind.
TEXT ·sumARM(SB), NOSPLIT, $0-16
	MOVW	p+0(FP), R0
	MOVW	n+4(FP), R1
	MOVW	$0x811c9dc5, R6
	MOVW	$0x27d4eb2f, R7
	MOVW	$0x1b873593, R8
	MOVW	$0x9e3779b1, R9

eight:
	CMP	$8, R1
	BLT	two
	PLD	256(R0)
	MOVM.IA.W	(R0), [R2-R5]
	EOR	R2, R6
	EOR	R3, R7
	MUL	R9, R6
	MUL	R9, R7
	ADD	R8@>13, R2, R8
	ADD	R8@>13, R3, R8
	EOR	R4, R6
	EOR	R5, R7
	MUL	R9, R6
	MUL	R9, R7
	ADD	R8@>13, R4, R8
	ADD	R8@>13, R5, R8
	MOVM.IA.W	(R0), [R2-R5]
	EOR	R2, R6
	EOR	R3, R7
	MUL	R9, R6
	MUL	R9, R7
	ADD	R8@>13, R2, R8
	ADD	R8@>13, R3, R8
	EOR	R4, R6
	EOR	R5, R7
	MUL	R9, R6
	MUL	R9, R7
	ADD	R8@>13, R4, R8
	ADD	R8@>13, R5, R8
	SUB	$8, R1
	B	eight

two:
	CMP	$2, R1
	BLT	one
	MOVM.IA.W	(R0), [R2-R3]
	EOR	R2, R6
	EOR	R3, R7
	MUL	R9, R6
	MUL	R9, R7
	ADD	R8@>13, R2, R8
	ADD	R8@>13, R3, R8
	SUB	$2, R1
	B	two

one:
	CMP	$0, R1
	BEQ	done
	MOVW	(R0), R2
	EOR	R2, R6
	MUL	R9, R6
	ADD	R8@>13, R2, R8

done:
	// hi = a0 ^ a1*mulB, lo = c
	MOVW	$0x85ebca77, R2
	MUL	R2, R7
	EOR	R7, R6
	MOVW	R8, ret_lo+8(FP)
	MOVW	R6, ret_hi+12(FP)
	RET
