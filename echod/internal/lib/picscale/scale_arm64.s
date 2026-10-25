//go:build arm64 && !noasm

#include "textflag.h"

// Go's assembler has no NEON integer multiplies or narrowing shifts, so those are WORDs, each with the
// instruction it is in a comment. Everything else is the assembler's own.
//
// Each function computes exactly what its Go counterpart in picscale.go does, sample for sample:
// SQRSHRN and SQRSHRUN round by adding half before they shift, as the Go code does, and UQXTN after
// SQRSHRUN clamps to 0..255 as clamp does.

// func verticalNEON(mid *int16, src *byte, stride int, w *int16, taps int, n16 int)
//
// Sixteen columns at a time: each tap's row widened to 16 bits and multiplied into four accumulators
// of four 32-bit lanes, then narrowed to mid's fixed point.
TEXT ·verticalNEON(SB), NOSPLIT, $0-48
	MOVD	mid+0(FP), R0
	MOVD	src+8(FP), R1
	MOVD	stride+16(FP), R2
	MOVD	w+24(FP), R3
	MOVD	taps+32(FP), R4
	MOVD	n16+40(FP), R5

columns:
	VEOR	V16.B16, V16.B16, V16.B16
	VEOR	V17.B16, V17.B16, V17.B16
	VEOR	V18.B16, V18.B16, V18.B16
	VEOR	V19.B16, V19.B16, V19.B16
	MOVD	R1, R6
	MOVD	R3, R7
	MOVD	R4, R8

tap:
	VLD1	(R6), [V0.B16]
	ADD	R2, R6
	VLD1R.P	2(R7), [V1.H8]
	VUXTL	V0.B8, V2.H8
	VUXTL2	V0.B16, V3.H8
	WORD	$0x0e618050 // smlal v16.4s, v2.4h, v1.4h
	WORD	$0x4e618051 // smlal2 v17.4s, v2.8h, v1.8h
	WORD	$0x0e618072 // smlal v18.4s, v3.4h, v1.4h
	WORD	$0x4e618073 // smlal2 v19.4s, v3.8h, v1.8h
	SUBS	$1, R8
	BNE	tap

	WORD	$0x0f169e04 // sqrshrn v4.4h, v16.4s, #10
	WORD	$0x4f169e24 // sqrshrn2 v4.8h, v17.4s, #10
	WORD	$0x0f169e45 // sqrshrn v5.4h, v18.4s, #10
	WORD	$0x4f169e65 // sqrshrn2 v5.8h, v19.4s, #10
	VST1.P	[V4.H8, V5.H8], 32(R0)
	ADD	$16, R1
	SUBS	$1, R5
	BNE	columns
	RET

// func horizontalNEON(out *byte, mid *int16, start *int32, w *int16, n8 int)
//
// Eight outputs at a time, each eight taps: mid is already offset so that start indexes it. Each
// output is one multiply and one multiply-accumulate into four lanes; pairwise adds fold the eight
// outputs' lanes into two vectors of sums, which narrow to bytes. All sixteen loads come before the
// arithmetic, so that on the Show 5's in-order cores no multiply waits on the load before it.
TEXT ·horizontalNEON(SB), NOSPLIT, $0-40
	MOVD	out+0(FP), R0
	MOVD	mid+8(FP), R1
	MOVD	start+16(FP), R2
	MOVD	w+24(FP), R3
	MOVD	n8+32(FP), R4

eight:
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V0.H8]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V1.H8]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V2.H8]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V3.H8]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V4.H8]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V5.H8]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V6.H8]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V7.H8]
	VLD1.P	64(R3), [V8.H8, V9.H8, V10.H8, V11.H8]
	VLD1.P	64(R3), [V12.H8, V13.H8, V14.H8, V15.H8]

	WORD	$0x0e68c010 // smull v16.4s, v0.4h, v8.4h
	WORD	$0x0e69c031 // smull v17.4s, v1.4h, v9.4h
	WORD	$0x0e6ac052 // smull v18.4s, v2.4h, v10.4h
	WORD	$0x0e6bc073 // smull v19.4s, v3.4h, v11.4h
	WORD	$0x0e6cc094 // smull v20.4s, v4.4h, v12.4h
	WORD	$0x0e6dc0b5 // smull v21.4s, v5.4h, v13.4h
	WORD	$0x0e6ec0d6 // smull v22.4s, v6.4h, v14.4h
	WORD	$0x0e6fc0f7 // smull v23.4s, v7.4h, v15.4h
	WORD	$0x4e688010 // smlal2 v16.4s, v0.8h, v8.8h
	WORD	$0x4e698031 // smlal2 v17.4s, v1.8h, v9.8h
	WORD	$0x4e6a8052 // smlal2 v18.4s, v2.8h, v10.8h
	WORD	$0x4e6b8073 // smlal2 v19.4s, v3.8h, v11.8h
	WORD	$0x4e6c8094 // smlal2 v20.4s, v4.8h, v12.8h
	WORD	$0x4e6d80b5 // smlal2 v21.4s, v5.8h, v13.8h
	WORD	$0x4e6e80d6 // smlal2 v22.4s, v6.8h, v14.8h
	WORD	$0x4e6f80f7 // smlal2 v23.4s, v7.8h, v15.8h

	VADDP	V17.S4, V16.S4, V16.S4
	VADDP	V19.S4, V18.S4, V18.S4
	VADDP	V21.S4, V20.S4, V20.S4
	VADDP	V23.S4, V22.S4, V22.S4
	VADDP	V18.S4, V16.S4, V16.S4
	VADDP	V22.S4, V20.S4, V20.S4
	WORD	$0x2f108e18 // sqrshrun v24.4h, v16.4s, #16
	WORD	$0x6f108e98 // sqrshrun2 v24.8h, v20.4s, #16
	WORD	$0x2e214b18 // uqxtn v24.8b, v24.8h
	VST1.P	[V24.B8], 8(R0)
	SUBS	$1, R4
	BNE	eight
	RET

// func horizontal4NEON(out *byte, mid *int16, start *int32, w *int16, n2 int)
//
// Four channels interleaved, two outputs at a time, each eight taps: a tap is one pixel's four
// channels times one weight, the even taps and the odd ones in accumulators of their own so the two
// outputs are four independent chains.
TEXT ·horizontal4NEON(SB), NOSPLIT, $0-40
	MOVD	out+0(FP), R0
	MOVD	mid+8(FP), R1
	MOVD	start+16(FP), R2
	MOVD	w+24(FP), R3
	MOVD	n2+32(FP), R4

two:
	MOVW.P	4(R2), R6
	ADD	R6<<3, R1, R6
	VLD1	(R6), [V0.H8, V1.H8, V2.H8, V3.H8]
	MOVW.P	4(R2), R6
	ADD	R6<<3, R1, R6
	VLD1	(R6), [V4.H8, V5.H8, V6.H8, V7.H8]
	VLD1.P	32(R3), [V8.H8, V9.H8]

	WORD	$0x0f48a010 // smull v16.4s, v0.4h, v8.h[0]
	WORD	$0x4f58a011 // smull2 v17.4s, v0.8h, v8.h[1]
	WORD	$0x0f49a092 // smull v18.4s, v4.4h, v9.h[0]
	WORD	$0x4f59a093 // smull2 v19.4s, v4.8h, v9.h[1]
	WORD	$0x0f682030 // smlal v16.4s, v1.4h, v8.h[2]
	WORD	$0x4f782031 // smlal2 v17.4s, v1.8h, v8.h[3]
	WORD	$0x0f6920b2 // smlal v18.4s, v5.4h, v9.h[2]
	WORD	$0x4f7920b3 // smlal2 v19.4s, v5.8h, v9.h[3]
	WORD	$0x0f482850 // smlal v16.4s, v2.4h, v8.h[4]
	WORD	$0x4f582851 // smlal2 v17.4s, v2.8h, v8.h[5]
	WORD	$0x0f4928d2 // smlal v18.4s, v6.4h, v9.h[4]
	WORD	$0x4f5928d3 // smlal2 v19.4s, v6.8h, v9.h[5]
	WORD	$0x0f682870 // smlal v16.4s, v3.4h, v8.h[6]
	WORD	$0x4f782871 // smlal2 v17.4s, v3.8h, v8.h[7]
	WORD	$0x0f6928f2 // smlal v18.4s, v7.4h, v9.h[6]
	WORD	$0x4f7928f3 // smlal2 v19.4s, v7.8h, v9.h[7]

	VADD	V17.S4, V16.S4, V16.S4
	VADD	V19.S4, V18.S4, V18.S4
	WORD	$0x2f108e10 // sqrshrun v16.4h, v16.4s, #16
	WORD	$0x6f108e50 // sqrshrun2 v16.8h, v18.4s, #16
	WORD	$0x2e214a10 // uqxtn v16.8b, v16.8h
	VST1.P	[V16.B8], 8(R0)
	SUBS	$1, R4
	BNE	two
	RET

// func toRGBANEON(row *byte, y, cb, cr *byte, n8 int)
//
// Eight pixels at a time, in color.YCbCrToRGB's own 32-bit arithmetic: Y times 0x10101 plus the
// chroma terms, then shifted down 16 with SQSHRUN and UQXTN, which clamp below zero and above 255 as
// clamp16 does. VST4 interleaves the three colors with opaque alpha.
TEXT ·toRGBANEON(SB), NOSPLIT, $0-40
	MOVD	row+0(FP), R0
	MOVD	y+8(FP), R1
	MOVD	cb+16(FP), R2
	MOVD	cr+24(FP), R3
	MOVD	n8+32(FP), R4

	// V31 is the four constants, V30.S[0] the fifth, V29 128 in each lane, V27 opaque alpha.
	MOVW	$0x10101, R5
	VMOV	R5, V31.S[0]
	MOVW	$91881, R5
	VMOV	R5, V31.S[1]
	MOVW	$-22554, R5
	VMOV	R5, V31.S[2]
	MOVW	$-46802, R5
	VMOV	R5, V31.S[3]
	MOVW	$116130, R5
	VMOV	R5, V30.S[0]
	MOVW	$128, R5
	VDUP	R5, V29.H8
	MOVW	$255, R5
	VDUP	R5, V27.B8

eight:
	VLD1.P	8(R1), [V0.B8]
	VLD1.P	8(R2), [V1.B8]
	VLD1.P	8(R3), [V2.B8]
	VUXTL	V0.B8, V0.H8
	VUXTL	V1.B8, V1.H8
	VUXTL	V2.B8, V2.H8
	VSUB	V29.H8, V1.H8, V1.H8
	VSUB	V29.H8, V2.H8, V2.H8
	VUSHLL	$0, V0.H4, V4.S4
	VUSHLL2	$0, V0.H8, V5.S4
	WORD	$0x0f10a426 // sxtl v6.4s, v1.4h
	WORD	$0x4f10a427 // sxtl2 v7.4s, v1.8h
	WORD	$0x0f10a450 // sxtl v16.4s, v2.4h
	WORD	$0x4f10a451 // sxtl2 v17.4s, v2.8h
	WORD	$0x4f9f8084 // mul v4.4s, v4.4s, v31.s[0]
	WORD	$0x4f9f80a5 // mul v5.4s, v5.4s, v31.s[0]
	VORR	V4.B16, V4.B16, V18.B16
	VORR	V5.B16, V5.B16, V19.B16
	VORR	V4.B16, V4.B16, V20.B16
	VORR	V5.B16, V5.B16, V21.B16
	WORD	$0x6fbf0212 // mla v18.4s, v16.4s, v31.s[1]
	WORD	$0x6fbf0233 // mla v19.4s, v17.4s, v31.s[1]
	WORD	$0x6f9f08d4 // mla v20.4s, v6.4s, v31.s[2]
	WORD	$0x6f9f08f5 // mla v21.4s, v7.4s, v31.s[2]
	WORD	$0x6f9e00c4 // mla v4.4s, v6.4s, v30.s[0]
	WORD	$0x6f9e00e5 // mla v5.4s, v7.4s, v30.s[0]
	WORD	$0x6fbf0a14 // mla v20.4s, v16.4s, v31.s[3]
	WORD	$0x6fbf0a35 // mla v21.4s, v17.4s, v31.s[3]
	WORD	$0x2f108652 // sqshrun v18.4h, v18.4s, #16
	WORD	$0x6f108672 // sqshrun2 v18.8h, v19.4s, #16
	WORD	$0x2f108484 // sqshrun v4.4h, v4.4s, #16
	WORD	$0x6f1084a4 // sqshrun2 v4.8h, v5.4s, #16
	WORD	$0x2f108694 // sqshrun v20.4h, v20.4s, #16
	WORD	$0x6f1086b4 // sqshrun2 v20.8h, v21.4s, #16
	WORD	$0x2e214a58 // uqxtn v24.8b, v18.8h
	WORD	$0x2e214a99 // uqxtn v25.8b, v20.8h
	WORD	$0x2e21489a // uqxtn v26.8b, v4.8h
	VST4.P	[V24.B8, V25.B8, V26.B8, V27.B8], 32(R0)
	SUBS	$1, R4
	BNE	eight
	RET

// func horizontal4tapsNEON(out *byte, mid *int16, start *int32, w *int16, n8 int)
//
// horizontalNEON for filters of four taps, which is what chroma enlarged to the luma's size takes:
// two outputs' samples share a register, so there are half the multiplies.
TEXT ·horizontal4tapsNEON(SB), NOSPLIT, $0-40
	MOVD	out+0(FP), R0
	MOVD	mid+8(FP), R1
	MOVD	start+16(FP), R2
	MOVD	w+24(FP), R3
	MOVD	n8+32(FP), R4

eight:
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V0.H4]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	WORD	$0x4d4084c0 // ld1 {v0.d}[1], [x6]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V1.H4]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	WORD	$0x4d4084c1 // ld1 {v1.d}[1], [x6]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V2.H4]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	WORD	$0x4d4084c2 // ld1 {v2.d}[1], [x6]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	VLD1	(R6), [V3.H4]
	MOVW.P	4(R2), R6
	ADD	R6<<1, R1, R6
	WORD	$0x4d4084c3 // ld1 {v3.d}[1], [x6]
	VLD1.P	64(R3), [V8.H8, V9.H8, V10.H8, V11.H8]

	WORD	$0x0e68c010 // smull v16.4s, v0.4h, v8.4h
	WORD	$0x4e68c011 // smull2 v17.4s, v0.8h, v8.8h
	WORD	$0x0e69c032 // smull v18.4s, v1.4h, v9.4h
	WORD	$0x4e69c033 // smull2 v19.4s, v1.8h, v9.8h
	WORD	$0x0e6ac054 // smull v20.4s, v2.4h, v10.4h
	WORD	$0x4e6ac055 // smull2 v21.4s, v2.8h, v10.8h
	WORD	$0x0e6bc076 // smull v22.4s, v3.4h, v11.4h
	WORD	$0x4e6bc077 // smull2 v23.4s, v3.8h, v11.8h

	VADDP	V17.S4, V16.S4, V16.S4
	VADDP	V19.S4, V18.S4, V18.S4
	VADDP	V21.S4, V20.S4, V20.S4
	VADDP	V23.S4, V22.S4, V22.S4
	VADDP	V18.S4, V16.S4, V16.S4
	VADDP	V22.S4, V20.S4, V20.S4
	WORD	$0x2f108e18 // sqrshrun v24.4h, v16.4s, #16
	WORD	$0x6f108e98 // sqrshrun2 v24.8h, v20.4s, #16
	WORD	$0x2e214b18 // uqxtn v24.8b, v24.8h
	VST1.P	[V24.B8], 8(R0)
	SUBS	$1, R4
	BNE	eight
	RET
