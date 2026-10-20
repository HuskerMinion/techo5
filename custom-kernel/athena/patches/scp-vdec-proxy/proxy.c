// SPDX-License-Identifier: GPL-2.0
/*
 * proxy.c: runs on the MT8183's SCP (Cortex-M4), linked into linux-firmware's mediatek/mt8183/scp.img
 * by patches/scp-vdec-proxy.py, so that the firmware's H.264 decoder reaches the decoder's registers
 * through the AP; and with it the encoder, the ISP's pass 2 and the face detector.
 *
 * On athena Fire OS's secure firmware does not let the SCP reach the media blocks' registers (DEVAPC):
 * its accesses are dropped. The decoder's firmware reads registers back as it works (the bitstream
 * parser), so its writes cannot just be replayed afterwards. Instead:
 *
 *  - The IPI handlers of the blocks served (the H.264 decoder and encoder, the ISP's pass 2, the face
 *    detector) are entered through trampolines (ipi_trampoline_N) that first make the multimedia
 *    blocks' registers, as
 *    the SCP addresses them, a no-access MPU region (7; the firmware uses 0-6): 64 MiB from
 *    0xa4000000 (AP 0x14000000-0x17ffffff: MMSYS, IMGSYS with the face detector at 0xa502b000,
 *    VDEC_SYS, whose clock bits and 0x18 the decoder's firmware sets as it starts each frame, the
 *    decoder at 0xa6020000, VENC). Every access the SCP makes there was dropped (DEVAPC) anyway.
 *  - Every load or store there then takes a MemManage fault. mm_fault() decodes the Thumb load/store,
 *    posts the access to a mailbox in uncached DRAM and spins until the AP has done it on the real
 *    register (drivers/remoteproc/mtk_scp_mmproxy.c, patches/scp-mmproxy.py, serves the blocks it
 *    knows and refuses the rest), puts a load's value in the destination register and steps over
 *    the instruction. An access nobody answers is given up after about a second, reading 0, as
 *    DEVAPC dropped it.
 *
 * Mailbox (32-bit words), at SCP 0x304ff000 = AP 0x504ff000, the last page of the firmware's DRAM:
 *   [0] request sequence   (SCP writes last)
 *   [1] op: bit 0 write, bits 4-7 size in bytes
 *   [2] AP physical address
 *   [3] value to write
 *   [4] answered sequence  (AP writes last)
 *   [5] value read
 *   [8] [9] an instruction proxy.c cannot decode: its address and its halfwords (then it stops)
 */
#include <stdint.h>

#define REG32(a)	(*(volatile uint32_t *)(a))
#define MBOX		((volatile uint32_t *)0x304ff000u)

#define SCB_SHCSR	0xe000ed24u
#define SCB_CFSR	0xe000ed28u
#define SCB_MMFAR	0xe000ed34u
#define MPU_RNR		0xe000ed98u
#define MPU_RBAR	0xe000ed9cu
#define MPU_RASR	0xe000eda0u

#define MM_SCP		0xa4000000u	/* the multimedia blocks as the SCP sees them (0xa... = AP 0x1...) */
#define MM_SIZE_LOG2	26

#define GIVE_UP			5000000u	/* mailbox polls, about a second */

struct frame {	/* what the core stacks on exception entry */
	uint32_t r[4], r12, lr, pc, xpsr;
};

static uint32_t seq;
static uint32_t armed;

static void arm(void)
{
	if (armed)
		return;
	REG32(MPU_RNR) = 7;
	REG32(MPU_RBAR) = MM_SCP;
	/* XN, AP=000 (no access), size 2^16, enable */
	REG32(MPU_RASR) = (1u << 28) | ((MM_SIZE_LOG2 - 1) << 1) | 1u;
	REG32(SCB_SHCSR) |= 1u << 16;	/* MemManage faults enabled */
	__asm volatile("dsb\n\tisb" ::: "memory");
	armed = 1;
}

typedef void (*ipi_handler_t)(int32_t id, void *data, uint32_t len);

/* The firmware's own handlers, by IPI id: scp-vdec-proxy-fw.py fills in the ones it routes here */
uint32_t ipi_original[16] = { 1 };	/* not zero: kept out of .bss, so the patch's words are in the image */

#define TRAMPOLINE(n)								\
	void ipi_trampoline_##n(int32_t id, void *data, uint32_t len)		\
	{									\
		arm();								\
		((ipi_handler_t)ipi_original[n])(id, data, len);		\
	}
TRAMPOLINE(1)	/* SCP_IPI_VDEC_H264 */
TRAMPOLINE(4)	/* SCP_IPI_VENC_H264 */
TRAMPOLINE(9)	/* SCP_IPI_DIP */
TRAMPOLINE(12)	/* SCP_IPI_FD_CMD */

static uint32_t ap_access(int write, uint32_t addr, uint32_t val, int size)
{
	MBOX[1] = (write ? 1u : 0u) | ((uint32_t)size << 4);
	MBOX[2] = addr - 0xa0000000u + 0x10000000u;
	MBOX[3] = val;
	__asm volatile("dsb" ::: "memory");
	MBOX[0] = ++seq;
	__asm volatile("dsb" ::: "memory");
	for (uint32_t n = 0; MBOX[4] != seq; n++)
		if (n == GIVE_UP)
			return 0;	/* nobody answered: as DEVAPC dropped it */
	return MBOX[5];
}

static uint32_t *reg_ptr(struct frame *f, uint32_t *hi, unsigned int n)
{
	if (n < 4)
		return &f->r[n];
	if (n < 12)
		return &hi[n - 4];
	if (n == 12)
		return &f->r12;
	if (n == 14)
		return &f->lr;
	return 0;	/* sp, pc: not used for registers */
}

static void stop(struct frame *f, uint32_t insn)
{
	MBOX[8] = f->pc;
	MBOX[9] = insn;
	__asm volatile("dsb" ::: "memory");
	for (;;)
		;
}

static void one(struct frame *f, uint32_t *hi, unsigned int rt, int load, int size, int sign,
		uint32_t addr)
{
	uint32_t *r = reg_ptr(f, hi, rt);

	if (!r)
		stop(f, 0xdead0000u | rt);
	if (load) {
		uint32_t v = ap_access(0, addr, 0, size);

		if (sign && size == 1)
			v = (uint32_t)(int32_t)(int8_t)v;
		else if (sign && size == 2)
			v = (uint32_t)(int32_t)(int16_t)v;
		*r = v;
	} else {
		ap_access(1, addr, *r, size);
	}
}

/*
 * Step the stacked IT state past the instruction done here, as the core does after each instruction
 * of an IT block (ARMv7-M ITAdvance()): the firmware has its loads in IT blocks (itttt eq; ldreq ...),
 * and returning with the state unchanged would run the instructions after it under the wrong conditions.
 * ITSTATE is xPSR bits 26:25 (IT[1:0]) and 15:10 (IT[7:2]).
 */
static void it_advance(struct frame *f)
{
	uint32_t x = f->xpsr, it = ((x >> 25) & 3u) | ((x >> 8) & 0xfcu);

	if (!it)
		return;
	it = (it & 7u) ? (it & 0xe0u) | ((it << 1) & 0x1fu) : 0;
	f->xpsr = (x & ~((3u << 25) | (0x3fu << 10))) | ((it & 3u) << 25) | ((it >> 2) << 10);
}

void mm_fault(struct frame *f, uint32_t *hi)
{
	uint32_t cfsr = REG32(SCB_CFSR);
	uint32_t addr = REG32(SCB_MMFAR);
	uint16_t h1 = *(volatile uint16_t *)(f->pc & ~1u);
	uint16_t h2 = *(volatile uint16_t *)((f->pc & ~1u) + 2);
	unsigned int len = 2;

	if (!(cfsr & 0x80u) || addr - MM_SCP >= (1u << MM_SIZE_LOG2))
		stop(f, 0xfa000000u | (cfsr & 0xffu));

	if ((h1 & 0xe000u) == 0x6000u || (h1 & 0xf000u) == 0x8000u) {
		/* LDR/STR(B,H) Rt, [Rn, #imm] (16-bit) */
		int load = (h1 >> 11) & 1;
		int size = (h1 & 0xf000u) == 0x8000u ? 2 : ((h1 & 0xf000u) == 0x7000u ? 1 : 4);

		one(f, hi, h1 & 7u, load, size, 0, addr);
	} else if ((h1 & 0xf000u) == 0x5000u) {
		/* LDR/STR register offset (16-bit) */
		static const int8_t sz[8] = { 4, 2, 1, 1, 4, 2, 1, 2 };
		unsigned int op = (h1 >> 9) & 7u;

		one(f, hi, h1 & 7u, op >= 3, sz[op], op == 3 || op == 7, addr);
	} else if ((h1 & 0xfe00u) == 0xf800u) {
		/* LDR/STR(B,H,SB,SH).W (32-bit), possibly with writeback */
		unsigned int rn = h1 & 0xfu, rt = h2 >> 12;
		int load = (h1 >> 4) & 1, size = 1 << ((h1 >> 5) & 3), sign = (h1 >> 8) & 1;

		len = 4;
		one(f, hi, rt, load, size, sign, addr);
		if (!(h1 & 0x80u) && (h2 & 0x800u) && (h2 & 0x100u)) {	/* imm8 with W */
			uint32_t *b = reg_ptr(f, hi, rn), imm = h2 & 0xffu;

			if (!b)
				stop(f, ((uint32_t)h2 << 16) | h1);
			*b = (h2 & 0x200u) ? *b + imm : *b - imm;
		}
	} else if ((h1 & 0xfe40u) == 0xe840u) {
		/* LDRD/STRD (32-bit), possibly with writeback */
		unsigned int rn = h1 & 0xfu, rt = h2 >> 12, rt2 = (h2 >> 8) & 0xfu;
		int load = (h1 >> 4) & 1;

		len = 4;
		one(f, hi, rt, load, 4, 0, addr);
		one(f, hi, rt2, load, 4, 0, addr + 4);
		if (h1 & 0x20u) {
			uint32_t *b = reg_ptr(f, hi, rn), imm = (h2 & 0xffu) << 2;

			if (!b)
				stop(f, ((uint32_t)h2 << 16) | h1);
			*b = (h1 & 0x80u) ? *b + imm : *b - imm;
		}
	} else {
		stop(f, ((uint32_t)h2 << 16) | h1);
	}

	REG32(SCB_CFSR) = 0xffu;	/* clear the MemManage status (write 1 to clear) */
	f->pc += len;
	it_advance(f);
}

/* The MemManage vector: hand the stacked frame and the live r4-r11 to mm_fault */
__attribute__((naked)) void mm_entry(void)
{
	__asm volatile(
		"tst lr, #4\n\t"
		"ite eq\n\t"
		"mrseq r0, msp\n\t"
		"mrsne r0, psp\n\t"
		"push {r4-r11, lr}\n\t"
		"mov r1, sp\n\t"
		"bl mm_fault\n\t"
		"pop {r4-r11, lr}\n\t"
		"bx lr\n\t");
}
