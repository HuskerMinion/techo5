// SPDX-License-Identifier: GPL-2.0
/*
 * techo5_vpu.c: the MT8183's VPU (Cadence Vision P6 core 0) for user space
 * (custom-kernel/athena/research/vpu-vision-p6.md).
 *
 * The device's node (patches/vpu.py) carries the VPU's power domain and every clock Fire OS turns on
 * for core 0, and a no-map region the core runs from, which it reaches at the same address with the
 * M4U bypassed.
 *
 * /dev/techo5-vpu runs it the way MediaTek's driver does, one command at a time:
 *
 *   TECHO5_VPU_BOOT	the firmware image (the unit's own cam_vpu1, laid out by echod's hardware/vpu
 *			or tools/vpu/mkregion.py: core 0's segments at their addresses and its IRAM blob
 *			at 0x7dd00000) goes into the region, the core is powered and boots from it
 *			(MT6771's vpu_hw_boot_sequence, with the AXI defaults kept) and takes SET_DBG
 *   TECHO5_VPU_LOAD	an algorithm (an ELF of cam_vpu2/3, as stored) goes in and DO_LOADER loads it
 *   TECHO5_VPU_D2D	a job: the vpu_buffer array and the settings are written into the region, the
 *			images are in its data area (mmap of the device, offset 0 = DATA_PA)
 *   TECHO5_VPU_SYNC	cache maintenance on a range of the data area, for its cached mapping
 *
 * The data area maps write-combined at offset 0, and cached at TECHO5_VPU_MMAP_CACHED. The MT8183's
 * VPU does not snoop the cores' caches, so the cached mapping wants SYNC FOR_DEVICE on what the cores
 * wrote before a job reads it and FOR_CPU on what a job wrote before the cores read it; for that the
 * kernel maps the data area cached too, and maintains the caches by that alias (they are physically
 * tagged). Reading a job's output that way is many times quicker than through the uncached mapping
 * (80-400 MB/s on these cores).
 *
 * A command is done when INFO00 reads READY again with DONE_ST's PWAITMODE set; the driver sleeps
 * between looks. An exception in the firmware leaves INFO00 at ERR (8); that, or a command that never
 * ends, powers the core off, and it takes a BOOT again.
 *
 * Power: the core and its clocks are on from a BOOT until the device is closed (or a command fails),
 * and off otherwise, so a VPU nobody has open costs nothing. One opener at a time: the core keeps one
 * firmware and one algorithm, and whoever booted it owns both.
 *
 * The device is root's alone: the VPU's DMA reaches any physical address the caller names in a job.
 *
 * With TECHO5_VPU_DEBUG, debugfs/techo5-vpu/, for research (tools/vpu/):
 *
 *   power	1 powers the core and its clocks, 0 lets them go
 *   regs	the core's window, AP 0x19100000 + 0x94000 (DMEM0/1, IMEM, CTRL at 0x80000): pread/pwrite
 *		in whole 32-bit words, only while powered
 *   mem	the no-map region the firmware runs from (PA 0x7da00000, 22 MiB): pread/pwrite
 *   log	the log the firmware and the algorithm print (SET_DBG's buffer)
 */

#include <linux/clk.h>
#include <linux/debugfs.h>
#include <linux/delay.h>
#include <linux/io.h>
#include <linux/iopoll.h>
#include <linux/miscdevice.h>
#include <linux/mm.h>
#include <linux/module.h>
#include <linux/of.h>
#include <linux/of_address.h>
#include <linux/platform_device.h>
#include <linux/pm_runtime.h>
#include <linux/uaccess.h>

#include <asm/cacheflush.h>

#include "techo5_vpu_uapi.h"

#define VPU_REGS	0x19100000
#define VPU_REGS_SIZE	0x94000

/* The core's control block (offsets in the window) */
#define VPU_CTRL	0x80110		/* bit 23 RUN_STALL, 17 PIF_GATED */
#define VPU_RESET	0x8000c		/* bit 12 OCDHALTONRESET, 4 B_RST, 8 D_RST */
#define VPU_DONE_ST	0x8010c		/* bit 7 PWAITMODE */
#define VPU_XTENSA_INT	0x80114		/* the core's interrupt to the AP: write 1 clears it */
#define VPU_CTL_INT	0x80118		/* the AP's interrupt to the core */
#define VPU_AXI0	0x8013c
#define VPU_AXI1	0x80140
#define VPU_INFO(n)	(0x80150 + 4 * (n))
#define VPU_PC		0x801e4
#define VPU_ALTRESETVEC	0x801f8

#define VPU_READY	1
#define VPU_ERR		8
#define VPU_CMD_DO_LOADER	0x01
#define VPU_CMD_DO_D2D		0x22
#define VPU_CMD_SET_DBG		0x40

/* The region (PA = the core's address): firmware, then core 1's unused area for ours */
#define FW_PA		0x7da00000	/* core 0's image, 4 MiB with the IRAM blob at +0x300000 */
#define FW_SIZE		0x400000
#define IRAM_PA		0x7dd00000
#define ALGO_PA		0x7e800000	/* the algorithm's ELF, as stored, up to 1 MiB */
#define ALGO_SIZE	0x100000
#define LOG_PA		0x7e900000	/* SET_DBG's log buffer */
#define LOG_SIZE	0x10000
#define DESC_PA		0x7ea00000	/* the vpu_buffer array */
#define SETTINGS_PA	0x7ea01000
#define DATA_PA		0x7eb00000	/* images, to the region's end */

struct tvpu {
	struct device *dev;
	struct clk_bulk_data *clks;
	int nclks;
	void __iomem *regs;
	void *mem;		/* the region up to the data area, write-combined */
	void *data;		/* the data area, cached */
	phys_addr_t mem_pa;
	size_t mem_size;
	size_t data_size;
	bool on;
	bool booted;
	struct mutex lock;
	atomic_t opened;
	struct dentry *dir;
	struct miscdevice misc;
};

static struct tvpu *tvpu_the;

static void tvpu_wr(struct tvpu *v, u32 o, u32 val);
static u32 tvpu_rd(struct tvpu *v, u32 o);

static int tvpu_power(struct tvpu *v, bool on)
{
	int ret = 0;

	if (on == v->on)
		return 0;
	if (on) {
		ret = pm_runtime_resume_and_get(v->dev);
		if (ret)
			return ret;
		ret = clk_bulk_prepare_enable(v->nclks, v->clks);
		if (ret) {
			pm_runtime_put_sync(v->dev);
			return ret;
		}
	} else {
		/* stalled first: nothing of the core runs into its clocks going away */
		tvpu_wr(v, VPU_CTRL, tvpu_rd(v, VPU_CTRL) | BIT(23));
		clk_bulk_disable_unprepare(v->nclks, v->clks);
		pm_runtime_put_sync(v->dev);
		v->booted = false;
	}
	v->on = on;
	return 0;
}

static void *tvpu_at(struct tvpu *v, phys_addr_t pa)
{
	return v->mem + (pa - v->mem_pa);
}

static u32 tvpu_rd(struct tvpu *v, u32 o) { return readl(v->regs + o); }
static void tvpu_wr(struct tvpu *v, u32 o, u32 val) { writel(val, v->regs + o); }

/* One command: its arguments in INFOn, then the core is let run and interrupted until it is done */
static int tvpu_cmd(struct tvpu *v, u32 cmd, const u32 (*args)[2], int nargs, u32 timeout_ms)
{
	u32 st;
	int i, ret;

	for (i = 0; i < nargs; i++)
		tvpu_wr(v, VPU_INFO(args[i][0]), args[i][1]);
	tvpu_wr(v, VPU_INFO(0), 0);
	tvpu_wr(v, VPU_INFO(1), cmd);
	tvpu_wr(v, VPU_XTENSA_INT, 1);
	wmb();
	tvpu_wr(v, VPU_CTRL, tvpu_rd(v, VPU_CTRL) & ~BIT(23));
	tvpu_wr(v, VPU_CTL_INT, 1);
	ret = readl_poll_timeout(v->regs + VPU_INFO(0), st,
				 st == VPU_ERR || (st == VPU_READY &&
						   (tvpu_rd(v, VPU_DONE_ST) & BIT(7))),
				 100, timeout_ms * 1000);
	tvpu_wr(v, VPU_XTENSA_INT, 1);
	tvpu_wr(v, VPU_CTRL, tvpu_rd(v, VPU_CTRL) | BIT(23));
	if (ret) {
		dev_err(v->dev, "command %#x: no answer (INFO00 %#x, stage %#x, PC %#x)\n", cmd,
			tvpu_rd(v, VPU_INFO(0)), tvpu_rd(v, VPU_INFO(25)), tvpu_rd(v, VPU_PC));
		tvpu_power(v, false);
		return -ETIMEDOUT;
	}
	if (st == VPU_ERR) {
		dev_err(v->dev, "command %#x: the firmware took an exception (stage %#x)\n", cmd,
			tvpu_rd(v, VPU_INFO(25)));
		tvpu_power(v, false);	/* parked until booted again */
		return -EIO;
	}
	return 0;
}

/* MT6771's vpu_hw_boot_sequence for core 0, AXI defaults kept (zeroed, iDMA writes never land) */
static int tvpu_boot(struct tvpu *v, const void __user *image, u32 size)
{
	static const u32 dbg[][2] = {
		{ 19, IRAM_PA }, { 21, LOG_PA }, { 22, LOG_SIZE }, { 23, 0 }, { 29, 0x18070300 },
	};
	u32 c, r, st;
	int ret;

	if (!size || size > FW_SIZE)
		return -EINVAL;
	/* a core that was running is power-cycled: an exception leaves it parked */
	tvpu_power(v, false);
	if (copy_from_user(tvpu_at(v, FW_PA), image, size))
		return -EFAULT;
	memset(tvpu_at(v, LOG_PA), 0, LOG_SIZE);
	wmb();
	ret = tvpu_power(v, true);
	if (ret)
		return ret;
	tvpu_wr(v, VPU_ALTRESETVEC, FW_PA);
	c = tvpu_rd(v, VPU_CTRL);
	c = (c | BIT(31) | BIT(26) | BIT(19) | BIT(23) | BIT(17)) & ~(7 << 27);
	tvpu_wr(v, VPU_CTRL, c);
	r = tvpu_rd(v, VPU_RESET);
	tvpu_wr(v, VPU_RESET, r | BIT(12));
	tvpu_wr(v, VPU_RESET, r);
	tvpu_wr(v, VPU_RESET, r | BIT(4) | BIT(8));
	tvpu_wr(v, VPU_RESET, r);
	tvpu_wr(v, VPU_CTRL, tvpu_rd(v, VPU_CTRL) & ~BIT(17));		/* PIF ungated */
	tvpu_wr(v, VPU_AXI0, (tvpu_rd(v, VPU_AXI0) & ~(BIT(22) | BIT(27))) | BIT(28));
	tvpu_wr(v, VPU_AXI1, tvpu_rd(v, VPU_AXI1) & ~(BIT(4) | BIT(9)));
	tvpu_wr(v, VPU_CTRL, tvpu_rd(v, VPU_CTRL) & ~BIT(23));		/* run */
	ret = readl_poll_timeout(v->regs + VPU_INFO(0), st, st != 0, 1000, 5000000);
	tvpu_wr(v, VPU_CTRL, tvpu_rd(v, VPU_CTRL) | BIT(23));
	if (ret) {
		dev_err(v->dev, "the firmware did not come up (PC %#x)\n", tvpu_rd(v, VPU_PC));
		tvpu_power(v, false);
		return ret;
	}
	ret = tvpu_cmd(v, VPU_CMD_SET_DBG, dbg, ARRAY_SIZE(dbg), 1000);
	if (ret)
		return ret;
	v->booted = true;
	dev_dbg(v->dev, "firmware %#x booted\n", tvpu_rd(v, VPU_INFO(20)));
	return 0;
}

static int tvpu_load(struct tvpu *v, const void __user *elf, u32 size)
{
	const u32 args[][2] = { { 12, ALGO_PA }, { 13, size }, { 15, 312000 }, { 16, 312000 } };

	if (!v->booted)
		return -ENODEV;
	if (!size || size > ALGO_SIZE)
		return -EINVAL;
	if (copy_from_user(tvpu_at(v, ALGO_PA), elf, size))
		return -EFAULT;
	wmb();
	return tvpu_cmd(v, VPU_CMD_DO_LOADER, args, ARRAY_SIZE(args), 1000);
}

static int tvpu_d2d(struct tvpu *v, struct techo5_vpu_d2d *j)
{
	const u32 args[][2] = {
		{ 12, j->nbufs }, { 13, DESC_PA }, { 14, SETTINGS_PA }, { 15, j->settings_len },
	};
	u32 data_size = v->data_size;
	u8 *desc = tvpu_at(v, DESC_PA);
	u32 i;
	int ret;

	if (!v->booted)
		return -ENODEV;
	if (!j->nbufs || j->nbufs > TECHO5_VPU_MAX_BUFS ||
	    j->settings_len > sizeof(j->settings) || j->timeout_ms > 10000)
		return -EINVAL;
	for (i = 0; i < j->nbufs; i++) {
		const struct techo5_vpu_buf *b = &j->bufs[i];
		u32 w[7] = { b->width, b->height, 0, b->stride, b->length, DATA_PA + b->offset, 0 };

		if (b->offset & 63 || b->offset > data_size || b->length > data_size - b->offset)
			return -EINVAL;
		/* vpu_buffer: port, format, planes, 0, then the plane (64 bytes in all) */
		memset(desc + 64 * i, 0, 64);
		desc[64 * i] = b->port;
		desc[64 * i + 1] = b->format;
		desc[64 * i + 2] = 1;
		memcpy(desc + 64 * i + 4, w, sizeof(w));
	}
	memcpy(tvpu_at(v, SETTINGS_PA), j->settings, j->settings_len);
	wmb();
	ret = tvpu_cmd(v, VPU_CMD_DO_D2D, args, ARRAY_SIZE(args), j->timeout_ms ?: 1000);
	j->stage = v->on ? tvpu_rd(v, VPU_INFO(25)) : 0;
	return ret;
}

/* Cache maintenance on the data area, by the kernel's cached alias of it */
static int tvpu_sync(struct tvpu *v, const struct techo5_vpu_sync *s)
{
	unsigned long start = (unsigned long)v->data + s->offset;

	if (!s->flags || s->flags & ~(TECHO5_VPU_SYNC_FOR_DEVICE | TECHO5_VPU_SYNC_FOR_CPU) ||
	    s->reserved || s->offset > v->data_size || s->length > v->data_size - s->offset)
		return -EINVAL;
	if (!s->length)
		return 0;
	if (s->flags & TECHO5_VPU_SYNC_FOR_DEVICE)
		dcache_clean_poc(start, start + s->length);
	if (s->flags & TECHO5_VPU_SYNC_FOR_CPU)
		dcache_inval_poc(start, start + s->length);
	return 0;
}

static long tvpu_ioctl(struct file *f, unsigned int cmd, unsigned long arg)
{
	struct tvpu *v = tvpu_the;
	void __user *p = (void __user *)arg;
	long ret;

	mutex_lock(&v->lock);
	switch (cmd) {
	case TECHO5_VPU_BOOT:
	case TECHO5_VPU_LOAD: {
		struct techo5_vpu_blob b;

		ret = -EFAULT;
		if (copy_from_user(&b, p, sizeof(b)))
			break;
		ret = cmd == TECHO5_VPU_BOOT ? tvpu_boot(v, u64_to_user_ptr(b.data), b.size) :
					       tvpu_load(v, u64_to_user_ptr(b.data), b.size);
		break;
	}
	case TECHO5_VPU_D2D: {
		struct techo5_vpu_d2d j;

		ret = -EFAULT;
		if (copy_from_user(&j, p, sizeof(j)))
			break;
		ret = tvpu_d2d(v, &j);
		if (copy_to_user(p, &j, sizeof(j)))
			ret = -EFAULT;
		break;
	}
	case TECHO5_VPU_SYNC: {
		struct techo5_vpu_sync s;

		ret = -EFAULT;
		if (copy_from_user(&s, p, sizeof(s)))
			break;
		ret = tvpu_sync(v, &s);
		break;
	}
	default:
		ret = -ENOTTY;
	}
	mutex_unlock(&v->lock);
	return ret;
}

/* The data area: offset 0 is DATA_PA, write-combined; TECHO5_VPU_MMAP_CACHED the same, cached */
static int tvpu_mmap(struct file *f, struct vm_area_struct *vma)
{
	struct tvpu *v = tvpu_the;
	size_t len = vma->vm_end - vma->vm_start;

	if (len > v->data_size)
		return -EINVAL;
	if (!vma->vm_pgoff)
		vma->vm_page_prot = pgprot_writecombine(vma->vm_page_prot);
	else if (vma->vm_pgoff != TECHO5_VPU_MMAP_CACHED >> PAGE_SHIFT)
		return -EINVAL;	/* the cached view keeps the mapping's own, normal memory */
	return remap_pfn_range(vma, vma->vm_start, PHYS_PFN(DATA_PA), len, vma->vm_page_prot);
}

static int tvpu_open(struct inode *inode, struct file *f)
{
	struct tvpu *v = tvpu_the;

	if (atomic_cmpxchg(&v->opened, 0, 1))
		return -EBUSY;
	return 0;
}

/* The last close (the mapping holds the file too) lets the core and its clocks go */
static int tvpu_release(struct inode *inode, struct file *f)
{
	struct tvpu *v = tvpu_the;

	mutex_lock(&v->lock);
	tvpu_power(v, false);
	mutex_unlock(&v->lock);
	atomic_set(&v->opened, 0);
	return 0;
}

static const struct file_operations tvpu_fops = {
	.owner = THIS_MODULE,
	.open = tvpu_open,
	.unlocked_ioctl = tvpu_ioctl,
	.compat_ioctl = compat_ptr_ioctl,
	.mmap = tvpu_mmap,
	.release = tvpu_release,
};

#ifdef CONFIG_TECHO5_VPU_DEBUG
static ssize_t power_write(struct file *f, const char __user *buf, size_t n, loff_t *pos)
{
	struct tvpu *v = f->private_data;
	bool on;
	int ret = kstrtobool_from_user(buf, n, &on);

	if (ret)
		return ret;
	mutex_lock(&v->lock);
	ret = tvpu_power(v, on);
	mutex_unlock(&v->lock);
	return ret ?: n;
}

static ssize_t power_read(struct file *f, char __user *buf, size_t n, loff_t *pos)
{
	struct tvpu *v = f->private_data;

	return simple_read_from_buffer(buf, n, pos, v->on ? "1\n" : "0\n", 2);
}

static const struct file_operations power_fops = {
	.open = simple_open, .read = power_read, .write = power_write, .llseek = default_llseek,
};

static ssize_t regs_rw(struct file *f, char __user *ubuf, size_t n, loff_t *pos, bool wr)
{
	struct tvpu *v = f->private_data;
	size_t i;
	ssize_t ret = n;

	if (*pos & 3 || n & 3 || *pos >= VPU_REGS_SIZE)
		return -EINVAL;
	n = min_t(size_t, n, VPU_REGS_SIZE - *pos);
	mutex_lock(&v->lock);
	if (!v->on) {
		ret = -ENODEV;	/* a powered-down core's registers stall the bus */
		goto out;
	}
	for (i = 0; i < n; i += 4) {
		u32 w;

		if (wr) {
			if (get_user(w, (u32 __user *)(ubuf + i))) {
				ret = -EFAULT;
				goto out;
			}
			writel(w, v->regs + *pos + i);
		} else {
			w = readl(v->regs + *pos + i);
			if (put_user(w, (u32 __user *)(ubuf + i))) {
				ret = -EFAULT;
				goto out;
			}
		}
	}
	*pos += n;
	ret = n;
out:
	mutex_unlock(&v->lock);
	return ret;
}

static ssize_t regs_read(struct file *f, char __user *b, size_t n, loff_t *p) { return regs_rw(f, b, n, p, false); }
static ssize_t regs_write(struct file *f, const char __user *b, size_t n, loff_t *p) { return regs_rw(f, (char __user *)b, n, p, true); }

static const struct file_operations regs_fops = {
	.open = simple_open, .read = regs_read, .write = regs_write, .llseek = default_llseek,
};

/* The region: up to the data area by the write-combined mapping, the data area by the cached one */
static ssize_t mem_rw(struct file *f, char __user *ubuf, size_t n, loff_t *pos, bool wr)
{
	struct tvpu *v = f->private_data;
	size_t lo = DATA_PA - v->mem_pa;
	loff_t at = *pos;
	ssize_t r;

	if (at < 0)
		return -EINVAL;
	if (at >= v->mem_size)
		return 0;
	n = min_t(size_t, n, v->mem_size - at);
	if (at < lo) {
		n = min_t(size_t, n, lo - at);
		if (wr ? copy_from_user(v->mem + at, ubuf, n) : copy_to_user(ubuf, v->mem + at, n))
			return -EFAULT;
		wmb();
		*pos = at + n;
		return n;
	}
	at -= lo;
	if (!wr)
		dcache_inval_poc((unsigned long)v->data + at, (unsigned long)v->data + at + n);
	r = wr ? copy_from_user(v->data + at, ubuf, n) : copy_to_user(ubuf, v->data + at, n);
	if (r)
		return -EFAULT;
	if (wr)
		dcache_clean_poc((unsigned long)v->data + at, (unsigned long)v->data + at + n);
	*pos += n;
	return n;
}

static ssize_t mem_read(struct file *f, char __user *b, size_t n, loff_t *p) { return mem_rw(f, b, n, p, false); }
static ssize_t mem_write(struct file *f, const char __user *b, size_t n, loff_t *p) { return mem_rw(f, (char __user *)b, n, p, true); }

static const struct file_operations mem_fops = {
	.open = simple_open, .read = mem_read, .write = mem_write, .llseek = default_llseek,
};

static ssize_t log_read(struct file *f, char __user *b, size_t n, loff_t *p)
{
	struct tvpu *v = f->private_data;

	return simple_read_from_buffer(b, n, p, tvpu_at(v, LOG_PA), LOG_SIZE);
}

static const struct file_operations log_fops = {
	.open = simple_open, .read = log_read, .llseek = default_llseek,
};

static void tvpu_debugfs(struct tvpu *v)
{
	v->dir = debugfs_create_dir("techo5-vpu", NULL);
	debugfs_create_file("power", 0600, v->dir, v, &power_fops);
	debugfs_create_file("regs", 0600, v->dir, v, &regs_fops);
	debugfs_create_file_size("mem", 0600, v->dir, v, &mem_fops, v->mem_size);
	debugfs_create_file_size("log", 0400, v->dir, v, &log_fops, LOG_SIZE);
}
#else
static void tvpu_debugfs(struct tvpu *v) {}
#endif

static int tvpu_probe(struct platform_device *pdev)
{
	struct device *dev = &pdev->dev;
	struct device_node *np;
	struct resource r;
	struct tvpu *v;
	int ret;

	v = devm_kzalloc(dev, sizeof(*v), GFP_KERNEL);
	if (!v)
		return -ENOMEM;
	v->dev = dev;
	mutex_init(&v->lock);
	ret = devm_clk_bulk_get_all(dev, &v->clks);
	if (ret < 0)
		return dev_err_probe(dev, ret, "clocks\n");
	v->nclks = ret;
	v->regs = devm_ioremap(dev, VPU_REGS, VPU_REGS_SIZE);	/* ipu_core0's clocks claim 0x19180000 */
	if (!v->regs)
		return -ENOMEM;
	np = of_parse_phandle(dev->of_node, "memory-region", 0);
	if (!np || of_address_to_resource(np, 0, &r))
		return dev_err_probe(dev, -EINVAL, "no memory-region\n");
	of_node_put(np);
	v->mem_pa = r.start;
	v->mem_size = resource_size(&r);
	if (v->mem_pa != FW_PA || v->mem_pa + v->mem_size <= DATA_PA)
		return dev_err_probe(dev, -EINVAL, "the region is not the one expected\n");
	v->data_size = v->mem_pa + v->mem_size - DATA_PA;
	v->mem = devm_memremap(dev, v->mem_pa, DATA_PA - v->mem_pa, MEMREMAP_WC);
	if (IS_ERR(v->mem))
		return PTR_ERR(v->mem);
	v->data = devm_memremap(dev, DATA_PA, v->data_size, MEMREMAP_WB);
	if (IS_ERR(v->data))
		return PTR_ERR(v->data);
	ret = devm_pm_runtime_enable(dev);
	if (ret)
		return ret;
	platform_set_drvdata(pdev, v);
	tvpu_the = v;
	tvpu_debugfs(v);
	v->misc.minor = MISC_DYNAMIC_MINOR;
	v->misc.name = "techo5-vpu";
	v->misc.fops = &tvpu_fops;
	v->misc.mode = 0600;
	ret = misc_register(&v->misc);
	if (ret)
		return ret;
	dev_info(dev, "%d clocks; region %pa, %zu bytes; powered off until used\n", v->nclks,
		 &v->mem_pa, v->mem_size);
	return 0;
}

static void tvpu_remove(struct platform_device *pdev)
{
	struct tvpu *v = platform_get_drvdata(pdev);

	misc_deregister(&v->misc);
	debugfs_remove_recursive(v->dir);
	mutex_lock(&v->lock);
	tvpu_power(v, false);
	mutex_unlock(&v->lock);
}

static const struct of_device_id tvpu_ids[] = {
	{ .compatible = "techo5,mt8183-vpu" },
	{}
};
MODULE_DEVICE_TABLE(of, tvpu_ids);

static struct platform_driver tvpu_driver = {
	.probe = tvpu_probe,
	.remove = tvpu_remove,
	.driver = { .name = "techo5-vpu", .of_match_table = tvpu_ids },
};
module_platform_driver(tvpu_driver);
MODULE_DESCRIPTION("MT8183 VPU (Vision P6 core 0) for user space");
MODULE_LICENSE("GPL");
