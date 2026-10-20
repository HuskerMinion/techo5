// SPDX-License-Identifier: GPL-2.0-only
/*
 * SODI ("screen-on deep idle") for the MT8163 (the 2nd gen Echo Show 5, cronos): when CPU0 is the
 * only core left powered and has nothing to do, the SPM's PCM powers CPU0, the cluster and MCUSYS
 * off while the display keeps scanning out, and powers them back on at the next wake event.
 *
 * After Amazon's GPL 4.9 tree, drivers/misc/mediatek/base/power/mt8163/{mt_spm_internal.c,
 * mt_spm_sodi.c,mt_cpuidle.c,mt_idle.c} and drivers/misc/mediatek/cirq/mtk_sys_cirq.c
 * (Copyright (C) 2015 MediaTek Inc., GPL-2.0), including the PCM program sodi_binary
 * ("pcm_sodi_v00.04_mt8163_20171227"), which that GPL source carries as an array.
 *
 * The sequence is Fire OS's spm_go_to_sodi(): mask every GIC interrupt but the SPM's IRQ0, let
 * CIRQ latch the others (the GIC goes down with MCUSYS), load and kick the PCM, then PSCI
 * CPU_SUSPEND 0x02010000 (power-down, affinity level 2). The PCM waits for CPU0's WFI in the
 * secure monitor, powers CPU0/CPUTOP/MCUSYS down, waits for a wake event (the GPT that carries the
 * tick broadcast, EINT, CIRQ, the PCM timer), powers them up, and the core warm-boots through the
 * monitor into cpu_resume. See research/mt8163-cpuidle.md, "Stage B".
 *
 * Deliberate differences from Fire OS, all towards doing less:
 *  - DRAM is never put into self-refresh: the PCM keeps its own APSRC request (PCM_SRC_REQ bit 0),
 *    SPM_DDRPHY_PDN_DIS is set and SPM_MEMPLL_RESET is not, so the program's DRAM event vectors
 *    are never enabled. Fire OS only allowed self-refresh when the masters that cannot ask for DRAM
 *    themselves (MSDC, SPI, USB, AP_DMA...) had their clocks off; on this unit Wi-Fi's SDIO host
 *    never does, so Fire OS's own rules would never have let it.
 *  - No CPU voltage scaling: SPM_CPU_DVS_DIS keeps the PCM off its I2C master (it would drive the
 *    sym827 that mainline's cpufreq owns), and every PMIC wrapper DVFS slot points at the MT6323's
 *    read-only chip ID, so a stray DVFS command changes nothing.
 *  - CIRQ is cloned from the GIC's enables as they were before masking (Fire OS cloned after, which
 *    left only IRQ0), so any enabled interrupt (Wi-Fi's SDIO host, I2C, the display) wakes it.
 *  - The PCM timer is armed as a backstop (timer_ms), so no lost wake source can sleep forever.
 *
 * Off unless enabled at run time (mt8163_sodi.enable); it only ever runs with CPU0 the sole online
 * core, whose SPM power status is the only one on, and only while timekeeping runs on the GPT
 * ("timer" clocksource): the arch timer's counter does not survive MCUSYS power-down (see
 * sodi_clock_ok()). Any PCM assert, a PCM that does not finish, or repeated PSCI errors switch it
 * off until the next boot.
 *
 * Measured on a unit (kernel #32): 15 entries a second, 95% residency, no errors over a 30 minute
 * soak with the daemon stopped. With the daemon running it never applies: one core cannot carry
 * its load, so cpuplug keeps two and SODI stays out.
 */
#include <linux/cpu.h>
#include <linux/cpu_pm.h>
#include <linux/cpuidle.h>
#include <linux/delay.h>
#include <linux/io.h>
#include <linux/iopoll.h>
#include <linux/irqchip/arm-gic.h>
#include <linux/jiffies.h>
#include <linux/module.h>
#include <linux/moduleparam.h>
#include <linux/mutex.h>
#include <linux/of.h>
#include <linux/interrupt.h>
#include <linux/of_address.h>
#include <linux/of_irq.h>
#include <linux/psci.h>
#include <linux/reboot.h>
#include <linux/slab.h>
#include <linux/timekeeping.h>
#include <linux/workqueue.h>
#include <asm/cacheflush.h>
#include <asm/cpuidle.h>
#include <asm/proc-fns.h>

/* SPM (0x10006000) */
#define SPM_POWERON_CONFIG_SET	0x000
#define SPM_POWER_ON_VAL0	0x010
#define SPM_POWER_ON_VAL1	0x014
#define SPM_PCM_CON0		0x310
#define SPM_PCM_CON1		0x314
#define SPM_PCM_IM_PTR		0x318
#define SPM_PCM_IM_LEN		0x31c
#define SPM_PCM_REG_DATA_INI	0x320
#define SPM_PCM_EVENT_VECTOR0	0x340
#define SPM_PCM_EVENT_VECTOR1	0x344
#define SPM_PCM_EVENT_VECTOR2	0x348
#define SPM_PCM_EVENT_VECTOR3	0x34c
#define SPM_PCM_MAS_PAUSE_MASK	0x354
#define SPM_PCM_PWR_IO_EN	0x358
#define SPM_PCM_TIMER_VAL	0x35c
#define SPM_PCM_TIMER_OUT	0x360
#define SPM_PCM_REG12_DATA	0x3b0
#define SPM_PCM_REG13_DATA	0x3b4
#define SPM_PCM_REG15_DATA	0x3bc
#define SPM_PCM_EVENT_REG_STA	0x3c0
#define SPM_PCM_FSM_STA		0x3c4
#define SPM_PCM_EVENT_VECTOR4	0x3d0
#define SPM_PCM_EVENT_VECTOR5	0x3d4
#define SPM_PCM_EVENT_VECTOR6	0x3d8
#define SPM_PCM_EVENT_VECTOR7	0x3dc
#define SPM_PCM_SW_INT_CLEAR	0x3e4
#define SPM_CLK_CON		0x400
#define SPM_AP_STANBY_CON	0x608
#define SPM_PWR_STATUS		0x60c
#define SPM_PWR_STATUS_2ND	0x610
#define SPM_SLEEP_WAKEUP_EVENT_MASK	0x810
#define SPM_SLEEP_CPU_WAKEUP_EVENT	0x814
#define SPM_SLEEP_ISR_MASK	0x900
#define SPM_SLEEP_ISR_STATUS	0x904
#define SPM_SLEEP_ISR_RAW_STA	0x910
#define SPM_SLEEP_WAKEUP_MISC	0x918
#define SPM_SLEEP_SUBSYS_IDLE_STA	0x920
#define SPM_PCM_RESERVE		0xb00
#define SPM_PCM_FLAGS		0xb08
#define SPM_PCM_SRC_REQ		0xb0c
#define SPM_PCM_PASR_DPD_2	0xb68
#define SPM_PCM_PASR_DPD_3	0xb6c
#define SPM_SLEEP_CA7_WFI0_EN	0xf00
#define SPM_SLEEP_CA15_WFI0_EN	0xf10

#define SPM_PROJECT_CODE	0xb16
#define CFG_KEY			(SPM_PROJECT_CODE << 16)

#define CON0_PCM_KICK		BIT(0)
#define CON0_IM_KICK		BIT(1)
#define CON0_IM_SLEEP_DVS	BIT(3)
#define CON0_PCM_SW_RESET	BIT(15)
#define CON1_IM_SLAVE		BIT(0)
#define CON1_MIF_APBEN		BIT(3)
#define CON1_PCM_TIMER_EN	BIT(5)
#define CON1_IM_NONRP_EN	BIT(6)
#define CON1_PCM_WDT_EN		BIT(8)
#define CON1_PCM_WDT_WAKE_MODE	BIT(9)
#define CON1_SPM_SRAM_SLP_B	BIT(10)
#define CON1_SPM_SRAM_ISO_B	BIT(11)
#define CON1_EVENT_LOCK_EN	BIT(12)
#define CON1_MD32_APB_INTERNAL_EN BIT(14)

#define PCM_PWRIO_EN_R0		BIT(0)
#define PCM_PWRIO_EN_R7		BIT(7)
#define PCM_RF_SYNC_R0		BIT(16)
#define PCM_RF_SYNC_R7		BIT(23)
#define PCM_SW_INT_ALL		0xff
#define CC_LOCK_INFRA_DCM	BIT(5)
#define CC_DISABLE_DORM_PWR	BIT(14)
#define ASC_SRCCLKENI_MASK	BIT(25)
#define ISRM_TWAM		BIT(2)
#define ISRM_PCM_RETURN		BIT(3)
#define ISRM_RET_IRQ0		BIT(8)
#define ISRM_RET_IRQ_1_7	(0x7f << 9)
#define ISRM_ALL_EXC_TWAM	(ISRM_RET_IRQ_1_7 | ISRM_RET_IRQ0 | ISRM_PCM_RETURN)
#define ISRS_PCM_RETURN		BIT(3)
#define ISRS_SW_INT0		BIT(4)
#define PCM_FSM_STA_DEF		0x48490

/* PCM_FLAGS, as mt_spm.h names them; the program tests bits 0, 2, 6, 7, 9, 12, 13 and 24 */
#define SPM_CPU_PDN_DIS		BIT(0)
#define SPM_DDRPHY_PDN_DIS	BIT(2)
#define SPM_VCORE_DVS_DIS	BIT(3)
#define SPM_MEMPLL_RESET	BIT(7)
#define SPM_CPU_DVS_DIS		BIT(9)
#define SPM_DDR_HIGH_SPEED	BIT(12)
#define SPM_SCREEN_OFF		BIT(13)
#define SPM_LOW_SPD_I2C		BIT(24)

/* wake sources (mt_spm.h), WAKE_SRC_FOR_SODI without SYSPWREQ */
#define WAKE_SRC_MD32_WDT	BIT(1)
#define WAKE_SRC_KP		BIT(2)
#define WAKE_SRC_GPT		BIT(4)
#define WAKE_SRC_CONN2AP	BIT(5)
#define WAKE_SRC_EINT		BIT(6)
#define WAKE_SRC_CONN_WDT	BIT(7)
#define WAKE_SRC_MD32_SPM	BIT(10)
#define WAKE_SRC_USB_CD		BIT(14)
#define WAKE_SRC_USB_PDN	BIT(15)
#define WAKE_SRC_AFE		BIT(20)
#define WAKE_SRC_CIRQ		BIT(22)
#define WAKE_SRC_SODI	(WAKE_SRC_MD32_WDT | WAKE_SRC_KP | WAKE_SRC_GPT | WAKE_SRC_CONN2AP | \
			 WAKE_SRC_EINT | WAKE_SRC_CONN_WDT | WAKE_SRC_MD32_SPM | WAKE_SRC_USB_CD | \
			 WAKE_SRC_USB_PDN | WAKE_SRC_AFE | WAKE_SRC_CIRQ)

#define CORE_PWR_STA_MASK	(0xf << 9)	/* PWR_STATUS bits 9-12: CPU0-3 */
#define CPU0_PWR_STA		BIT(9)

#define EVENT_VEC(event, resume, imme, pc) \
	(((pc) << 16) | (!!(imme) << 6) | (!!(resume) << 5) | ((event) & 0x1f))

/* GIC-400 distributor: 288 interrupts (TYPER.ITLinesNumber 8) */
#define GIC_NR_WORDS		9
#define SPM_IRQ0_HWIRQ		(32 + 163)

/* SYS_CIRQ (0x10204000): 169 inputs from SPI 72 */
#define CIRQ_STA		0x000
#define CIRQ_ACK		0x040
#define CIRQ_MASK		0x080
#define CIRQ_MASK_SET		0x0c0
#define CIRQ_MASK_CLR		0x100
#define CIRQ_SENS_SET		0x180
#define CIRQ_SENS_CLR		0x1c0
#define CIRQ_POL_SET		0x240
#define CIRQ_POL_CLR		0x280
#define CIRQ_CON		0x300
#define CIRQ_CON_EN		BIT(0)
#define CIRQ_CON_EDGE_ONLY	BIT(1)
#define CIRQ_NUM		169
#define CIRQ_HWIRQ0		(32 + 72)
#define CIRQ_WORDS		DIV_ROUND_UP(CIRQ_NUM, 32)

/* PMIC wrapper (0x1000d000) DVFS command slots, and the MT6323's read-only chip ID */
#define PWRAP_DVFS_ADR(n)	(0xe8 + 8 * (n))
#define PWRAP_DVFS_WDATA(n)	(0xec + 8 * (n))
#define MT6323_CID		0x0100

/* infracfg (0x10001000) clock gate status: a set bit is a gated clock */
#define INFRA_PDN_STA0		0x090
#define INFRA_PDN_STA1		0x094
#define INFRA0_GCE		BIT(9)
#define INFRA1_MSDC0		BIT(6)

/* PCM program for SODI, from Amazon's mt_spm_sodi.c (GPL-2.0) */
static const u32 sodi_binary[] = {
	0x81411801, 0xd8000125, 0x17c07c1f, 0x18c0001f, 0x10006240, 0xe0e00016,
	0xe0e0001e, 0xe0e0000e, 0xe0e0000f, 0x803e0400, 0x1b80001f, 0x20000050,
	0x803e8400, 0x803f0400, 0x803f8400, 0x1b80001f, 0x20000208, 0x803d0400,
	0x1b80001f, 0x20000034, 0x80380400, 0xa01d8400, 0x1b80001f, 0x20000034,
	0x803d8400, 0x803b0400, 0x1b80001f, 0x20000158, 0x80340400, 0x80310400,
	0x1b80001f, 0x2000000a, 0x18c0001f, 0x10006240, 0xe0e0000d, 0x81fa0407,
	0x18c0001f, 0x100040f4, 0x1910001f, 0x100040f4, 0xa11c8404, 0xe0c00004,
	0x813c8404, 0xe0c00004, 0x1b80001f, 0x20000100, 0x81f08407, 0xe8208000,
	0x10006354, 0xfff01b01, 0xa1d80407, 0xa1dc0407, 0xa1de8407, 0xa1df0407,
	0x1b00001f, 0xbffce7ff, 0xf0000000, 0x17c07c1f, 0x1b80001f, 0x20000fdf,
	0x1a50001f, 0x10006608, 0x80c9a401, 0x810aa401, 0x10918c1f, 0xa0939002,
	0x80ca2401, 0x810ba401, 0xa09c0c02, 0xa0979002, 0xa0950402, 0x8080080d,
	0xd8200a82, 0x17c07c1f, 0x1b00001f, 0x3ffce7ff, 0x1b80001f, 0x20000004,
	0xd80011cc, 0x17c07c1f, 0x1b00001f, 0xbffce7ff, 0xd00011c0, 0x17c07c1f,
	0x81f80407, 0x81fc0407, 0x81fe8407, 0x81ff0407, 0xe8208000, 0x10006354,
	0xfff01b47, 0x1b80001f, 0x20000020, 0x02000408, 0x1880001f, 0x10006320,
	0xc0c01f40, 0xe080000f, 0xd8000943, 0x17c07c1f, 0xe080001f, 0x1950001f,
	0x10006b00, 0x81429401, 0xd8000ca5, 0x17c07c1f, 0xa1da0407, 0xa0110400,
	0xa0140400, 0xa01b0400, 0xa0180400, 0xa01d0400, 0x1950001f, 0x10006b00,
	0x81431401, 0xd8000e05, 0x17c07c1f, 0xa01f8400, 0xa01f0400, 0xa01e8400,
	0xa01e0400, 0x1b80001f, 0x20000104, 0x1950001f, 0x10006b00, 0x81439401,
	0xd8000f65, 0x17c07c1f, 0x81411801, 0xd80010e5, 0x17c07c1f, 0x18c0001f,
	0x10006240, 0xc0c01d60, 0x17c07c1f, 0x1950001f, 0x10006b00, 0x81441401,
	0xd80010e5, 0x17c07c1f, 0x1b00001f, 0x7ffce7ff, 0xf0000000, 0x17c07c1f,
	0x803d8400, 0x1b80001f, 0x2000001a, 0x80340400, 0x17c07c1f, 0x17c07c1f,
	0x80310400, 0x89c00007, 0xffeffffd, 0xa9c00007, 0x61010000, 0xe8208000,
	0x10006354, 0xfff01b01, 0x1b00001f, 0xbffce7ff, 0x1b00001f, 0xfffcffff,
	0x1b80001f, 0x20000004, 0x8a80000c, 0x2ffce7ff, 0xd820154a, 0x17c07c1f,
	0x1b00001f, 0x3ffce7ff, 0xf0000000, 0x17c07c1f, 0x1990001f, 0x10006b08,
	0x81461801, 0xd8001685, 0x10807c1f, 0x1890001f, 0x10212018, 0x808f8801,
	0x1a50001f, 0x10006608, 0x80c9a401, 0x810aa401, 0xa0918c02, 0xa0939002,
	0x80ca2401, 0x810ba401, 0xa09c0c02, 0xa0979002, 0xa0950402, 0x8080080d,
	0xd80013c2, 0x17c07c1f, 0x1b00001f, 0xfffcffff, 0x1b80001f, 0x20000004,
	0x8a80000c, 0x2ffce7ff, 0xd800150a, 0x17c07c1f, 0xe8208000, 0x10006354,
	0xfff01b47, 0x1b80001f, 0x20000020, 0x02000408, 0x89c00007, 0x9efeffff,
	0xa9c00007, 0x00100002, 0x1b80001f, 0x20000020, 0x1950001f, 0x10006b00,
	0x81449401, 0xd8001ac5, 0x17c07c1f, 0x1940001f, 0x001c0bae, 0xa0110400,
	0xa0140400, 0xa01d8400, 0x1b00001f, 0x7ffce7ff, 0x1950001f, 0x10006b00,
	0x81451401, 0xd8001c45, 0x17c07c1f, 0x1940001f, 0x001c0bae, 0xf0000000,
	0x17c07c1f, 0xe0f07f0d, 0xe0f07f0f, 0xe0f07f1e, 0xe0f07f12, 0xf0000000,
	0x17c07c1f, 0xa1d08407, 0x1b80001f, 0x20000080, 0x812ab401, 0x1900001f,
	0x10006814, 0xe1000003, 0xf0000000, 0x17c07c1f, 0xd0001fe0, 0x11407c1f,
	0x81f08407, 0x1b80001f, 0x20000001, 0xa1d08407, 0x1b80001f, 0x20000020,
	0x80eab401, 0xd8202103, 0x17c07c1f, 0x80c01403, 0xd8201f83, 0x01400405,
	0x1900001f, 0x10006814, 0xf0000000, 0xe1000003, 0xa1d10407, 0x1b80001f,
	0x20000020, 0xf0000000, 0x17c07c1f, 0x18c0001f, 0x10006b6c, 0x1910001f,
	0x10006b6c, 0xa1002804, 0xe0c00004, 0xf0000000, 0x17c07c1f, 0xd8202429,
	0x17c07c1f, 0xe2e0000d, 0xe2e0000c, 0xe2e0001c, 0xe2e0001e, 0xe2e00016,
	0xe2e00012, 0xf0000000, 0x17c07c1f, 0xd82025a9, 0x17c07c1f, 0xe2e00016,
	0x1380201f, 0xe2e0001e, 0x1380201f, 0xe2e0001c, 0x1380201f, 0xe2e0000c,
	0xe2e0000d, 0xf0000000, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f, 0x17c07c1f,
	0x17c07c1f, 0x17c07c1f, 0x1840001f, 0x00000001, 0xa1d48407, 0x1990001f,
	0x10006b08, 0xe8208000, 0x10006310, 0x0b160008, 0xe8208000, 0x10006b6c,
	0x00000000, 0x1b00001f, 0x2ffce7ff, 0x81469801, 0xd8004305, 0x17c07c1f,
	0x1b80001f, 0xd00f0000, 0x8880000c, 0x2ffce7ff, 0xd8005fa2, 0x17c07c1f,
	0xd0004340, 0x17c07c1f, 0x1b80001f, 0x500f0000, 0xe8208000, 0x10006354,
	0xfff01b01, 0x17c07c1f, 0x81401801, 0xd8004a25, 0x17c07c1f, 0x81f60407,
	0x1950001f, 0x10006b00, 0x81401401, 0xd8004445, 0x17c07c1f, 0x18c0001f,
	0x10006200, 0xc0c06100, 0x12807c1f, 0xe8208000, 0x1000625c, 0x00000001,
	0x1b80001f, 0x20000080, 0xc0c06100, 0x1280041f, 0x1950001f, 0x10006b00,
	0x81409401, 0xd8004645, 0x17c07c1f, 0x18c0001f, 0x10006208, 0xc0c06100,
	0x12807c1f, 0x1b80001f, 0x20000003, 0xe8208000, 0x10006248, 0x00000000,
	0x1b80001f, 0x20000080, 0xc0c06100, 0x1280041f, 0x1950001f, 0x10006b00,
	0x81411401, 0xd8004885, 0x17c07c1f, 0x18c0001f, 0x10006290, 0xe0e0004f,
	0xc0c06100, 0x1280041f, 0xe8208000, 0x10006404, 0x00003101, 0x81469801,
	0xd8204b85, 0x17c07c1f, 0x1b00001f, 0x2ffce7ff, 0x1b80001f, 0x30000004,
	0x8880000c, 0x2ffce7ff, 0xd8005b02, 0x17c07c1f, 0xe8208000, 0x10006294,
	0x0003ffff, 0x18c0001f, 0x10006294, 0xe0e03fff, 0xe0e003ff, 0x81449801,
	0xd8004d85, 0x17c07c1f, 0x1a00001f, 0x10006604, 0xc0c06c20, 0x17c07c1f,
	0xc0c06580, 0x12807c1f, 0x1950001f, 0x10006b00, 0x81419401, 0xd8004d85,
	0x17c07c1f, 0x81401801, 0xd8004f25, 0x17c07c1f, 0xa1d38407, 0xa0108400,
	0xa0148400, 0xa01b8400, 0xa0188400, 0x12007c1f, 0xe8208000, 0x10006310,
	0x0b160008, 0x81431801, 0xd8005125, 0x17c07c1f, 0xe8208000, 0x10006310,
	0x0b1600c8, 0x81439801, 0xd8205125, 0x17c07c1f, 0xe8208000, 0x10006310,
	0x0b160038, 0x1b00001f, 0xbffce7ff, 0x1950001f, 0x10006b00, 0x81421401,
	0xd8005165, 0x17c07c1f, 0x1940001f, 0x001c0bae, 0x1950001f, 0x10006b00,
	0x814f9401, 0xd8205325, 0x17c07c1f, 0x1b80001f, 0x00001000, 0x1b80001f,
	0x90100000, 0x1240301f, 0x1ac0001f, 0x10006b6c, 0xe2c00008, 0xe8208000,
	0x10006310, 0x0b160008, 0x82809c01, 0x1990001f, 0x10006b08, 0x81439801,
	0xd82055a5, 0x17c07c1f, 0x828d8001, 0xca80120a, 0x17c07c1f, 0xd00055e0,
	0x17c07c1f, 0xca80000a, 0x17c07c1f, 0x1990001f, 0x10006b08, 0x1b00001f,
	0x3ffce7ff, 0x18c0001f, 0x10006294, 0xe0e007fe, 0xe0e00ffc, 0xe0e01ff8,
	0xe0e03ff0, 0xe0e03fe0, 0xe0e03fc0, 0x1b80001f, 0x20000020, 0xe8208000,
	0x10006294, 0x0003ffc0, 0xe8208000, 0x10006294, 0x0003fc00, 0x81401801,
	0xd8005b05, 0x17c07c1f, 0x81449801, 0xd80059a5, 0x17c07c1f, 0xc0c06580,
	0x1280041f, 0x1b80001f, 0x200016a8, 0x80388400, 0x1b80001f, 0x20000300,
	0x803b8400, 0x1b80001f, 0x20000300, 0x80348400, 0x1b80001f, 0x20000104,
	0x80308400, 0x81f38407, 0x81401801, 0xd8005fa5, 0x17c07c1f, 0xe8208000,
	0x10006404, 0x00000101, 0x18c0001f, 0x10006290, 0x1212841f, 0xc0c06280,
	0x12807c1f, 0xc0c06280, 0x1280041f, 0x18c0001f, 0x10006208, 0x1212841f,
	0xc0c06280, 0x12807c1f, 0xe8208000, 0x10006248, 0x00000001, 0x1b80001f,
	0x20000080, 0xc0c06280, 0x1280041f, 0x18c0001f, 0x10006200, 0x1212841f,
	0xc0c06280, 0x12807c1f, 0xe8208000, 0x1000625c, 0x00000000, 0x1b80001f,
	0x20000080, 0xc0c06280, 0x1280041f, 0x19c0001f, 0x61415820, 0x10007c1f,
	0xe8208000, 0x10006b30, 0x00000000, 0xe8208000, 0x100063e0, 0x00000001,
	0xf0000000, 0x17c07c1f, 0xd80061aa, 0x17c07c1f, 0xe2e0004f, 0xe2e0006f,
	0xe2e0002f, 0xd820624a, 0x17c07c1f, 0xe2e0002e, 0xe2e0003e, 0xe2e00032,
	0xf0000000, 0x17c07c1f, 0xd800638a, 0x17c07c1f, 0xe2e00036, 0x17c07c1f,
	0x17c07c1f, 0xe2e0003e, 0x1380201f, 0xe2e0003c, 0xd82064ca, 0x17c07c1f,
	0x1b80001f, 0x20000018, 0xe2e0007c, 0x1b80001f, 0x20000003, 0xe2e0005c,
	0xe2e0004c, 0xe2e0004d, 0xf0000000, 0x17c07c1f, 0x1b80001f, 0x20000300,
	0xf0000000, 0x17c07c1f, 0x1a10001f, 0x10059824, 0xd8006588, 0x17c07c1f,
	0x1a00001f, 0x10059838, 0xe2200001, 0xe8208000, 0x10059814, 0x00000002,
	0xe8208000, 0x10059820, 0x00000011, 0xe8208000, 0x10059848, 0x00000000,
	0xe8208000, 0x10059810, 0x00000028, 0xe8208000, 0x10059804, 0x000000c0,
	0x1a00001f, 0x10059800, 0xd8206b0a, 0x17c07c1f, 0xe2200001, 0x1950001f,
	0x10006b04, 0xe2000005, 0xe8208000, 0x10059824, 0x00000001, 0x814c1801,
	0xd8006a85, 0x17c07c1f, 0x1b80001f, 0x20000524, 0xd0006ac0, 0x17c07c1f,
	0x1b80001f, 0x200066cb, 0xd0006be0, 0x17c07c1f, 0xe2200001, 0xe2200094,
	0xe8208000, 0x10059824, 0x00000001, 0x1b80001f, 0x20000524, 0xf0000000,
	0x17c07c1f, 0xe8208000, 0x10059838, 0x00000001, 0xe8208000, 0x1005980c,
	0x0000001f, 0xe8208000, 0x10059820, 0x00000011, 0xe8208000, 0x10059848,
	0x00000000, 0xe8208000, 0x10059810, 0x0000003a, 0xe8208000, 0x10059804,
	0x000000c0, 0xe8208000, 0x10059814, 0x00000001, 0xe8208000, 0x1005986c,
	0x00000001, 0xe8208000, 0x10059818, 0x00000002, 0x1a00001f, 0x10059800,
	0xe2000001, 0xe8208000, 0x10059824, 0x00000001, 0x8a100001, 0x1005980c,
	0xd8207048, 0x17c07c1f, 0x1950001f, 0x10059800, 0x1a00001f, 0x10006b04,
	0xe2000005, 0xe8208000, 0x10059810, 0x00000000, 0xe8208000, 0x10059818,
	0x00000001, 0xf0000000, 0x17c07c1f,
};

#define SODI_VERSION	"pcm_sodi_v00.04_mt8163_20171227"
#define SODI_SESS	2
#define PSCI_CLUSTER_PDN	0x02010000	/* power-down, affinity level 2: Fire OS's state 2 */

static bool enable;
static bool cpu_pdn = true;
static unsigned int timer_ms = 2000;
static unsigned int deadman;
static unsigned int trace;

static void __iomem *spm, *cirq, *gicd, *sysirq, *pwrap, *infra, *gpt;
static u32 *pcm_im;		/* sodi_binary, in memory the PCM can fetch */
static u32 pcm_im_pa;
static bool ready, failed;
static DEFINE_MUTEX(sodi_mutex);
static unsigned long keepalive_at;

static struct {
	unsigned long tries, entered, powered, aborted, psci_err, fallback, blocked_cpu,
		blocked_emmc, blocked_clock, pcm_late, fireos_clk_block;
	unsigned long wake_bits[32];
	u32 last_r12, last_raw, last_misc, last_timer_out, last_debug, last_event, last_isr, last_pc;
	int last_ret;
} st;

static inline u32 spm_rd(u32 off) { return readl_relaxed(spm + off); }
static inline void spm_wr(u32 off, u32 v) { writel(v, spm + off); }

/* Fire OS's soidle_condition_mask for INFRA0/INFRA1: counted, not enforced (see the header). */
#define FIREOS_INFRA0_MASK	0x07a73980
#define FIREOS_INFRA1_MASK	0x00440076

static bool sodi_cores_alone(void)
{
	u32 a = spm_rd(SPM_PWR_STATUS) & CORE_PWR_STA_MASK;
	u32 b = spm_rd(SPM_PWR_STATUS_2ND) & CORE_PWR_STA_MASK;

	return num_online_cpus() == 1 && smp_processor_id() == 0 &&
	       a == CPU0_PWR_STA && b == CPU0_PWR_STA;
}

/* --- CIRQ, after mtk_sys_cirq.c, with the GIC state passed in rather than read back */

static u32 gic_pending_vec(unsigned int start)
{
	unsigned int reg = start / 32, sh = start % 32;
	u32 lo = readl_relaxed(gicd + GIC_DIST_PENDING_SET + reg * 4);

	if (!sh)
		return lo;
	return (lo >> sh) | (readl_relaxed(gicd + GIC_DIST_PENDING_SET + (reg + 1) * 4) << (32 - sh));
}

static u32 cirq_valid(unsigned int w)
{
	return w == CIRQ_WORDS - 1 && CIRQ_NUM % 32 ? GENMASK(CIRQ_NUM % 32 - 1, 0) : ~0u;
}

static void cirq_ack_all(void)
{
	unsigned int i;

	for (i = 0; i < CIRQ_WORDS; i++) {
		u32 pend = gic_pending_vec(CIRQ_HWIRQ0 + i * 32);
		u32 mask = readl_relaxed(cirq + CIRQ_MASK + i * 4);

		writel_relaxed(~pend | mask, cirq + CIRQ_ACK + i * 4);
	}
	mb();
}

/* bit c of GIC word-array v, for CIRQ input c */
static u32 gic_bits(const u32 *v, unsigned int words, unsigned int cbase)
{
	unsigned int hw = CIRQ_HWIRQ0 + cbase, reg = hw / 32, sh = hw % 32;
	u32 lo = reg < words ? v[reg] : 0, hi = reg + 1 < words ? v[reg + 1] : 0;

	return sh ? (lo >> sh) | (hi << (32 - sh)) : lo;
}

static void cirq_clone(const u32 *enabled)
{
	u32 cfg_edge[GIC_NR_WORDS] = {}, pol[GIC_NR_WORDS] = {};
	unsigned int i, irq;

	/* GIC ICFGR: two bits per interrupt, bit 1 set = edge */
	for (irq = CIRQ_HWIRQ0; irq < CIRQ_HWIRQ0 + CIRQ_NUM; irq++) {
		u32 cfg = readl_relaxed(gicd + GIC_DIST_CONFIG + irq / 16 * 4);

		if (cfg & (2u << ((irq % 16) * 2)))
			cfg_edge[irq / 32] |= BIT(irq % 32);
	}
	/* sysirq: one bit per SPI, set = inverted (the line is active low) */
	for (i = 1; i < GIC_NR_WORDS; i++)
		pol[i] = readl_relaxed(sysirq + (i - 1) * 4);

	for (i = 0; i < CIRQ_WORDS; i++) {
		u32 valid = cirq_valid(i);
		u32 en = gic_bits(enabled, GIC_NR_WORDS, i * 32) & valid;
		u32 edge = gic_bits(cfg_edge, GIC_NR_WORDS, i * 32) & valid;
		u32 neg = gic_bits(pol, GIC_NR_WORDS, i * 32) & valid;

		writel_relaxed(neg, cirq + CIRQ_POL_CLR + i * 4);	/* POL_NEG */
		writel_relaxed(~neg & valid, cirq + CIRQ_POL_SET + i * 4);
		writel_relaxed(edge, cirq + CIRQ_SENS_CLR + i * 4);	/* edge */
		writel_relaxed(~edge & valid, cirq + CIRQ_SENS_SET + i * 4);
		writel_relaxed(en, cirq + CIRQ_MASK_CLR + i * 4);
		writel_relaxed(~en & valid, cirq + CIRQ_MASK_SET + i * 4);
	}
	mb();
}

static void cirq_enable(void)
{
	cirq_ack_all();
	writel((readl_relaxed(cirq + CIRQ_CON) | CIRQ_CON_EN | CIRQ_CON_EDGE_ONLY) & 0x7,
	       cirq + CIRQ_CON);
}

/* Replay what CIRQ latched into the GIC as pending, then mask, ack and disable it. */
static void cirq_flush_disable(void)
{
	unsigned int i;

	for (i = 0; i < CIRQ_WORDS; i++)
		writel_relaxed(~0u, cirq + CIRQ_MASK_CLR + i * 4);
	mb();
	for (i = 0; i < CIRQ_WORDS; i++) {
		u32 sta = readl_relaxed(cirq + CIRQ_STA + i * 4) & cirq_valid(i);

		while (sta) {
			unsigned int b = __ffs(sta), hw = CIRQ_HWIRQ0 + i * 32 + b;

			sta &= ~BIT(b);
			writel_relaxed(BIT(hw % 32), gicd + GIC_DIST_PENDING_SET + hw / 32 * 4);
		}
	}
	for (i = 0; i < CIRQ_WORDS; i++)
		writel_relaxed(~0u, cirq + CIRQ_MASK_SET + i * 4);
	mb();
	cirq_ack_all();
	writel(readl_relaxed(cirq + CIRQ_CON) & ~CIRQ_CON_EN & 0x7, cirq + CIRQ_CON);
}

/* --- the PCM, after mt_spm_internal.c */

static int pcm_reset_and_init(void)
{
	u32 con1;

	spm_wr(SPM_PCM_CON0, CFG_KEY | CON0_PCM_SW_RESET);
	spm_wr(SPM_PCM_CON0, CFG_KEY);
	if (spm_rd(SPM_PCM_FSM_STA) != PCM_FSM_STA_DEF)
		return -EIO;
	spm_wr(SPM_PCM_CON0, CFG_KEY | CON0_IM_SLEEP_DVS);
	con1 = spm_rd(SPM_PCM_CON1) & (CON1_PCM_WDT_WAKE_MODE | CON1_PCM_WDT_EN);
	spm_wr(SPM_PCM_CON1, con1 | CFG_KEY | CON1_EVENT_LOCK_EN | CON1_SPM_SRAM_ISO_B |
	       CON1_SPM_SRAM_SLP_B | CON1_IM_NONRP_EN | CON1_MIF_APBEN | CON1_MD32_APB_INTERNAL_EN);
	return 0;
}

static void pcm_kick_im(void)
{
	u32 len = ARRAY_SIZE(sodi_binary) - 1, con0;

	if (spm_rd(SPM_PCM_IM_PTR) != pcm_im_pa || spm_rd(SPM_PCM_IM_LEN) != len ||
	    SODI_SESS > 2) {
		spm_wr(SPM_PCM_IM_PTR, pcm_im_pa);
		spm_wr(SPM_PCM_IM_LEN, len);
	} else {
		spm_wr(SPM_PCM_CON1, spm_rd(SPM_PCM_CON1) | CFG_KEY | CON1_IM_SLAVE);
	}
	con0 = spm_rd(SPM_PCM_CON0) & ~(CON0_IM_KICK | CON0_PCM_KICK);
	spm_wr(SPM_PCM_CON0, con0 | CFG_KEY | CON0_IM_KICK);
	spm_wr(SPM_PCM_CON0, con0 | CFG_KEY);
}

static void pcm_init_regs_and_vectors(void)
{
	/* r0 and r7 start from the values the SPM drives now */
	spm_wr(SPM_PCM_REG_DATA_INI, spm_rd(SPM_POWER_ON_VAL0));
	spm_wr(SPM_PCM_PWR_IO_EN, PCM_RF_SYNC_R0);
	spm_wr(SPM_PCM_PWR_IO_EN, 0);
	spm_wr(SPM_PCM_REG_DATA_INI, spm_rd(SPM_POWER_ON_VAL1));
	spm_wr(SPM_PCM_PWR_IO_EN, PCM_RF_SYNC_R7);
	spm_wr(SPM_PCM_PWR_IO_EN, 0);

	/* Fire OS's vectors (the APSRC wake/sleep entries); the program enables them only with
	 * SPM_MEMPLL_RESET or SPM_SODI_DIS, which are left clear */
	spm_wr(SPM_PCM_EVENT_VECTOR0, EVENT_VEC(30, 1, 0, 144));
	spm_wr(SPM_PCM_EVENT_VECTOR1, EVENT_VEC(31, 1, 0, 172));
	spm_wr(SPM_PCM_EVENT_VECTOR2, EVENT_VEC(30, 1, 0, 0));
	spm_wr(SPM_PCM_EVENT_VECTOR3, EVENT_VEC(31, 1, 0, 58));
	spm_wr(SPM_PCM_EVENT_VECTOR4, 0);
	spm_wr(SPM_PCM_EVENT_VECTOR5, 0);
	spm_wr(SPM_PCM_EVENT_VECTOR6, 0);
	spm_wr(SPM_PCM_EVENT_VECTOR7, 0);
}

static struct {
	u32 ap_stanby_con, src_req, pasr_dpd_2;
} saved_ctl;

/* sodi_ctrl from mt_spm_sodi.c: wfi_op AND over every core's WFI, the display may ask for DRAM */
static void pcm_set_power_control(void)
{
	unsigned int i;

	saved_ctl.ap_stanby_con = spm_rd(SPM_AP_STANBY_CON);
	saved_ctl.src_req = spm_rd(SPM_PCM_SRC_REQ);
	saved_ctl.pasr_dpd_2 = spm_rd(SPM_PCM_PASR_DPD_2);

	spm_wr(SPM_AP_STANBY_CON, (saved_ctl.ap_stanby_con & ASC_SRCCLKENI_MASK) |
	       BIT(23) /* !conn_mask */ | BIT(21) /* !md32_req_mask */ |
	       BIT(16) /* !disp_req_mask */ | BIT(4) /* wfi_op AND */);
	/* our own APSRC request held: DRAM stays out of self-refresh */
	spm_wr(SPM_PCM_SRC_REQ, (saved_ctl.src_req & ~0x3) | BIT(0));
	spm_wr(SPM_PCM_PASR_DPD_2, 0x1f);
	for (i = 0; i < 4; i++) {
		spm_wr(SPM_SLEEP_CA15_WFI0_EN + 4 * i, 1);
		spm_wr(SPM_SLEEP_CA7_WFI0_EN + 4 * i, 1);
	}
}

static void pcm_set_wakeup_event(void)
{
	u64 lim = 0xffffffffu - 30 * 32768;	/* PCM_TIMER_MAX: below the PCM watchdog */
	u32 val = timer_ms ? min_t(u64, max_t(u64, 1, (u64)timer_ms * 32768 / 1000), lim) : lim;

	spm_wr(SPM_PCM_TIMER_VAL, val);
	spm_wr(SPM_PCM_CON1, spm_rd(SPM_PCM_CON1) | CFG_KEY | CON1_PCM_TIMER_EN);
	spm_wr(SPM_SLEEP_WAKEUP_EVENT_MASK, ~(u32)WAKE_SRC_SODI);
	spm_wr(SPM_SLEEP_ISR_MASK, (spm_rd(SPM_SLEEP_ISR_MASK) & ISRM_TWAM) | ISRM_RET_IRQ_1_7);
}

static void pcm_kick_run(u32 flags)
{
	u32 con0;

	spm_wr(SPM_PCM_MAS_PAUSE_MASK, 0xffffffff);
	spm_wr(SPM_PCM_REG_DATA_INI, 0);
	spm_wr(SPM_CLK_CON, spm_rd(SPM_CLK_CON) & ~(CC_DISABLE_DORM_PWR | CC_LOCK_INFRA_DCM));
	spm_wr(SPM_PCM_FLAGS, flags);
	spm_wr(SPM_PCM_RESERVE, 0);
	spm_wr(SPM_PCM_PWR_IO_EN, PCM_PWRIO_EN_R0 | PCM_PWRIO_EN_R7);
	con0 = spm_rd(SPM_PCM_CON0) & ~(CON0_IM_KICK | CON0_PCM_KICK);
	spm_wr(SPM_PCM_CON0, con0 | CFG_KEY | CON0_PCM_KICK);
	spm_wr(SPM_PCM_CON0, con0 | CFG_KEY);
}

static void pcm_clean_after_wakeup(void)
{
	/* the SPM drives POWER_ON_VAL0/1 again, not the PCM's r0/r7 */
	spm_wr(SPM_PCM_PWR_IO_EN, 0);
	spm_wr(SPM_SLEEP_CPU_WAKEUP_EVENT, 0);
	spm_wr(SPM_PCM_CON1, CFG_KEY | (spm_rd(SPM_PCM_CON1) & ~(u32)CON1_PCM_TIMER_EN & 0xffff));
	spm_wr(SPM_SLEEP_WAKEUP_EVENT_MASK, ~0u);
	spm_wr(SPM_SLEEP_ISR_MASK, spm_rd(SPM_SLEEP_ISR_MASK) | ISRM_ALL_EXC_TWAM);
	spm_wr(SPM_SLEEP_ISR_STATUS, ISRS_PCM_RETURN);
	spm_wr(SPM_PCM_SW_INT_CLEAR, PCM_SW_INT_ALL);
	spm_wr(SPM_AP_STANBY_CON, saved_ctl.ap_stanby_con);
	spm_wr(SPM_PCM_SRC_REQ, saved_ctl.src_req);
	spm_wr(SPM_PCM_PASR_DPD_2, saved_ctl.pasr_dpd_2);
}

static void sodi_state_disable(bool off);

static void sodi_fail(const char *why)
{
	failed = true;
	enable = false;
	sodi_state_disable(true);
	printk_deferred(KERN_ERR "mt8163-sodi: %s; off until reboot (pc %08x r12 %08x isr %08x raw %08x fsm %08x)\n",
			why, spm_rd(SPM_PCM_REG15_DATA), spm_rd(SPM_PCM_REG12_DATA),
			spm_rd(SPM_SLEEP_ISR_STATUS), spm_rd(SPM_SLEEP_ISR_RAW_STA),
			spm_rd(SPM_PCM_FSM_STA));
}

static u32 sodi_flags(void)
{
	u32 f = SPM_SCREEN_OFF | SPM_LOW_SPD_I2C | SPM_CPU_DVS_DIS | SPM_DDRPHY_PDN_DIS |
		SPM_VCORE_DVS_DIS;

	if (!cpu_pdn)
		f |= SPM_CPU_PDN_DIS;
	/* Fire OS: DDR_HIGH_SPEED unless GCE's clock runs (only read on the DRAM path) */
	if (readl_relaxed(infra + INFRA_PDN_STA0) & INFRA0_GCE)
		f |= SPM_DDR_HIGH_SPEED;
	return f;
}

/* One SODI. Returns the cpuidle index actually used (1, or 0 if it fell back to WFI). */
static int sodi_once(void)
{
	u32 en[GIC_NR_WORDS];
	bool pdn = cpu_pdn;
	unsigned int i;
	int ret = 0, err;
	u32 v;

	st.tries++;
	if (pdn) {
		if (cpu_pm_enter())
			goto wfi;
		if (cpu_cluster_pm_enter()) {
			cpu_pm_exit();
			goto wfi;
		}
	}

	/* mask everything at the GIC but the SPM's IRQ0; CIRQ watches what was enabled */
	for (i = 0; i < GIC_NR_WORDS; i++)
		en[i] = readl_relaxed(gicd + GIC_DIST_ENABLE_SET + i * 4);
	cirq_clone(en);
	for (i = 0; i < GIC_NR_WORDS; i++)
		writel_relaxed(~0u, gicd + GIC_DIST_ENABLE_CLEAR + i * 4);
	writel_relaxed(BIT(SPM_IRQ0_HWIRQ % 32), gicd + GIC_DIST_ENABLE_SET + SPM_IRQ0_HWIRQ / 32 * 4);
	mb();
	cirq_enable();

	err = pcm_reset_and_init();
	if (err) {
		sodi_fail("PCM did not reset");
		goto restore;
	}
	pcm_kick_im();
	pcm_init_regs_and_vectors();
	pcm_set_power_control();
	pcm_set_wakeup_event();
	pcm_kick_run(sodi_flags());
	st.entered++;

	if (pdn) {
		bool tr = trace > 0;
		u64 c0 = 0;
		u32 g0 = 0;

		if (tr) {
			c0 = read_sysreg(cntvct_el0);
			g0 = readl_relaxed(gpt + 0x28);
			pr_emerg("mt8163-sodi: trace: CPU_SUSPEND cntvct %llx gpt2 %08x flags %08x\n",
				 c0, g0, spm_rd(SPM_PCM_FLAGS));
		}
		err = psci_cpu_suspend_enter(PSCI_CLUSTER_PDN);
		if (tr) {
			trace--;
			pr_emerg("mt8163-sodi: trace: back %d cntvct %llx (+%llu) gpt2 %08x (+%u) pc %08x r12 %08x isr %08x timer_out %u\n",
				 err, read_sysreg(cntvct_el0), read_sysreg(cntvct_el0) - c0,
				 readl_relaxed(gpt + 0x28), readl_relaxed(gpt + 0x28) - g0,
				 spm_rd(SPM_PCM_REG15_DATA), spm_rd(SPM_PCM_REG12_DATA),
				 spm_rd(SPM_SLEEP_ISR_STATUS), spm_rd(SPM_PCM_TIMER_OUT));
		}
	} else {
		/* the PCM keeps CPU0 powered; it wakes us with IRQ0 when a wake event comes */
		ct_cpuidle_enter();
		dsb(sy);
		isb();
		wfi();
		ct_cpuidle_exit();
		err = -EOPNOTSUPP;
	}
	st.last_ret = err;

	/* The PCM ends every path by raising SW_INT0; wait for it before touching its outputs. */
	if (readl_relaxed_poll_timeout_atomic(spm + SPM_SLEEP_ISR_RAW_STA, v,
					      v & ISRS_SW_INT0, 5, 2000) &&
	    !(spm_rd(SPM_SLEEP_ISR_STATUS) & (ISRS_SW_INT0 | ISRS_PCM_RETURN))) {
		st.pcm_late++;
		spm_wr(SPM_PCM_PWR_IO_EN, 0);
		sodi_fail("PCM did not finish");
		pcm_reset_and_init();
	}

	st.last_pc = spm_rd(SPM_PCM_REG_DATA_INI);
	st.last_r12 = spm_rd(SPM_PCM_REG12_DATA);
	st.last_raw = spm_rd(SPM_SLEEP_ISR_RAW_STA);
	st.last_misc = spm_rd(SPM_SLEEP_WAKEUP_MISC);
	st.last_timer_out = spm_rd(SPM_PCM_TIMER_OUT);
	st.last_debug = spm_rd(SPM_PCM_PASR_DPD_3);
	st.last_event = spm_rd(SPM_PCM_EVENT_REG_STA);
	st.last_isr = spm_rd(SPM_SLEEP_ISR_STATUS);
	pcm_clean_after_wakeup();

	for (i = 0; i < 32; i++)
		if (st.last_r12 & BIT(i))
			st.wake_bits[i]++;
	if (st.last_pc)
		sodi_fail("PCM assert");
	if (!err) {
		st.powered++;
	} else if (err == -EOPNOTSUPP) {
		st.aborted++;
	} else {
		st.psci_err++;
		if (st.psci_err >= 3)
			sodi_fail("PSCI CPU_SUSPEND errors");
	}
	ret = 1;

restore:
	/* IRQ0's source is cleared (pcm_clean_after_wakeup); CIRQ's latches go into the GIC */
	writel_relaxed(BIT(SPM_IRQ0_HWIRQ % 32), gicd + GIC_DIST_PENDING_CLEAR + SPM_IRQ0_HWIRQ / 32 * 4);
	cirq_flush_disable();
	for (i = 0; i < GIC_NR_WORDS; i++)
		writel_relaxed(en[i], gicd + GIC_DIST_ENABLE_SET + i * 4);
	mb();
	if (pdn) {
		cpu_cluster_pm_exit();
		cpu_pm_exit();
	}
	if (ret)
		return 1;
wfi:
	st.fallback++;
	ct_cpuidle_enter();
	cpu_do_idle();
	ct_cpuidle_exit();
	return 0;
}

/*
 * The arch timer's counter lives in MCUSYS: after a SODI it restarts from a value the secure
 * monitor saved once, so it runs backwards (measured: research/mt8163-cpuidle.md, "Stage B
 * results"). Timekeeping must not count on it: SODI waits until the clocksource is the GPT
 * ("timer"); sched_clock is the GPT already (same 13 MHz, registered last).
 */
static bool sodi_clock_ok(void)
{
	struct system_time_snapshot snap;

	ktime_get_snapshot_id(CLOCK_MONOTONIC, &snap);
	return snap.cs_id != CSID_ARM_ARCH_COUNTER;
}

static __cpuidle int sodi_enter(struct cpuidle_device *dev, struct cpuidle_driver *drv, int idx)
{
	u32 sta1;

	if (!enable || failed || !sodi_cores_alone()) {
		st.blocked_cpu++;
		goto wfi;
	}
	if (cpu_pdn && !sodi_clock_ok()) {
		st.blocked_clock++;
		goto wfi;
	}
	/* margin: no SODI while the eMMC host's clock runs (Fire OS's MSDC0 rule) */
	sta1 = readl_relaxed(infra + INFRA_PDN_STA1);
	if (!(sta1 & INFRA1_MSDC0)) {
		st.blocked_emmc++;
		goto wfi;
	}
	if ((~readl_relaxed(infra + INFRA_PDN_STA0) & FIREOS_INFRA0_MASK) ||
	    (~sta1 & FIREOS_INFRA1_MASK))
		st.fireos_clk_block++;
	return sodi_once();
wfi:
	ct_cpuidle_enter();
	cpu_do_idle();
	ct_cpuidle_exit();
	return 0;
}

static __cpuidle int wfi_enter(struct cpuidle_device *dev, struct cpuidle_driver *drv, int idx)
{
	cpu_do_idle();
	return idx;
}

static struct cpuidle_driver sodi_driver = {
	.name = "mt8163_sodi",
	.owner = THIS_MODULE,
	.states = {
		{
			.enter = wfi_enter,
			.exit_latency = 1,
			.target_residency = 1,
			.name = "WFI",
			.desc = "ARM WFI",
		},
		{
			.enter = sodi_enter,
			.exit_latency = 1500,
			.target_residency = 5000,
			.flags = CPUIDLE_FLAG_TIMER_STOP | CPUIDLE_FLAG_RCU_IDLE,
			.name = "SODI",
			.desc = "SPM: CPU0+cluster+MCUSYS off",
		},
	},
	.state_count = 2,
};

/* Fire OS's spm_irq0_handler: IRQ0 outside SODI means a stray PCM interrupt; clear it. */
static irqreturn_t spm_irq0(int irq, void *data)
{
	u32 isr = spm_rd(SPM_SLEEP_ISR_STATUS);

	spm_wr(SPM_SLEEP_ISR_MASK, spm_rd(SPM_SLEEP_ISR_MASK) | ISRM_ALL_EXC_TWAM);
	spm_wr(SPM_SLEEP_ISR_STATUS, isr);
	spm_wr(SPM_PCM_SW_INT_CLEAR, PCM_SW_INT_ALL);
	pr_warn_ratelimited("mt8163-sodi: SPM IRQ0 outside SODI (isr %08x)\n", isr);
	return IRQ_HANDLED;
}

/*
 * The SPM's IRQ0 (SPI 163) is active low, like the rest of the SPM's lines in Fire OS's DT; the
 * sysirq inverts it once it is requested with that type. Unrequested, the GIC sees it pending.
 */
static int spm_irq0_request(void)
{
	struct of_phandle_args args = {
		.np = of_find_compatible_node(NULL, NULL, "mediatek,mt6577-sysirq"),
		.args_count = 3,
		.args = { 0 /* GIC_SPI */, SPM_IRQ0_HWIRQ - 32, IRQ_TYPE_LEVEL_LOW },
	};
	unsigned int virq;
	int err;

	if (!args.np)
		return -ENODEV;
	virq = irq_create_of_mapping(&args);
	of_node_put(args.np);
	if (!virq)
		return -ENXIO;
	err = request_irq(virq, spm_irq0, IRQF_NO_SUSPEND, "mt8163-sodi", NULL);
	if (err)
		irq_dispose_mapping(virq);
	return err;
}

static void sodi_state_disable(bool off)
{
	struct cpuidle_device *dev = per_cpu(cpuidle_devices, 0);

	if (!dev)
		return;
	if (off)
		dev->states_usage[1].disable |= CPUIDLE_STATE_DISABLED_BY_DRIVER;
	else
		dev->states_usage[1].disable &= ~CPUIDLE_STATE_DISABLED_BY_DRIVER;
}

static void __iomem *map(const char *compat, int idx, phys_addr_t fallback, size_t len)
{
	struct device_node *np = compat ? of_find_compatible_node(NULL, NULL, compat) : NULL;
	void __iomem *p = np ? of_iomap(np, idx) : NULL;

	of_node_put(np);
	return p ? p : ioremap(fallback, len);
}

static int sodi_setup(void)
{
	unsigned int i;
	int err;

	if (!psci_ops.cpu_suspend) {
		pr_err("mt8163-sodi: no PSCI CPU_SUSPEND\n");
		return -ENODEV;
	}
	spm = map("mediatek,mt8163-scpsys", 0, 0x10006000, SZ_4K);
	cirq = ioremap(0x10204000, SZ_4K);
	gicd = map("arm,gic-400", 0, 0x10221000, SZ_4K);
	sysirq = map("mediatek,mt6577-sysirq", 0, 0x10200620, 0x20);
	pwrap = ioremap(0x1000d000, SZ_4K);
	infra = ioremap(0x10001000, SZ_4K);
	gpt = ioremap(0x10008000, SZ_4K);
	if (!spm || !cirq || !gicd || !sysirq || !pwrap || !infra || !gpt) {
		err = -ENOMEM;
		goto unmap;
	}
	/* register control on, as mt-boot and Fire OS's spm_register_init() set it */
	writel((SPM_PROJECT_CODE << 16) | BIT(0), spm + SPM_POWERON_CONFIG_SET);
	if ((readl_relaxed(gicd + 4) & 0x1f) + 1 != GIC_NR_WORDS) {
		pr_err("mt8163-sodi: unexpected GIC (typer %08x)\n", readl_relaxed(gicd + 4));
		err = -ENODEV;
		goto unmap;
	}

	pcm_im = kmemdup(sodi_binary, sizeof(sodi_binary), GFP_KERNEL);
	if (!pcm_im) {
		err = -ENOMEM;
		goto unmap;
	}
	dcache_clean_poc((unsigned long)pcm_im, (unsigned long)pcm_im + sizeof(sodi_binary));
	pcm_im_pa = (u32)virt_to_phys(pcm_im);

	/* every PMIC wrapper DVFS slot: a write to the MT6323's read-only chip ID */
	for (i = 0; i < 8; i++) {
		writel(MT6323_CID, pwrap + PWRAP_DVFS_ADR(i));
		writel(0, pwrap + PWRAP_DVFS_WDATA(i));
	}

	/* the PCM's own interrupts off and clear before IRQ0 is unmasked */
	spm_wr(SPM_SLEEP_ISR_MASK, spm_rd(SPM_SLEEP_ISR_MASK) | ISRM_ALL_EXC_TWAM);
	spm_wr(SPM_SLEEP_ISR_STATUS, ISRS_PCM_RETURN);
	spm_wr(SPM_PCM_SW_INT_CLEAR, PCM_SW_INT_ALL);
	err = spm_irq0_request();
	if (err) {
		pr_err("mt8163-sodi: SPM IRQ0: %d\n", err);
		kfree(pcm_im);
		goto unmap;
	}

	sodi_driver.cpumask = (struct cpumask *)cpumask_of(0);
	err = cpuidle_register(&sodi_driver, NULL);
	if (err) {
		/* IRQ0 stays requested: harmless, and the next enable does not retry */
		pr_err("mt8163-sodi: cpuidle_register: %d\n", err);
		failed = true;
		return err;
	}
	pr_info("mt8163-sodi: %s at %08x, %zu words\n", SODI_VERSION, pcm_im_pa,
		ARRAY_SIZE(sodi_binary));
	ready = true;
	return 0;
unmap:
	for (i = 0; i < 7; i++) {
		void __iomem *p = (void __iomem *[]){ spm, cirq, gicd, sysirq, pwrap, infra, gpt }[i];

		if (p)
			iounmap(p);
	}
	spm = cirq = gicd = sysirq = pwrap = infra = gpt = NULL;
	return err;
}

static void deadman_fn(struct work_struct *w);
static DECLARE_DELAYED_WORK(deadman_work, deadman_fn);

static void deadman_fn(struct work_struct *w)
{
	if (!enable || !deadman)
		return;
	if (time_after(jiffies, keepalive_at + deadman * HZ)) {
		enable = false;
		sodi_state_disable(true);
		pr_emerg("mt8163-sodi: no keepalive for %u s; SODI off, restarting\n", deadman);
		orderly_reboot();
		return;
	}
	schedule_delayed_work(&deadman_work, 5 * HZ);
}

static int enable_set(const char *val, const struct kernel_param *kp)
{
	bool on;
	int err = kstrtobool(val, &on);

	if (err)
		return err;
	mutex_lock(&sodi_mutex);
	if (on && failed) {
		err = -EIO;
	} else if (on && !ready && !of_machine_is_compatible("mediatek,mt8163")) {
		err = -ENODEV;
	} else if (on && !ready) {
		err = sodi_setup();
	}
	if (!err && on && !sodi_clock_ok())
		pr_warn("mt8163-sodi: waiting for a clocksource other than the arch counter (echo timer > /sys/devices/system/clocksource/clocksource0/current_clocksource)\n");
	if (!err) {
		keepalive_at = jiffies;
		WRITE_ONCE(enable, on);
		sodi_state_disable(!on);
		if (on && deadman)
			schedule_delayed_work(&deadman_work, 5 * HZ);
	}
	mutex_unlock(&sodi_mutex);
	return err;
}

static const struct kernel_param_ops enable_ops = {
	.set = enable_set,
	.get = param_get_bool,
};
module_param_cb(enable, &enable_ops, &enable, 0644);
MODULE_PARM_DESC(enable, "enter SODI when CPU0 is the only online core and idle (default off)");
module_param(cpu_pdn, bool, 0644);
MODULE_PARM_DESC(cpu_pdn, "power CPU0/cluster/MCUSYS down (0: the PCM keeps them on, for tests)");
module_param(timer_ms, uint, 0644);
MODULE_PARM_DESC(timer_ms, "PCM timer backstop: wake after at most this long (0: none)");
module_param(trace, uint, 0644);
MODULE_PARM_DESC(trace, "tests: log the next this many CPU_SUSPENDs to the console");
module_param(deadman, uint, 0644);
MODULE_PARM_DESC(deadman, "tests: restart if keepalive is not written for this many seconds (0: off)");

static int keepalive_set(const char *val, const struct kernel_param *kp)
{
	keepalive_at = jiffies;
	return 0;
}

static const struct kernel_param_ops keepalive_ops = { .set = keepalive_set };
module_param_cb(keepalive, &keepalive_ops, NULL, 0200);

static int status_get(char *buf, const struct kernel_param *kp)
{
	int n, i;

	n = sysfs_emit(buf, "ready %d failed %d enable %d cpu_pdn %d\n"
		       "tries %lu entered %lu powered %lu aborted %lu psci_err %lu fallback %lu pcm_late %lu\n"
		       "blocked_cpu %lu blocked_emmc %lu blocked_clock %lu fireos_clk_block %lu\n"
		       "last ret %d r12 %08x raw %08x misc %08x timer_out %u debug %08x event %08x isr %08x assert_pc %u\n"
		       "wake",
		       ready, failed, enable, cpu_pdn, st.tries, st.entered, st.powered, st.aborted,
		       st.psci_err, st.fallback, st.pcm_late, st.blocked_cpu, st.blocked_emmc,
		       st.blocked_clock, st.fireos_clk_block, st.last_ret, st.last_r12, st.last_raw, st.last_misc,
		       st.last_timer_out, st.last_debug, st.last_event, st.last_isr, st.last_pc);
	for (i = 0; i < 32; i++)
		if (st.wake_bits[i])
			n += sysfs_emit_at(buf, n, " %d:%lu", i, st.wake_bits[i]);
	n += sysfs_emit_at(buf, n, "\n");
	return n;
}

static const struct kernel_param_ops status_ops = { .get = status_get };
module_param_cb(status, &status_ops, NULL, 0444);

MODULE_DESCRIPTION("MT8163 screen-on deep idle (SODI) through the SPM's PCM");
MODULE_LICENSE("GPL");
