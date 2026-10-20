// SPDX-License-Identifier: GPL-2.0-only
/*
 * mdptest: a debugfs harness for experiments with the MT8163 MDP (RDMA, RSZ0/1, WDMA, WROT) on the
 * Echo Show 5 2nd gen, memory to memory, beside the running display and camera.
 *
 * It allocates its buffers on the camera node's device, in the IOMMU domain every multimedia
 * master shares, and puts larb0's MDP ports (RDMA 14, WDMA 15, WROT 16) in translated mode while
 * it is powered (SMI_LARB_MMU_EN; the display's ports on larb0 are left as they are). It enables
 * the MDP's clocks in mmsys through the clock framework.
 *
 * Writes are limited to the MDP blocks (0x14001000-0x14006fff), MMSYS's MDP routing registers
 * (ISP/RDMA/PRZ0/PRZ1/TDSHP MOUT_EN 0x01c-0x02c, PRZ0/PRZ1/TDSHP/WDMA/WROT SEL_IN 0x044-0x054),
 * its MDP reset bits (SW0_RST_B 0x140 bits 2-8, through "rst"), and the registers of mutex 6
 * (0x140150e0-0x140150ff), which the display does not use. Reads anywhere in those pages.
 *
 * debugfs, under /sys/kernel/debug/mdptest:
 *   ctl         commands (one per line or ';'), see do_cmd(); results go to "log" (read ctl)
 *   yuyv i420 aux   the DMA buffers
 *   info        buffer IOVAs and state
 */
#include <linux/clk.h>
#include <linux/clk-provider.h>
#include <linux/debugfs.h>
#include <linux/delay.h>
#include <linux/dma-mapping.h>
#include <linux/io.h>
#include <linux/ktime.h>
#include <linux/module.h>
#include <linux/mutex.h>
#include <linux/of.h>
#include <linux/of_platform.h>
#include <linux/platform_device.h>
#include <linux/slab.h>
#include <linux/string.h>
#include <linux/uaccess.h>

#define MM_PHYS		0x14000000
#define MM_LEN		0x7000		/* MMSYS config and the MDP blocks */
#define MUTEX_PHYS	0x14015000
#define LARB0_PHYS	0x14016000
#define LARB_MMU_EN	0xf00
#define LARB0_MDP_PORTS	(BIT(14) | BIT(15) | BIT(16))
#define SW0_RST_B	0x140
#define RST_MDP		GENMASK(8, 2)
#define LOG_SIZE	(256 * 1024)

/* CLK_MM_CAM_MDP .. CLK_MM_MDP_WROT in dt-bindings/clock/mediatek,mt8163-mmsys.h */
#define NCLK 7
static const unsigned int clk_ids[NCLK] = { 2, 3, 4, 5, 6, 7, 8 };

struct buf {
	const char *name;
	size_t size;
	void *va;
	dma_addr_t iova;
};

static struct buf bufs[] = {
	{ "yuyv", PAGE_ALIGN(1600 * 1200 * 2 + 65536) },
	{ "i420", PAGE_ALIGN(1600 * 1200 * 3 / 2 + 65536) },
	{ "aux", 1 << 20 },
};

static struct {
	struct device *dev;
	struct clk *clks[NCLK];
	void __iomem *mm, *mutex, *larb0;
	bool on;
	u32 mmu_saved;
	struct dentry *dir;
	struct mutex lock;
	char *log;
	size_t loglen;
} t;

static __printf(1, 2) void tlog(const char *fmt, ...)
{
	va_list ap;
	int n;

	if (t.loglen >= LOG_SIZE - 1)
		return;
	va_start(ap, fmt);
	n = vscnprintf(t.log + t.loglen, LOG_SIZE - t.loglen, fmt, ap);
	va_end(ap);
	t.loglen += n;
}

/* An absolute address, mapped; writable says whether a write there is allowed. */
static void __iomem *reg(u32 addr, bool write)
{
	if (addr & 3)
		return NULL;
	if (addr >= MM_PHYS + 0x1000 && addr < MM_PHYS + MM_LEN)
		return t.mm + (addr - MM_PHYS);
	if (addr >= MM_PHYS && addr < MM_PHYS + 0x1000) {
		u32 o = addr - MM_PHYS;

		if (write && !((o >= 0x01c && o <= 0x02c) || (o >= 0x044 && o <= 0x054)))
			return NULL;
		return t.mm + o;
	}
	if (addr >= MUTEX_PHYS && addr < MUTEX_PHYS + 0x1000) {
		u32 o = addr - MUTEX_PHYS;

		if (write && (o < 0xe0 || o > 0xfc))
			return NULL;
		return t.mutex + o;
	}
	return NULL;
}

static void mdp_reset(u32 bits)
{
	u32 v = readl(t.mm + SW0_RST_B);

	bits &= RST_MDP;
	writel(v & ~bits, t.mm + SW0_RST_B);
	udelay(5);
	writel(v | bits, t.mm + SW0_RST_B);
}

static void routes_off(void)
{
	u32 o;

	for (o = 0x01c; o <= 0x02c; o += 4)
		writel(0, t.mm + o);
	for (o = 0x044; o <= 0x054; o += 4)
		writel(0, t.mm + o);
	writel(0, t.mutex + 0xe0);	/* mutex 6: off, no modules */
	writel(0, t.mutex + 0xec);
}

static int power_on(void)
{
	int i, ret;

	if (t.on)
		return 0;
	for (i = 0; i < NCLK; i++) {
		ret = clk_prepare_enable(t.clks[i]);
		if (ret) {
			while (--i >= 0)
				clk_disable_unprepare(t.clks[i]);
			return ret;
		}
	}
	t.mmu_saved = readl(t.larb0 + LARB_MMU_EN);
	writel(t.mmu_saved | LARB0_MDP_PORTS, t.larb0 + LARB_MMU_EN);
	t.on = true;
	tlog("on: larb0 MMU_EN %#x -> %#x, mmsys CG_CON0 %#x, SW0_RST_B %#x\n", t.mmu_saved,
	     readl(t.larb0 + LARB_MMU_EN), readl(t.mm + 0x100), readl(t.mm + SW0_RST_B));
	return 0;
}

static void power_off(void)
{
	int i;

	if (!t.on)
		return;
	routes_off();
	mdp_reset(RST_MDP);	/* every MDP engine stopped before the buffers can go */
	writel(t.mmu_saved | (readl(t.larb0 + LARB_MMU_EN) & ~LARB0_MDP_PORTS),
	       t.larb0 + LARB_MMU_EN);
	for (i = NCLK - 1; i >= 0; i--)
		clk_disable_unprepare(t.clks[i]);
	t.on = false;
	tlog("off\n");
}

/* A number, or @buffer[+offset] for a buffer's IOVA. */
static int parse_val(const char *s, u32 *v)
{
	unsigned int i;

	if (*s == '@') {
		for (i = 0; i < ARRAY_SIZE(bufs); i++) {
			size_t n = strlen(bufs[i].name);
			u32 off = 0;

			if (strncmp(s + 1, bufs[i].name, n))
				continue;
			if (s[1 + n] == '+') {
				if (kstrtou32(s + 2 + n, 0, &off) || off >= bufs[i].size)
					return -EINVAL;
			} else if (s[1 + n]) {
				continue;
			}
			*v = bufs[i].iova + off;
			return 0;
		}
		return -EINVAL;
	}
	return kstrtou32(s, 0, v);
}

static struct buf *find_buf(const char *name)
{
	unsigned int i;

	for (i = 0; i < ARRAY_SIZE(bufs); i++)
		if (!strcmp(bufs[i].name, name))
			return &bufs[i];
	return NULL;
}

static int do_cmd(char *line)
{
	char *argv[12];
	int argc = 0;
	u32 addr, val, mask, n;
	void __iomem *r;
	unsigned int i;
	char *p;

	while ((p = strsep(&line, " \t")) && argc < ARRAY_SIZE(argv))
		if (*p)
			argv[argc++] = p;
	if (!argc || argv[0][0] == '#')
		return 0;

	if (!strcmp(argv[0], "clear")) {
		t.loglen = 0;
		return 0;
	}
	if (!strcmp(argv[0], "on"))
		return power_on();
	if (!strcmp(argv[0], "off")) {
		power_off();
		return 0;
	}
	if (!strcmp(argv[0], "fill") && argc == 3) {	/* fill BUF BYTE */
		struct buf *bf = find_buf(argv[1]);

		if (!bf || kstrtou32(argv[2], 0, &val))
			return -EINVAL;
		memset(bf->va, val, bf->size);
		return 0;
	}
	if (!strcmp(argv[0], "us") && argc == 2) {	/* us N: sleep */
		if (kstrtou32(argv[1], 0, &val) || val > 1000000)
			return -EINVAL;
		usleep_range(val, val + 10);
		return 0;
	}
	if (!t.on) {
		tlog("%s: not powered\n", argv[0]);
		return -ENODEV;
	}
	if (!strcmp(argv[0], "rst")) {	/* rst [BITS]: MMSYS reset of MDP engines */
		val = RST_MDP;
		if (argc > 1 && kstrtou32(argv[1], 0, &val))
			return -EINVAL;
		mdp_reset(val);
		return 0;
	}
	if (!strcmp(argv[0], "w") && argc >= 3) {	/* w ADDR VAL [MASK]: GCE-style masked write */
		if (kstrtou32(argv[1], 0, &addr) || parse_val(argv[2], &val))
			return -EINVAL;
		mask = ~0U;
		if (argc > 3 && parse_val(argv[3], &mask))
			return -EINVAL;
		r = reg(addr, true);
		if (!r) {
			tlog("w %08x: not allowed\n", addr);
			return -EPERM;
		}
		if (mask != ~0U)
			val = (readl(r) & ~mask) | (val & mask);
		writel(val, r);
		return 0;
	}
	if (!strcmp(argv[0], "r") && argc >= 2) {	/* r ADDR [N] */
		if (kstrtou32(argv[1], 0, &addr))
			return -EINVAL;
		n = 1;
		if (argc > 2 && kstrtou32(argv[2], 0, &n))
			return -EINVAL;
		for (i = 0; i < n; i++) {
			r = reg(addr + 4 * i, false);
			if (!r)
				break;
			if (!(i % 8))
				tlog("%s%08x:", i ? "\n" : "", addr + 4 * i);
			tlog(" %08x", readl(r));
		}
		tlog("\n");
		return 0;
	}
	if (!strcmp(argv[0], "p") && argc == 5) {	/* p ADDR VAL MASK MS: poll (reg & MASK) == VAL */
		ktime_t t0 = ktime_get(), end;
		u32 v;

		if (kstrtou32(argv[1], 0, &addr) || parse_val(argv[2], &val) ||
		    parse_val(argv[3], &mask) || kstrtou32(argv[4], 0, &n))
			return -EINVAL;
		r = reg(addr, false);
		if (!r)
			return -EINVAL;
		end = ktime_add_ms(t0, min(n, 1000U));
		while (((v = readl(r)) & mask) != val) {
			if (ktime_after(ktime_get(), end)) {
				tlog("p %08x: timed out, %08x\n", addr, v);
				return -ETIMEDOUT;
			}
			udelay(5);
		}
		tlog("p %08x: %08x after %lld us\n", addr, v, ktime_us_delta(ktime_get(), t0));
		return 0;
	}
	tlog("unknown: %s\n", argv[0]);
	return -EINVAL;
}

static ssize_t ctl_write(struct file *f, const char __user *ubuf, size_t len, loff_t *ppos)
{
	char *kbuf, *line, *cur;
	int ret = 0;

	if (len > 65536)
		return -E2BIG;
	kbuf = memdup_user_nul(ubuf, len);
	if (IS_ERR(kbuf))
		return PTR_ERR(kbuf);
	mutex_lock(&t.lock);
	cur = kbuf;
	while ((line = strsep(&cur, "\n;"))) {
		ret = do_cmd(line);
		if (ret) {
			tlog("error %d\n", ret);
			break;
		}
	}
	mutex_unlock(&t.lock);
	kfree(kbuf);
	return ret ? ret : len;
}

static ssize_t log_read(struct file *f, char __user *ubuf, size_t len, loff_t *ppos)
{
	ssize_t ret;

	mutex_lock(&t.lock);
	ret = simple_read_from_buffer(ubuf, len, ppos, t.log, t.loglen);
	mutex_unlock(&t.lock);
	return ret;
}

static ssize_t info_read(struct file *f, char __user *ubuf, size_t len, loff_t *ppos)
{
	char s[512];
	int n = 0;
	unsigned int i;

	for (i = 0; i < ARRAY_SIZE(bufs); i++)
		n += scnprintf(s + n, sizeof(s) - n, "%s iova=%#llx size=%zu\n", bufs[i].name,
			(unsigned long long)bufs[i].iova, bufs[i].size);
	n += scnprintf(s + n, sizeof(s) - n, "powered=%d\n", t.on);
	return simple_read_from_buffer(ubuf, len, ppos, s, n);
}

static ssize_t buf_read(struct file *f, char __user *ubuf, size_t len, loff_t *ppos)
{
	struct buf *bf = f->private_data;

	return simple_read_from_buffer(ubuf, len, ppos, bf->va, bf->size);
}

static ssize_t buf_write(struct file *f, const char __user *ubuf, size_t len, loff_t *ppos)
{
	struct buf *bf = f->private_data;

	return simple_write_to_buffer(bf->va, bf->size, ppos, ubuf, len);
}

static const struct file_operations ctl_fops = {
	.owner = THIS_MODULE, .write = ctl_write, .read = log_read, .llseek = default_llseek,
};
static const struct file_operations info_fops = {
	.owner = THIS_MODULE, .read = info_read, .llseek = default_llseek,
};
static const struct file_operations buf_fops = {
	.owner = THIS_MODULE, .open = simple_open, .read = buf_read, .write = buf_write,
	.llseek = default_llseek,
};

static void free_bufs(void)
{
	unsigned int i;

	for (i = 0; i < ARRAY_SIZE(bufs); i++) {
		if (bufs[i].va)
			dma_free_coherent(t.dev, bufs[i].size, bufs[i].va, bufs[i].iova);
		bufs[i].va = NULL;
	}
}

static void put_all(void)
{
	int i;

	if (t.larb0) iounmap(t.larb0);
	if (t.mutex) iounmap(t.mutex);
	if (t.mm) iounmap(t.mm);
	for (i = 0; i < NCLK; i++)
		if (!IS_ERR_OR_NULL(t.clks[i]))
			clk_put(t.clks[i]);
	if (t.dev) put_device(t.dev);
	kvfree(t.log);
}

static int __init mdptest_init(void)
{
	struct of_phandle_args spec = { .args_count = 1 };
	struct platform_device *pdev;
	struct device_node *np;
	unsigned int i;
	int ret;

	mutex_init(&t.lock);
	t.log = kvzalloc(LOG_SIZE, GFP_KERNEL);
	if (!t.log)
		return -ENOMEM;
	np = of_find_compatible_node(NULL, NULL, "amazon,cronos-camera");
	pdev = np ? of_find_device_by_node(np) : NULL;
	of_node_put(np);
	if (!pdev) { ret = -ENODEV; goto fail; }
	t.dev = &pdev->dev;
	if (!t.dev->driver) { ret = -EPROBE_DEFER; goto fail; }

	spec.np = of_find_compatible_node(NULL, NULL, "mediatek,mt8163-mmsys");
	if (!spec.np) { ret = -ENODEV; goto fail; }
	for (i = 0; i < NCLK; i++) {
		spec.args[0] = clk_ids[i];
		t.clks[i] = of_clk_get_from_provider(&spec);
		if (IS_ERR(t.clks[i])) {
			ret = PTR_ERR(t.clks[i]);
			of_node_put(spec.np);
			goto fail;
		}
	}
	of_node_put(spec.np);
	t.mm = ioremap(MM_PHYS, MM_LEN);
	t.mutex = ioremap(MUTEX_PHYS, 0x1000);
	t.larb0 = ioremap(LARB0_PHYS, 0x1000);
	if (!t.mm || !t.mutex || !t.larb0) { ret = -ENOMEM; goto fail; }

	for (i = 0; i < ARRAY_SIZE(bufs); i++) {
		bufs[i].va = dma_alloc_coherent(t.dev, bufs[i].size, &bufs[i].iova, GFP_KERNEL);
		if (!bufs[i].va) { ret = -ENOMEM; goto fail_bufs; }
		memset(bufs[i].va, 0, bufs[i].size);
	}

	t.dir = debugfs_create_dir("mdptest", NULL);
	debugfs_create_file("ctl", 0600, t.dir, NULL, &ctl_fops);
	debugfs_create_file("info", 0400, t.dir, NULL, &info_fops);
	for (i = 0; i < ARRAY_SIZE(bufs); i++)
		debugfs_create_file_size(bufs[i].name, 0600, t.dir, &bufs[i], &buf_fops, bufs[i].size);
	pr_info("mdptest: yuyv %pad i420 %pad aux %pad\n", &bufs[0].iova, &bufs[1].iova,
		&bufs[2].iova);
	return 0;

fail_bufs:
	free_bufs();
fail:
	put_all();
	return ret;
}

static void __exit mdptest_exit(void)
{
	debugfs_remove_recursive(t.dir);
	mutex_lock(&t.lock);
	power_off();
	mutex_unlock(&t.lock);
	free_bufs();
	put_all();
}

module_init(mdptest_init);
module_exit(mdptest_exit);
MODULE_DESCRIPTION("MT8163 MDP experiment harness (cronos)");
MODULE_LICENSE("GPL");
