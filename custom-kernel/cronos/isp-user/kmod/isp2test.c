// SPDX-License-Identifier: GPL-2.0-only
/*
 * isp2test: a debugfs harness for experiments with MT8163 ISP pass 2 (memory to memory) on the
 * Echo Show 5 2nd gen, used while the capture driver (cronos-camera, pass 1) is stopped.
 *
 * It borrows the camera node's device: its ISP power domain and imgsys clocks, its larb (larb2),
 * and its IOMMU domain, from which it allocates the pass-2 buffers. The larb's IMGI/IMG2O ports
 * are not in the node's "iommus", so while the harness is powered it puts every larb2 port in
 * translated mode (SMI_LARB_MMU_EN), and every pass-2 DMA goes through the IOMMU: a stray address
 * faults there instead of writing memory. The previous mode is restored on "off".
 *
 * Register access is limited to the camera node's CAM window (0x15004000-0x15007fff, writes and
 * reads) and the imgsys page (0x15000000-0x15000fff, reads only); the only other register it
 * writes is larb2's MMU_EN.
 *
 * debugfs, under /sys/kernel/debug/isp2test:
 *   ctl              write commands (one per line), see do_cmd(); results go to "log"
 *   log              read the results; "clear" empties it
 *   imgi img2o tdr aux   the DMA buffers, read/write at any offset
 *   info             buffer IOVAs and state
 */
#include <linux/clk.h>
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
#include <linux/pm_runtime.h>
#include <linux/slab.h>
#include <linux/string.h>
#include <linux/uaccess.h>

#define CAM_PHYS	0x15004000
#define CAM_LEN		0x4000
#define IMG_PHYS	0x15000000
#define IMG_LEN		0x1000
#define LARB2_PHYS	0x15001000
#define LARB_MMU_EN	0xf00
#define LARB2_PORTS	0x3f	/* IMGO, IMG2O, LSCI, IMGI, ESFKO, AAO */

#define SW_CTL		0x05c
#define DBG_SET		0x160
#define DBG_PORT	0x164
#define LOG_SIZE	(256 * 1024)

/* The read-clear status pairs: INT/DMA, INTB/DMAB, INTC/DMAC, INTX/DMAX. */
static const u32 status_regs[] = { 0x024, 0x028, 0x030, 0x034, 0x03c, 0x040, 0x044, 0x048 };
#define NSTATUS ARRAY_SIZE(status_regs)

struct buf {
	const char *name;
	size_t size;
	void *va;
	dma_addr_t iova;
};

static struct buf bufs[] = {
	{ "imgi", PAGE_ALIGN(2400000 + 65536) },	/* one packed RAW10 frame, and slack */
	{ "img2o", PAGE_ALIGN(1600 * 1200 * 2 + 65536) },	/* YUV422, and an overrun guard */
	{ "tdr", 65536 },
	{ "aux", 1 << 20 },	/* a target for every other DMA base */
};

static struct {
	struct device *dev, *larb;
	struct clk_bulk_data *clks;
	int nclks;
	void __iomem *cam, *img, *larbio;
	bool on;
	u32 mmu_saved;
	u32 acc[NSTATUS];
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

static void drain_status(u32 *v)
{
	unsigned int i;

	for (i = 0; i < NSTATUS; i++) {
		v[i] = readl(t.cam + status_regs[i]);
		t.acc[i] |= v[i];
	}
}

static void isp_reset(void)
{
	writel(5, t.cam + SW_CTL);
	udelay(10);
	writel(4, t.cam + SW_CTL);
	udelay(10);
	writel(0, t.cam + SW_CTL);
}

static int power_on(void)
{
	int ret;

	if (t.on)
		return 0;
	ret = pm_runtime_resume_and_get(t.dev);
	if (ret < 0)
		return ret;
	ret = pm_runtime_resume_and_get(t.larb);
	if (ret < 0)
		goto put_dev;
	ret = clk_bulk_prepare_enable(t.nclks, t.clks);
	if (ret)
		goto put_larb;
	t.mmu_saved = readl(t.larbio + LARB_MMU_EN);
	writel(t.mmu_saved | LARB2_PORTS, t.larbio + LARB_MMU_EN);
	t.on = true;
	tlog("on: larb2 MMU_EN %#x -> %#x\n", t.mmu_saved, readl(t.larbio + LARB_MMU_EN));
	return 0;
put_larb:
	pm_runtime_put_sync(t.larb);
put_dev:
	pm_runtime_put_sync(t.dev);
	return ret;
}

static void power_off(void)
{
	if (!t.on)
		return;
	/* Stop every DMA before the buffers can go away. */
	isp_reset();
	writel(0, t.cam + 0x00c);
	writel(0, t.cam + 0x004);
	writel(0, t.cam + 0x008);
	writel(0, t.cam + 0x054);
	isp_reset();
	writel(t.mmu_saved, t.larbio + LARB_MMU_EN);
	clk_bulk_disable_unprepare(t.nclks, t.clks);
	pm_runtime_put_sync(t.larb);
	pm_runtime_put_sync(t.dev);
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

static int cam_off(const char *s, u32 *off)
{
	if (kstrtou32(s, 0, off) || *off >= CAM_LEN || (*off & 3))
		return -EINVAL;
	return 0;
}

static struct buf *find_buf(const char *name)
{
	unsigned int i;

	for (i = 0; i < ARRAY_SIZE(bufs); i++)
		if (!strcmp(bufs[i].name, name))
			return &bufs[i];
	return NULL;
}

static void log_dbg(u32 sel)
{
	writel(sel, t.cam + DBG_SET);
	tlog(" dbg[%04x]=%08x", sel, readl(t.cam + DBG_PORT));
}

/*
 * run OFF VAL TIMEOUT_MS [WAITMASK]: drain the status words, write VAL to OFF (the start
 * register), then poll every 20 us until INT_STATUS has a bit of WAITMASK (default PASS2_DON,
 * bit 14) or the time is up. The status bits seen are accumulated and logged.
 */
static int cmd_run(u32 off, u32 val, u32 ms, u32 waitmask, int then, u32 toff, u32 tclr, u32 tset)
{
	u32 v[NSTATUS], seen[NSTATUS] = { 0 };
	ktime_t start, end;
	unsigned int i;
	s64 us;
	bool done = false;

	drain_status(v);
	memset(t.acc, 0, sizeof(t.acc));
	start = ktime_get();
	end = ktime_add_ms(start, min(ms, 5000U));
	writel(val, t.cam + off);
	if (then)	/* a masked write right after the start (a pass-1 bit put back) */
		writel((readl(t.cam + toff) & ~tclr) | tset, t.cam + toff);
	for (;;) {
		drain_status(v);
		for (i = 0; i < NSTATUS; i++)
			seen[i] |= v[i];
		if (seen[0] & waitmask) {
			done = true;
			break;
		}
		if (ktime_after(ktime_get(), end))
			break;
		udelay(20);
	}
	us = ktime_us_delta(ktime_get(), start);
	tlog("run %03x=%08x: %s after %lld us; start reg now %08x\n", off, val,
		done ? "DONE" : "TIMEOUT", us, readl(t.cam + off));
	tlog(" status INT %08x DMA %08x INTB %08x DMAB %08x INTC %08x DMAC %08x INTX %08x DMAX %08x\n",
		seen[0], seen[1], seen[2], seen[3], seen[4], seen[5], seen[6], seen[7]);
	tlog(" ");
	log_dbg(0x9003); log_dbg(0x9006); log_dbg(0x9009); log_dbg(0x900c);
	log_dbg(0x5000); log_dbg(0x5100); log_dbg(0x6000);
	tlog("\n");
	return done ? 0 : -ETIMEDOUT;
}

static int do_cmd(char *line)
{
	char *argv[12];
	int argc = 0, ret = 0;
	u32 off, val, a, b;
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
	if (!t.on) {
		tlog("%s: not powered\n", argv[0]);
		return -ENODEV;
	}

	if (!strcmp(argv[0], "reset")) {
		isp_reset();
		return 0;
	}
	if (!strcmp(argv[0], "w") && argc >= 3) {	/* w OFF VAL [VAL...]: consecutive words */
		if (cam_off(argv[1], &off))
			return -EINVAL;
		for (i = 2; i < argc; i++, off += 4) {
			if (off >= CAM_LEN || parse_val(argv[i], &val))
				return -EINVAL;
			writel(val, t.cam + off);
		}
		return 0;
	}
	if (!strcmp(argv[0], "wm") && argc == 4) {	/* wm OFF CLEAR SET */
		if (cam_off(argv[1], &off) || parse_val(argv[2], &a) || parse_val(argv[3], &b))
			return -EINVAL;
		writel((readl(t.cam + off) & ~a) | b, t.cam + off);
		return 0;
	}
	if ((!strcmp(argv[0], "r") || !strcmp(argv[0], "ri")) && argc >= 2) {	/* r OFF [N] */
		bool img = argv[0][1] == 'i';
		u32 n = 1, lim = img ? IMG_LEN : CAM_LEN;

		if (kstrtou32(argv[1], 0, &off) || (off & 3) || off >= lim)
			return -EINVAL;
		if (argc > 2 && kstrtou32(argv[2], 0, &n))
			return -EINVAL;
		for (i = 0; i < n && off + 4 * i < lim; i++) {
			u32 o = off + 4 * i;
			/* The status words clear on read: they are read only through "st". */
			bool rc = !img && o >= 0x024 && o < 0x04c;

			if (!(i % 8))
				tlog("%s%s %03x:", i ? "\n" : "", img ? "img" : "cam", o);
			if (rc)
				tlog(" --------");
			else
				tlog(" %08x", readl((img ? t.img : t.cam) + o));
		}
		tlog("\n");
		return 0;
	}
	if (!strcmp(argv[0], "st")) {
		u32 v[NSTATUS];

		drain_status(v);
		tlog("st INT %08x DMA %08x INTB %08x DMAB %08x INTC %08x DMAC %08x INTX %08x DMAX %08x\n",
			v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]);
		return 0;
	}
	if (!strcmp(argv[0], "dbg") && argc >= 2) {	/* dbg SEL [SEL...] */
		for (i = 1; i < argc; i++) {
			if (kstrtou32(argv[i], 0, &val))
				return -EINVAL;
			log_dbg(val);
		}
		tlog("\n");
		return 0;
	}
	if (!strcmp(argv[0], "run") && argc >= 4) {
		u32 ms, mask = BIT(14), toff = 0, tclr = 0, tset = 0;

		if (cam_off(argv[1], &off) || parse_val(argv[2], &val) ||
		    kstrtou32(argv[3], 0, &ms))
			return -EINVAL;
		if (argc > 4 && kstrtou32(argv[4], 0, &mask))
			return -EINVAL;
		/* run OFF VAL MS MASK TOFF TCLR TSET: and a masked write right after the start */
		if (argc > 7 && (cam_off(argv[5], &toff) || parse_val(argv[6], &tclr) ||
				 parse_val(argv[7], &tset)))
			return -EINVAL;
		ret = cmd_run(off, val, ms, mask, argc > 7, toff, tclr, tset);
		return ret == -ETIMEDOUT ? 0 : ret;
	}
	if (!strcmp(argv[0], "vs")) {	/* vs: wait for the TG's frame counter to move (100 ms at most) */
		u32 first = readl(t.cam + 0x44c) >> 16 & 0xff;
		ktime_t end = ktime_add_ms(ktime_get(), 100);

		while ((readl(t.cam + 0x44c) >> 16 & 0xff) == first) {
			if (ktime_after(ktime_get(), end)) {
				tlog("vs: no frame\n");
				return -ETIMEDOUT;
			}
			udelay(20);
		}
		return 0;
	}
	if (!strcmp(argv[0], "wait") && argc == 4) {	/* wait OFF MASK MS: poll until a bit of MASK */
		ktime_t t0 = ktime_get(), end;
		u32 v = 0;

		if (cam_off(argv[1], &off) || parse_val(argv[2], &a) || kstrtou32(argv[3], 0, &b))
			return -EINVAL;
		end = ktime_add_ms(t0, min(b, 1000U));
		while (!((v = readl(t.cam + off)) & a)) {
			if (ktime_after(ktime_get(), end)) {
				tlog("wait %03x: timed out\n", off);
				return -ETIMEDOUT;
			}
			udelay(10);
		}
		tlog("wait %03x: %08x after %lld us\n", off, v, ktime_us_delta(ktime_get(), t0));
		return 0;
	}
	if (!strcmp(argv[0], "us") && argc == 2) {	/* us N: sleep */
		if (kstrtou32(argv[1], 0, &val) || val > 1000000)
			return -EINVAL;
		usleep_range(val, val + 10);
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
	if (t.larbio) iounmap(t.larbio);
	if (t.img) iounmap(t.img);
	if (t.cam) iounmap(t.cam);
	if (t.clks) clk_bulk_put_all(t.nclks, t.clks);
	if (t.larb) put_device(t.larb);
	if (t.dev) put_device(t.dev);
	kvfree(t.log);
}

static int __init isp2test_init(void)
{
	struct device_node *np, *ln;
	struct platform_device *pdev;
	unsigned int i;
	int ret;

	mutex_init(&t.lock);
	t.log = kvzalloc(LOG_SIZE, GFP_KERNEL);
	if (!t.log)
		return -ENOMEM;
	np = of_find_compatible_node(NULL, NULL, "amazon,cronos-camera");
	if (!np) { ret = -ENODEV; goto fail; }
	pdev = of_find_device_by_node(np);
	ln = of_parse_phandle(np, "mediatek,larb", 0);
	of_node_put(np);
	if (!pdev || !ln) { of_node_put(ln); ret = -ENODEV; goto fail; }
	t.dev = &pdev->dev;
	pdev = of_find_device_by_node(ln);
	of_node_put(ln);
	if (!pdev) { ret = -ENODEV; goto fail; }
	t.larb = &pdev->dev;
	if (!t.dev->driver || !t.larb->driver) { ret = -EPROBE_DEFER; goto fail; }

	t.nclks = clk_bulk_get_all(t.dev, &t.clks);
	if (t.nclks < 0) { ret = t.nclks; t.clks = NULL; goto fail; }
	t.cam = ioremap(CAM_PHYS, CAM_LEN);
	t.img = ioremap(IMG_PHYS, IMG_LEN);
	t.larbio = ioremap(LARB2_PHYS, 0x1000);
	if (!t.cam || !t.img || !t.larbio) { ret = -ENOMEM; goto fail; }

	for (i = 0; i < ARRAY_SIZE(bufs); i++) {
		bufs[i].va = dma_alloc_coherent(t.dev, bufs[i].size, &bufs[i].iova, GFP_KERNEL);
		if (!bufs[i].va) { ret = -ENOMEM; goto fail_bufs; }
		memset(bufs[i].va, 0, bufs[i].size);
	}

	t.dir = debugfs_create_dir("isp2test", NULL);
	debugfs_create_file("ctl", 0600, t.dir, NULL, &ctl_fops);
	debugfs_create_file("log", 0400, t.dir, NULL, &ctl_fops);
	debugfs_create_file("info", 0400, t.dir, NULL, &info_fops);
	for (i = 0; i < ARRAY_SIZE(bufs); i++)
		debugfs_create_file_size(bufs[i].name, 0600, t.dir, &bufs[i], &buf_fops, bufs[i].size);
	pr_info("isp2test: %d clocks, imgi %pad img2o %pad tdr %pad aux %pad\n", t.nclks,
		&bufs[0].iova, &bufs[1].iova, &bufs[2].iova, &bufs[3].iova);
	return 0;

fail_bufs:
	free_bufs();
fail:
	put_all();
	return ret;
}

static void __exit isp2test_exit(void)
{
	debugfs_remove_recursive(t.dir);
	mutex_lock(&t.lock);
	power_off();
	mutex_unlock(&t.lock);
	free_bufs();
	put_all();
}

module_init(isp2test_init);
module_exit(isp2test_exit);
MODULE_DESCRIPTION("MT8163 ISP pass-2 experiment harness (cronos)");
MODULE_LICENSE("GPL");
