// SPDX-License-Identifier: GPL-2.0
/*
 * athena-mcdi.c: the AP's side of the MT8183's MCDI (CPU idle) on the 2nd gen Echo Show 8 (athena),
 * so that mainline's PSCI idle states can be used (custom-kernel/athena/research/mcdi-cpuidle.md).
 *
 * Amazon's TF-A answers a CPU_SUSPEND by parking the core's GIC interface and leaving the power-down,
 * and the wake-up, to the SSPM's MCDI firmware. That firmware only manages the CPUs the AP lists as
 * available, in a mailbox the Fire OS kernel writes (mtk_mcdi.c) and mainline never did: a core then
 * went to sleep and never woke. This lists them, as Fire OS does:
 *
 *  - at boot, a pause/resume handshake shows that the SSPM's MCDI task answers; if it does not,
 *    cpuidle is turned off and the cores idle in WFI;
 *  - no rail may be switched off (BUCK_POWER_OFF_MASK = 0) and no CPU is isolated (ISOLATION = 0);
 *  - AVAIL_CPU_MASK follows the CPUs that are up: a CPU's bit is set before it starts and cleared
 *    once it is down.
 *
 * The cores outside MCDI_IDLE_CPUS (mcdi.py sets it for a build that tries some first) are held in
 * WFI by a resume latency of 0 (/sys/devices/system/cpu/cpuN/power/pm_qos_resume_latency_us, which
 * lifts it with "n/a"): the PSCI cpuidle driver needs the idle state on every core or on none.
 *
 * Mainline's cluster-level states (PSCI 0x01010001) work with this alone: Fire OS writes
 * CLUSTER_n_CAN_POWER_OFF before each such call, but the SSPM powers a cluster down without it (the
 * cluster's PWR_STATUS bit clears and its counter in the mailbox advances; research/mcdi-cpuidle.md).
 *
 * The mailbox (SSPM mailbox 3, 32-bit words, polled by both sides, no doorbell) at 0x10480000.
 */

#include <linux/bitops.h>
#include <linux/cpu.h>
#include <linux/cpuhotplug.h>
#include <linux/cpuidle.h>
#include <linux/delay.h>
#include <linux/init.h>
#include <linux/io.h>
#include <linux/iopoll.h>
#include <linux/of.h>
#include <linux/pm_qos.h>
#include <linux/spinlock.h>

#define MCDI_MBOX		0x10480000
#define MCDI_BUCK_OFF_MASK	(4 * 2)
#define MCDI_PAUSE		(4 * 6)		/* 1 pause, 0 run (Amazon's values, not TF-A's BIT(1)) */
#define MCDI_AVAIL_CPU_MASK	(4 * 7)
#define MCDI_ISOLATION_MASK	(4 * 12)
#define MCDI_PAUSE_ACK		(4 * 13)

#ifndef MCDI_IDLE_CPUS
#define MCDI_IDLE_CPUS		0xff
#endif

static void __iomem *mcdi;
static DEFINE_SPINLOCK(mcdi_lock);
static u32 mcdi_avail;

static int mcdi_pause(u32 pause)
{
	u32 v;

	writel(pause, mcdi + MCDI_PAUSE);
	return readl_poll_timeout_atomic(mcdi + MCDI_PAUSE_ACK, v, v == pause, 10, 5000);
}

/* A CPU's bit, with the task paused around the change as Fire OS does */
static void mcdi_set_avail(unsigned int cpu, bool on)
{
	unsigned long flags;

	spin_lock_irqsave(&mcdi_lock, flags);
	if (on)
		mcdi_avail |= BIT(cpu);
	else
		mcdi_avail &= ~BIT(cpu);
	if (mcdi_pause(1))
		pr_warn_ratelimited("athena-mcdi: the SSPM did not pause\n");
	writel(mcdi_avail, mcdi + MCDI_AVAIL_CPU_MASK);
	mcdi_pause(0);
	spin_unlock_irqrestore(&mcdi_lock, flags);
}

static int mcdi_cpu_prepare(unsigned int cpu)
{
	mcdi_set_avail(cpu, true);
	return 0;
}

static int mcdi_cpu_dead(unsigned int cpu)
{
	mcdi_set_avail(cpu, false);
	return 0;
}

/*
 * Early, before the secondary CPUs start and long before the PSCI cpuidle driver registers: the
 * secondaries' bits go in from the prepare step of their bring-up, on the CPU starting them.
 */
static int __init athena_mcdi_init(void)
{
	int ret;

	if (!of_machine_is_compatible("amzn,athena"))
		return 0;
	mcdi = ioremap(MCDI_MBOX, 0x100);
	if (!mcdi)
		goto off;
	if (mcdi_pause(1) || mcdi_pause(0)) {
		pr_err("athena-mcdi: the SSPM's MCDI task does not answer: no deep idle\n");
		goto off;
	}
	writel(0, mcdi + MCDI_BUCK_OFF_MASK);
	writel(0, mcdi + MCDI_ISOLATION_MASK);
	ret = cpuhp_setup_state(CPUHP_BP_PREPARE_DYN, "soc/athena-mcdi:prepare", mcdi_cpu_prepare,
				mcdi_cpu_dead);
	if (ret < 0) {
		pr_err("athena-mcdi: no hotplug state (%d): no deep idle\n", ret);
		goto off;
	}
	pr_info("athena-mcdi: the SSPM manages CPUs %#x\n", readl(mcdi + MCDI_AVAIL_CPU_MASK));
	return 0;
off:
	disable_cpuidle();
	return 0;
}
early_initcall(athena_mcdi_init);

/* Once the CPUs' devices exist, and before the PSCI cpuidle driver registers */
static int __init athena_mcdi_hold(void)
{
	static struct dev_pm_qos_request wfi[NR_CPUS];
	unsigned int cpu;

	if (!mcdi || MCDI_IDLE_CPUS == 0xff)
		return 0;
	for_each_possible_cpu(cpu) {
		struct device *dev = get_cpu_device(cpu);

		if (!(MCDI_IDLE_CPUS & BIT(cpu)) && dev)
			dev_pm_qos_add_request(dev, &wfi[cpu], DEV_PM_QOS_RESUME_LATENCY, 0);
	}
	pr_info("athena-mcdi: deep idle on CPUs %#x only\n", MCDI_IDLE_CPUS);
	return 0;
}
subsys_initcall_sync(athena_mcdi_hold);
