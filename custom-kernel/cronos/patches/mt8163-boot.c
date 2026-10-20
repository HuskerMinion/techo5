// SPDX-License-Identifier: GPL-2.0-only
/*
 * "mt-boot": how the MT8163's secondary cores start under Amazon's LK bootloader, after
 * drivers/misc/mediatek/base/power/mt8163/{mt_psci.c,mtcmos.c} in Amazon's 4.9 tree.
 *
 * The secure monitor implements PSCI 0.1 CPU_ON over SMC, but that call only records the entry
 * point: the kernel has to power the core's MTCMOS domain itself, through the SPM. Mainline's
 * "psci" enable method does the first half only, so the cores never left reset ("failed in
 * unknown state : 0x0"), and "spin-table" has nothing to release.
 *
 * Taking a core offline is the same in reverse (mt_psci.c's cpu_kill): the core calls PSCI CPU_OFF
 * and stops in WFI; the kernel then waits for the SPM to see it there and powers its domain down,
 * so an offline core draws nothing, where an idle one sits powered in WFI. CPU0 never goes.
 */
#include <linux/delay.h>
#include <linux/io.h>
#include <linux/iopoll.h>
#include <linux/of.h>
#include <linux/of_address.h>
#include <linux/spinlock.h>
#include <asm/cpu_ops.h>
#include <asm/smp_plat.h>

extern const struct cpu_operations cpu_psci_ops;

#define SPM_POWERON_CONFIG_SET	0x000
#define SPM_PWR_STATUS		0x60c
#define SPM_PWR_STATUS_2ND	0x610
#define SPM_SLEEP_TIMER_STA	0x720
#define CPU_STANDBYWFI(core)	BIT(16 + (core))
#define SPM_PROJECT_CODE	0xb16

#define SRAM_ISOINT_B		BIT(6)
#define SRAM_CKISO		BIT(5)
#define PWR_CLK_DIS		BIT(4)
#define PWR_ON_2ND		BIT(3)
#define PWR_ON			BIT(2)
#define PWR_ISO			BIT(1)
#define PWR_RST_B		BIT(0)
#define L1_PDN_ACK		BIT(8)
#define L1_PDN			BIT(0)

static const struct {
	u16 pwr_con;
	u16 l1_pdn;
	u32 sta;
} mt8163_core[] = {
	{ 0x200, 0x25c, BIT(9) },
	{ 0x218, 0x264, BIT(10) },
	{ 0x21c, 0x26c, BIT(11) },
	{ 0x220, 0x274, BIT(12) },
};

static void __iomem *spm;
static DEFINE_SPINLOCK(spm_lock);

static void spm_set(u32 off, u32 bits)
{
	writel_relaxed(readl_relaxed(spm + off) | bits, spm + off);
}

static void spm_clr(u32 off, u32 bits)
{
	writel_relaxed(readl_relaxed(spm + off) & ~bits, spm + off);
}

/* The 4.9 sequence, with its unbounded waits given a limit. */
static int mt8163_core_power_on(unsigned int core)
{
	unsigned long flags;
	u32 v;
	int err;

	if (core >= ARRAY_SIZE(mt8163_core))
		return -EINVAL;

	spin_lock_irqsave(&spm_lock, flags);
	writel_relaxed((SPM_PROJECT_CODE << 16) | BIT(0), spm + SPM_POWERON_CONFIG_SET);

	spm_set(mt8163_core[core].pwr_con, PWR_ON);
	udelay(1);
	spm_set(mt8163_core[core].pwr_con, PWR_ON_2ND);
	err = readl_relaxed_poll_timeout_atomic(spm + SPM_PWR_STATUS, v,
						v & mt8163_core[core].sta, 1, 10000);
	if (!err)
		err = readl_relaxed_poll_timeout_atomic(spm + SPM_PWR_STATUS_2ND, v,
							v & mt8163_core[core].sta, 1, 10000);
	if (err)
		goto out;

	spm_clr(mt8163_core[core].pwr_con, PWR_ISO);
	spm_clr(mt8163_core[core].l1_pdn, L1_PDN);
	err = readl_relaxed_poll_timeout_atomic(spm + mt8163_core[core].l1_pdn, v,
						!(v & L1_PDN_ACK), 1, 10000);
	if (err)
		goto out;

	udelay(1);
	spm_set(mt8163_core[core].pwr_con, SRAM_ISOINT_B);
	spm_clr(mt8163_core[core].pwr_con, SRAM_CKISO);
	spm_clr(mt8163_core[core].pwr_con, PWR_CLK_DIS);
	spm_set(mt8163_core[core].pwr_con, PWR_RST_B);
out:
	spin_unlock_irqrestore(&spm_lock, flags);
	if (err)
		pr_err("mt-boot: core %u did not power up (pwr_con %08x status %08x/%08x)\n", core,
		       readl_relaxed(spm + mt8163_core[core].pwr_con),
		       readl_relaxed(spm + SPM_PWR_STATUS), readl_relaxed(spm + SPM_PWR_STATUS_2ND));
	return err;
}

/* mtcmos.c's STA_POWER_DOWN, after the core is in WFI, each wait bounded. */
static int mt8163_core_power_off(unsigned int core)
{
	unsigned long flags;
	u32 v;
	int err;

	if (core == 0 || core >= ARRAY_SIZE(mt8163_core))
		return -EINVAL;

	/* the core's last act was PSCI CPU_OFF: it is in WFI, or nearly */
	err = readl_relaxed_poll_timeout(spm + SPM_SLEEP_TIMER_STA, v, v & CPU_STANDBYWFI(core),
					 10, 100000);
	if (err) {
		pr_err("mt-boot: core %u never reached WFI (sleep timer %08x); left powered\n", core, v);
		return err;
	}

	spin_lock_irqsave(&spm_lock, flags);
	writel_relaxed((SPM_PROJECT_CODE << 16) | BIT(0), spm + SPM_POWERON_CONFIG_SET);
	spm_set(mt8163_core[core].pwr_con, PWR_ISO);
	spm_set(mt8163_core[core].pwr_con, SRAM_CKISO);
	spm_clr(mt8163_core[core].pwr_con, SRAM_ISOINT_B);
	spm_set(mt8163_core[core].l1_pdn, L1_PDN);
	err = readl_relaxed_poll_timeout_atomic(spm + mt8163_core[core].l1_pdn, v,
						v & L1_PDN_ACK, 1, 10000);
	if (err)
		goto out;

	spm_clr(mt8163_core[core].pwr_con, PWR_RST_B);
	spm_set(mt8163_core[core].pwr_con, PWR_CLK_DIS);
	spm_clr(mt8163_core[core].pwr_con, PWR_ON);
	spm_clr(mt8163_core[core].pwr_con, PWR_ON_2ND);
	err = readl_relaxed_poll_timeout_atomic(spm + SPM_PWR_STATUS, v,
						!(v & mt8163_core[core].sta), 1, 10000);
	if (!err)
		err = readl_relaxed_poll_timeout_atomic(spm + SPM_PWR_STATUS_2ND, v,
							!(v & mt8163_core[core].sta), 1, 10000);
out:
	spin_unlock_irqrestore(&spm_lock, flags);
	if (err)
		pr_err("mt-boot: core %u did not power down (pwr_con %08x status %08x/%08x l1 %08x)\n",
		       core, readl_relaxed(spm + mt8163_core[core].pwr_con),
		       readl_relaxed(spm + SPM_PWR_STATUS), readl_relaxed(spm + SPM_PWR_STATUS_2ND),
		       readl_relaxed(spm + mt8163_core[core].l1_pdn));
	return err;
}

static int __init mt8163_boot_cpu_init(unsigned int cpu)
{
	return 0;
}

static int __init mt8163_boot_cpu_prepare(unsigned int cpu)
{
	if (!spm) {
		struct device_node *np = of_find_compatible_node(NULL, NULL, "mediatek,mt8163-scpsys");

		spm = np ? of_iomap(np, 0) : NULL;
		of_node_put(np);
		if (!spm)
			spm = ioremap(0x10006000, SZ_4K);
		if (!spm) {
			pr_err("mt-boot: cannot map the SPM\n");
			return -ENODEV;
		}
	}
	return cpu_psci_ops.cpu_prepare(cpu);
}

static int mt8163_boot_cpu_boot(unsigned int cpu)
{
	unsigned int core = MPIDR_AFFINITY_LEVEL(cpu_logical_map(cpu), 0);
	int err;

	/* A core left powered by a failed power-down would not start from the new entry point. */
	if (core < ARRAY_SIZE(mt8163_core) && spm &&
	    (readl_relaxed(spm + SPM_PWR_STATUS) & mt8163_core[core].sta)) {
		pr_err("mt-boot: core %u is still powered; not starting it\n", core);
		return -EBUSY;
	}
	err = cpu_psci_ops.cpu_boot(cpu);
	if (err)
		return err;
	return mt8163_core_power_on(core);
}

#ifdef CONFIG_HOTPLUG_CPU
static bool mt8163_boot_cpu_can_disable(unsigned int cpu)
{
	return cpu != 0 && cpu_psci_ops.cpu_disable && cpu_psci_ops.cpu_die;
}

static int mt8163_boot_cpu_disable(unsigned int cpu)
{
	return cpu_psci_ops.cpu_disable(cpu);
}

static void mt8163_boot_cpu_die(unsigned int cpu)
{
	cpu_psci_ops.cpu_die(cpu);
}

static int mt8163_boot_cpu_kill(unsigned int cpu)
{
	return mt8163_core_power_off(MPIDR_AFFINITY_LEVEL(cpu_logical_map(cpu), 0));
}
#endif

const struct cpu_operations mt8163_boot_ops = {
	.name		= "mt-boot",
	.cpu_init	= mt8163_boot_cpu_init,
	.cpu_prepare	= mt8163_boot_cpu_prepare,
	.cpu_boot	= mt8163_boot_cpu_boot,
#ifdef CONFIG_HOTPLUG_CPU
	.cpu_can_disable = mt8163_boot_cpu_can_disable,
	.cpu_disable	= mt8163_boot_cpu_disable,
	.cpu_die	= mt8163_boot_cpu_die,
	.cpu_kill	= mt8163_boot_cpu_kill,
#endif
};
