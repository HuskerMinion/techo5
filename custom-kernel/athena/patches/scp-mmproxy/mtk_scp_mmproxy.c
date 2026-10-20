// SPDX-License-Identifier: GPL-2.0
/*
 * mtk_scp_mmproxy.c: the SCP's register accesses to the media blocks, done by the AP (MT8183, athena).
 *
 * On the Chromebooks the SCP (the MT8183's Cortex-M4) programs the media blocks itself. On athena,
 * Fire OS's secure firmware leaves the SCP's access domain forbidden on every multimedia slave
 * (DEVAPC; custom-kernel/athena/research/tfa-devapc-psci.md), so its accesses are dropped, and there is
 * no call to change that. The AP may reach them. So the SCP's firmware is patched
 * (patches/scp-vdec-proxy/proxy.c) to trap every load or store to the multimedia window as it sees it
 * (0xa4000000-0xa7ffffff = AP 0x14000000-0x17ffffff) and post it to a mailbox in its DRAM, and this
 * thread does it on the real register and answers.
 *
 * Only the windows below are served, each while the device that owns it is powered (a powered-down
 * block's registers stall the bus): the H.264 decoder (VDEC_SYS's first page, whose clock bits and
 * 0x18 its firmware sets at each frame, and its 64 KiB), the H.264 encoder (which its firmware
 * programs and starts itself), the face detector (FDVT) and the ISP's pass 2 (DIP), whose firmware
 * sets some of its registers directly. Anything else
 * the firmware touches is refused as DEVAPC refused it, reads as 0, and is logged once.
 *
 * The firmware only touches a block while it handles a message, or just after (a task it handed the
 * message to): the thread spins while any message is out to the SCP (scp_ipi_send, mtk_scp_ipi.c) and
 * for 20 ms after the last access, looks once a millisecond for a second after the last message or
 * access, and otherwise sleeps until the next message is sent. So an idle unit has no thread waking
 * a thousand times a second, which kept a core out of its deep idle state; and its sleeps are
 * TASK_IDLE, which the load average does not count. The firmware gives up on an access nobody
 * answers after a second, so nothing here can leave the SCP stuck.
 *
 * Mailbox (32-bit words) at AP 0x504ff000, the last page of the firmware's DRAM: [0] request sequence
 * (SCP writes last), [1] op (bit 0 write, bits 4-7 size), [2] AP address, [3] value to write,
 * [4] answered sequence (AP writes last), [5] value read, [8] [9] an instruction the firmware could not
 * decode (it then stops).
 */

#include <linux/delay.h>
#include <linux/io.h>
#include <linux/kthread.h>
#include <linux/module.h>
#include <linux/of.h>
#include <linux/of_platform.h>
#include <linux/platform_device.h>
#include <linux/pm_runtime.h>
#include <linux/remoteproc/mtk_scp.h>

#define MMPROXY_MBOX		0x504ff000
/*
 * The last 255 accesses in a no-map page DRAM keeps through a watchdog reset (mode 3): word 0 magic,
 * 1 count, then {count, op (bit 31: not done), addr, val} each; the next boot's first message out
 * prints the last 32.
 */
#define MMPROXY_TRAIL		0x52901000
#define MMPROXY_TRAIL_MAGIC	0x7a11da7a

/*
 * 0: off (nothing is answered; the firmware gives up on each access after a second), 1: log the
 * accesses only and answer 0, 2: do them, 3: do them, log each one and keep the trail
 */
static int mmproxy_mode = 2;
module_param(mmproxy_mode, int, 0644);

static struct mmproxy_window {
	phys_addr_t base;
	u32 size;
	const char *owner;	/* the compatible of the device whose power the window needs */
	struct device *dev;
	void __iomem *regs;
} windows[] = {
	{ 0x16000000, 0x1000, "mediatek,mt8183-vcodec-dec" },	/* VDEC_SYS */
	{ 0x16020000, 0x10000, "mediatek,mt8183-vcodec-dec" },	/* the H.264 decoder */
	{ 0x17020000, 0x1000, "mediatek,mt8183-vcodec-enc" },	/* the H.264 encoder */
	{ 0x1502b000, 0x1000, "mediatek,mt8183-fd" },		/* FDVT */
	{ 0x15022000, 0x6000, "mediatek,mt8183-dip" },		/* ISP pass 2, its own settings */
};

static struct {
	void __iomem *mbox, *trail;
	u32 seq;
	unsigned long served;
	atomic_t busy;		/* messages out to the SCP */
	unsigned long last;	/* jiffies of the last access served */
	unsigned long active;	/* jiffies of the last access or message */
	struct task_struct *thread;
} mmproxy;

/*
 * Ready from the start: scp_ipi_send brackets messages with mtk_scp_mmproxy_busy from the SCP's first
 * one, well before this file's late_initcall.
 */
static DECLARE_WAIT_QUEUE_HEAD(mmproxy_wq);

/* scp_ipi_send brackets each message with these */
void mtk_scp_mmproxy_busy(bool busy)
{
	if (busy) {
		WRITE_ONCE(mmproxy.active, jiffies);
		atomic_inc(&mmproxy.busy);
		wake_up(&mmproxy_wq);
	} else {
		atomic_dec(&mmproxy.busy);
	}
}

static struct device *mmproxy_owner(struct mmproxy_window *w)
{
	struct device_node *np;
	struct platform_device *pdev;

	if (w->dev)
		return w->dev;
	np = of_find_compatible_node(NULL, NULL, w->owner);
	if (!np)
		return NULL;
	pdev = of_find_device_by_node(np);
	of_node_put(np);
	if (pdev)
		w->dev = &pdev->dev;	/* kept: the devices stay */
	return w->dev;
}

static void __iomem *mmproxy_reg(u32 addr, bool *refused)
{
	int i;

	for (i = 0; i < ARRAY_SIZE(windows); i++) {
		struct mmproxy_window *w = &windows[i];
		struct device *dev;

		if (addr - w->base >= w->size)
			continue;
		dev = mmproxy_owner(w);
		if (!dev || !pm_runtime_active(dev)) {
			pr_err_ratelimited("scp mmproxy: %#x while %s is off, refused\n", addr, w->owner);
			*refused = true;
			return NULL;
		}
		if (!w->regs)
			w->regs = ioremap(w->base, w->size);
		return w->regs ? w->regs + (addr - w->base) : NULL;
	}
	pr_warn_ratelimited("scp mmproxy: %#x is no block it serves, refused\n", addr);
	*refused = true;
	return NULL;
}

/* One access, if one is posted. True if one was. */
static bool mmproxy_serve_one(void)
{
	void __iomem *m = mmproxy.mbox, *t = NULL, *r;
	u32 seq = readl(m), op, addr, val = 0, size;
	bool refused = false;

	if (seq == mmproxy.seq)
		return false;
	rmb();
	op = readl(m + 4);
	addr = readl(m + 8);
	size = (op >> 4) & 0xf;
	if (mmproxy_mode != 2)
		pr_info("scp mmproxy: %s%u %#x = %#x\n", op & 1 ? "write" : "read", size * 8, addr,
			op & 1 ? readl(m + 12) : 0);
	if (mmproxy_mode >= 2 && (r = mmproxy_reg(addr, &refused))) {
		if (mmproxy_mode == 3 && mmproxy.trail) {
			t = mmproxy.trail + 16 + (mmproxy.served % 255) * 16;
			writel(mmproxy.served, t);
			writel(op | BIT(31), t + 4);
			writel(addr, t + 8);
			writel(op & 1 ? readl(m + 12) : 0, t + 12);
			writel(mmproxy.served + 1, mmproxy.trail + 4);
			wmb();
		}
		if (op & 1) {
			val = readl(m + 12);
			if (size == 1)
				writeb(val, r);
			else if (size == 2)
				writew(val, r);
			else
				writel(val, r);
		} else {
			val = size == 1 ? readb(r) : size == 2 ? readw(r) : readl(r);
		}
		if (t) {
			writel(op, t + 4);
			if (!(op & 1))
				writel(val, t + 12);
		}
	}
	writel(val, m + 20);
	wmb();
	writel(seq, m + 16);
	mmproxy.seq = seq;
	mmproxy.served++;
	mmproxy.last = mmproxy.active = jiffies;
	return true;
}

static int mmproxy_thread(void *unused)
{
	while (!kthread_should_stop()) {
		if (mmproxy_serve_one())
			continue;
		if (atomic_read(&mmproxy.busy) || time_before(jiffies, mmproxy.last + msecs_to_jiffies(20))) {
			cpu_relax();
			cond_resched();
			continue;
		}
		if (!mmproxy_mode) {
			wait_event_idle_timeout(mmproxy_wq, kthread_should_stop(), HZ);
			continue;
		}
		if (time_before(jiffies, READ_ONCE(mmproxy.active) + HZ)) {
			usleep_range_idle(800, 1200);
			continue;
		}
		/* idle: until a message goes out (mtk_scp_mmproxy_busy wakes us) */
		wait_event_idle_timeout(mmproxy_wq, atomic_read(&mmproxy.busy) || kthread_should_stop(), HZ);
	}
	return 0;
}

static int __init mmproxy_init(void)
{
	void __iomem *t;

	if (!of_machine_is_compatible("amzn,athena"))
		return 0;
	mmproxy.mbox = ioremap(MMPROXY_MBOX, 0x1000);
	if (!mmproxy.mbox)
		return -ENOMEM;
	t = mmproxy.trail = ioremap(MMPROXY_TRAIL, 0x1000);
	if (t) {
		u32 n = readl(t + 4), i;

		/* the trail an earlier boot left: the last accesses before a hang */
		if (readl(t) == MMPROXY_TRAIL_MAGIC && n)
			for (i = n > 32 ? n - 32 : 0; i < n; i++) {
				void __iomem *e = t + 16 + (i % 255) * 16;

				pr_info("scp mmproxy trail: %u op %#x %#x = %#x\n", readl(e),
					readl(e + 4), readl(e + 8), readl(e + 12));
			}
		writel(MMPROXY_TRAIL_MAGIC, t);
		writel(0, t + 4);
	}
	mmproxy.seq = readl(mmproxy.mbox);
	writel(mmproxy.seq, mmproxy.mbox + 16);
	mmproxy.thread = kthread_run(mmproxy_thread, NULL, "scp-mmproxy");
	return PTR_ERR_OR_ZERO(mmproxy.thread);
}
late_initcall(mmproxy_init);
