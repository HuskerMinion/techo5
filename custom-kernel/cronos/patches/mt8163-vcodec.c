// SPDX-License-Identifier: GPL-2.0
/*
 * mt8163-vcodec.c: /dev/Vcodec for the MT8163 (2nd gen Echo Show 5, cronos), the kernel side of
 * MediaTek's AP-driven video codec libraries (libvcodecdrv and libvcodec_utility, 32-bit Android).
 *
 * On the MT8163 no co-processor runs the codecs: the library parses the stream and programs the
 * H.264 decoder (VDEC, 0x16020000) and encoder (VENC, 0x17002000) itself, through registers it maps
 * from this device. The kernel only powers the blocks, hands one client at a time the hardware
 * (LOCKHW/UNLOCKHW), waits for its interrupt (WAITISR) and gives it DMA buffers. The ioctl numbers
 * and the structures are those of Amazon's GPL driver (drivers/misc/mediatek/videocodec/mt8163,
 * android_kernel_amazon_mt8163), in their 32-bit layout: the libraries are 32-bit, and nothing
 * else talks to this device. The rest is mainline's: runtime PM on the VDEC and VENC power
 * domains with their SMI larbs (linked by the IOMMU), and buffers from the DMA API, whose
 * addresses are the IOMMU's, which is what the blocks are programmed with.
 *
 * The libraries open the device several times in one process (eValInit, eHalInit, the ION and
 * M4U stand-ins each do), so the hardware lock and the buffers belong to the process: they are let
 * go when its last open file is closed.
 *
 * The stand-ins for Android's ION and M4U (custom-kernel/cronos/vcodec-user) allocate through one
 * ioctl of our own, VCODEC_SHIM_ALLOC: zeroed memory, mapped for the blocks, as a dma-buf.
 *
 * The decoder and the encoder are two devices (each is behind its own larb, and an IOMMU client
 * is linked to one larb): "techo5,mt8163-vdec" and "techo5,mt8163-venc". The character device
 * appears once both have probed.
 *
 * A decoder that hangs (no interrupt within the library's timeout) stays hung: the library's own
 * reset (VLD 0x108) does not bring it back, and its power domain never goes off on this kernel
 * (genpd keeps the domains that were on at boot on until the scpsys provider's sync_state, which
 * never comes). So a timed-out wait resets it here, through the TOPRGU's VDEC reset, with larb1
 * suspended around it so that it comes back configured (its MMU enables are in its registers).
 */

#include <linux/clk.h>
#include <linux/compat.h>
#include <linux/delay.h>
#include <linux/dma-buf.h>
#include <linux/dma-mapping.h>
#include <linux/interrupt.h>
#include <linux/io.h>
#include <linux/miscdevice.h>
#include <linux/mm.h>
#include <linux/module.h>
#include <linux/mutex.h>
#include <linux/of.h>
#include <linux/of_platform.h>
#include <linux/platform_device.h>
#include <linux/pm_runtime.h>
#include <linux/reset.h>
#include <linux/slab.h>
#include <linux/uaccess.h>
#include <linux/wait.h>

/* Amazon's videocodec_kernel_driver.h; every one is _IOW('M', n, unsigned int) */
#define MFV_IOC_MAGIC			'M'
#define VCODEC_WAITISR			_IOW(MFV_IOC_MAGIC, 0x0b, unsigned int)
#define VCODEC_LOCKHW			_IOW(MFV_IOC_MAGIC, 0x0d, unsigned int)
#define VCODEC_INC_ENC_EMI_USER		_IOW(MFV_IOC_MAGIC, 0x15, unsigned int)
#define VCODEC_DEC_ENC_EMI_USER		_IOW(MFV_IOC_MAGIC, 0x16, unsigned int)
#define VCODEC_INC_DEC_EMI_USER		_IOW(MFV_IOC_MAGIC, 0x17, unsigned int)
#define VCODEC_DEC_DEC_EMI_USER		_IOW(MFV_IOC_MAGIC, 0x18, unsigned int)
#define VCODEC_INITHWLOCK		_IOW(MFV_IOC_MAGIC, 0x20, unsigned int)
#define VCODEC_DEINITHWLOCK		_IOW(MFV_IOC_MAGIC, 0x21, unsigned int)
#define VCODEC_ALLOC_NON_CACHE_BUFFER	_IOW(MFV_IOC_MAGIC, 0x22, unsigned int)
#define VCODEC_FREE_NON_CACHE_BUFFER	_IOW(MFV_IOC_MAGIC, 0x23, unsigned int)
#define VCODEC_SET_THREAD_ID		_IOW(MFV_IOC_MAGIC, 0x24, unsigned int)
#define VCODEC_INC_PWR_USER		_IOW(MFV_IOC_MAGIC, 0x27, unsigned int)
#define VCODEC_DEC_PWR_USER		_IOW(MFV_IOC_MAGIC, 0x28, unsigned int)
#define VCODEC_GET_CPU_LOADING_INFO	_IOW(MFV_IOC_MAGIC, 0x29, unsigned int)
#define VCODEC_GET_CORE_LOADING		_IOW(MFV_IOC_MAGIC, 0x30, unsigned int)
#define VCODEC_GET_CORE_NUMBER		_IOW(MFV_IOC_MAGIC, 0x31, unsigned int)
#define VCODEC_SET_CPU_OPP_LIMIT	_IOW(MFV_IOC_MAGIC, 0x32, unsigned int)
#define VCODEC_UNLOCKHW			_IOW(MFV_IOC_MAGIC, 0x33, unsigned int)
#define VCODEC_MB			_IOW(MFV_IOC_MAGIC, 0x34, unsigned int)

/* Ours, for the ION/M4U stand-ins (vcodec-user/include/vcodec_shim_uapi.h); same on arm and arm64 */
struct vcodec_shim_alloc {
	__u64 size;	/* in: bytes; out: the size allocated */
	__u64 iova;	/* out: the buffer's address for the blocks, below 4 GiB */
	__u32 align;	/* in: 0, or a power of two up to a page */
	__u32 flags;	/* in: VCODEC_SHIM_ALLOC_* */
	__s32 fd;	/* out: the dma-buf */
	__u32 reserved;
};
#define VCODEC_SHIM_ALLOC_CACHED	BIT(0)
#define VCODEC_SHIM_ALLOC_VENC		BIT(1)
#define VCODEC_SHIM_ALLOC		_IOWR('V', 0x01, struct vcodec_shim_alloc)

/* VAL_DRIVER_TYPE_T (val_types_public.h) */
enum {
	VAL_DRIVER_TYPE_NONE = 0,
	VAL_DRIVER_TYPE_MP4_ENC,
	VAL_DRIVER_TYPE_MP4_DEC,
	VAL_DRIVER_TYPE_H263_ENC,
	VAL_DRIVER_TYPE_H263_DEC,
	VAL_DRIVER_TYPE_H264_ENC,
	VAL_DRIVER_TYPE_H264_DEC,
	VAL_DRIVER_TYPE_SORENSON_SPARK_DEC,
	VAL_DRIVER_TYPE_VC1_SP_DEC,
	VAL_DRIVER_TYPE_RV9_DEC,
	VAL_DRIVER_TYPE_MP1_MP2_DEC,
	VAL_DRIVER_TYPE_XVID_DEC,
	VAL_DRIVER_TYPE_DIVX4_DIVX5_DEC,
	VAL_DRIVER_TYPE_VC1_MP_WMV9_DEC,
	VAL_DRIVER_TYPE_RV8_DEC,
	VAL_DRIVER_TYPE_WMV7_DEC,
	VAL_DRIVER_TYPE_WMV8_DEC,
	VAL_DRIVER_TYPE_AVS_DEC,
	VAL_DRIVER_TYPE_DIVX_3_11_DEC,
	VAL_DRIVER_TYPE_H264_DEC_MAIN,
	VAL_DRIVER_TYPE_H264_DEC_MAIN_CABAC,
	VAL_DRIVER_TYPE_VP8_DEC,
	VAL_DRIVER_TYPE_MP2_DEC,
	VAL_DRIVER_TYPE_VP9_DEC,
	VAL_DRIVER_TYPE_VP8_ENC,
	VAL_DRIVER_TYPE_VC1_ADV_DEC,
	VAL_DRIVER_TYPE_VC1_DEC,
	VAL_DRIVER_TYPE_JPEG_ENC,
	VAL_DRIVER_TYPE_HEVC_ENC,
	VAL_DRIVER_TYPE_HEVC_DEC,
	VAL_DRIVER_TYPE_H264_ENC_LIVEPHOTO,
};

/* VAL_RESULT_T values the ioctls hand back as they are */
#define VAL_RESULT_INVALID_PARAMETER	3

/* The structures, as a 32-bit (EABI) caller lays them out */
struct vc32_hw_lock {
	u32 handle, handle_size, lock, timeout_ms, reserved, reserved_size, driver_type;
	u8 secure, pad[3];
};

#define IRQ_STATUS_MAX_NUM	16
struct vc32_isr {
	u32 handle, handle_size, driver_type, isr_function, reserved, reserved_size;
	u32 timeout_ms, irq_status_num, irq_status[IRQ_STATUS_MAX_NUM];
};

struct vc32_memory {			/* VAL_MEMORY_T: its unions are 8 bytes, 8-aligned */
	u32 sign, mem_type;
	u64 size;
	u64 va;
	u64 pa;				/* the buffer's address for the blocks (the IOMMU's) */
	u32 alignment, pad0;
	u64 align_va, align_pa;
	u32 mem_codec, ion_share_fd;
	u64 ion_handle, reserved;
	u64 reserved_size;
	u32 ion_dev_fd, pad1;
};

struct vc32_core_loading {
	s32 cpu_id, loading;
};

static_assert(sizeof(struct vc32_hw_lock) == 32);
static_assert(sizeof(struct vc32_isr) == 96);
static_assert(sizeof(struct vc32_memory) == 96);

/* Registers (Amazon's driver) */
#define VDEC_GCON_ACTIVE	0x0000		/* VDEC_SYS: bit 4, the decoder still running */
#define VDEC_DONE		0x00a4		/* MISC: bit 16 decode done; bits 0, 4 clear the IRQ */
#define VENC_IRQ_STATUS		0x005c
#define VENC_IRQ_ACK		0x0060
#define VENC_SW_HRST_N		0x00a8
#define VENC_SW_PAUSE		0x00ac
#define VENC_IRQ_ALL		0xbf		/* SPS, PPS, FRM, DRAM, PAUSE, SWITCH, VPS */

/*
 * reset (/sys/module/mt8163_vcodec/parameters/reset) set to 0 leaves a decoder that timed out as it
 * is: for tests. resets counts the resets done.
 */
static bool reset_on_timeout = true;
module_param_named(reset, reset_on_timeout, bool, 0644);
static unsigned int resets;
module_param(resets, uint, 0444);

/* What a client may map: the blocks' registers, as the library asks for them */
static const struct { phys_addr_t base; size_t size; } vc_windows[] = {
	{ 0x16000000, 0x29000 },	/* VDEC_SYS (gcon), larb1, the decoder */
	{ 0x17002000, 0x1000 },		/* the H.264 encoder */
};

enum { VC_DEC, VC_ENC, VC_N };

/* A process using the device: its open files, and what it holds */
struct vc_proc {
	struct list_head node;
	pid_t tgid;
	int opens;
};

struct vc_buf {
	struct list_head node;
	struct vc_proc *owner;
	void *cpu;
	dma_addr_t iova;
	size_t size;
};

struct vc_block {
	struct device *dev;
	void __iomem *regs;		/* VDEC: VDEC_SYS (0x16000000); VENC: the encoder */
	void __iomem *misc;		/* VDEC: the decoder's MISC (0x16020000) */
	struct clk_bulk_data *clks;
	int nclks;
	int irq;
	struct reset_control *rst;	/* VDEC: the TOPRGU's reset of the whole decoder */
	struct device *larb;		/* VDEC: larb1, suspended around that reset */
	/* the hardware lock: who holds it (the process, and the library's instance handle) */
	struct vc_proc *owner;
	u32 handle;
	int driver_type;
	bool powered;
	wait_queue_head_t lock_wq;
	/* the interrupt */
	wait_queue_head_t isr_wq;
	bool isr_done;
	u32 irq_status;			/* VENC: the status the interrupt cleared */
};

static struct {
	struct mutex lock;
	struct vc_block blk[VC_N];
	struct list_head bufs;
	struct list_head procs;
	struct miscdevice misc;
	bool registered;
	u32 emi_users[VC_N];
} vc = {
	.lock = __MUTEX_INITIALIZER(vc.lock),
	.bufs = LIST_HEAD_INIT(vc.bufs),
	.procs = LIST_HEAD_INIT(vc.procs),
};

#define OWNER(f)	((struct vc_proc *)(f)->private_data)

static int vc_block_of(u32 driver_type)
{
	switch (driver_type) {
	case VAL_DRIVER_TYPE_MP4_DEC:
	case VAL_DRIVER_TYPE_HEVC_DEC:
	case VAL_DRIVER_TYPE_H264_DEC:
	case VAL_DRIVER_TYPE_MP1_MP2_DEC:
	case VAL_DRIVER_TYPE_VC1_DEC:
	case VAL_DRIVER_TYPE_VC1_ADV_DEC:
	case VAL_DRIVER_TYPE_VP9_DEC:
	case VAL_DRIVER_TYPE_VP8_DEC:
		return VC_DEC;
	case VAL_DRIVER_TYPE_H264_ENC:
	case VAL_DRIVER_TYPE_HEVC_ENC:
	case VAL_DRIVER_TYPE_VP8_ENC:
	case VAL_DRIVER_TYPE_JPEG_ENC:
		return VC_ENC;
	}
	return -EINVAL;
}

static irqreturn_t vc_dec_irq(int irq, void *data)
{
	struct vc_block *b = data;
	u32 v;

	if (readl(b->regs + VDEC_GCON_ACTIVE) & BIT(4))
		return IRQ_NONE;	/* not done yet */
	if (!(readl(b->misc + VDEC_DONE) & BIT(16)))
		return IRQ_NONE;
	v = readl(b->misc + VDEC_DONE);
	writel(v | 0x11, b->misc + VDEC_DONE);
	writel((v | 0x11) & ~0x10, b->misc + VDEC_DONE);
	b->isr_done = true;
	wake_up_interruptible(&b->isr_wq);
	return IRQ_HANDLED;
}

static irqreturn_t vc_enc_irq(int irq, void *data)
{
	struct vc_block *b = data;
	u32 st = readl(b->regs + VENC_IRQ_STATUS);
	int i;

	for (i = 0; i < 8; i++)
		if (st & VENC_IRQ_ALL & BIT(i))
			writel(BIT(i), b->regs + VENC_IRQ_ACK);
	b->irq_status = st;
	b->isr_done = true;
	wake_up_interruptible(&b->isr_wq);
	return IRQ_HANDLED;
}

static int vc_power(struct vc_block *b, bool on)
{
	int ret;

	if (on == b->powered)
		return 0;
	if (on) {
		ret = pm_runtime_resume_and_get(b->dev);
		if (ret)
			return ret;
		ret = clk_bulk_prepare_enable(b->nclks, b->clks);
		if (ret) {
			pm_runtime_put(b->dev);
			return ret;
		}
		b->isr_done = false;
		enable_irq(b->irq);
	} else {
		disable_irq(b->irq);
		clk_bulk_disable_unprepare(b->nclks, b->clks);
		pm_runtime_put(b->dev);
	}
	b->powered = on;
	return 0;
}

/* The encoder back to a known state when a holder goes away mid-frame (Amazon's venc_hw_reset) */
static void vc_enc_reset(struct vc_block *b)
{
	writel(1, b->regs + VENC_SW_PAUSE);
	udelay(200);
	writel(0, b->regs + VENC_SW_PAUSE);
	writel(0, b->regs + VENC_SW_HRST_N);
	readl(b->regs + VENC_SW_HRST_N);
	writel(1, b->regs + VENC_SW_HRST_N);
	readl(b->regs + VENC_SW_HRST_N);
}

/*
 * The decoder back from a hang, with vc.lock held and the block powered (the caller holds it):
 * powered down to its larb, reset, and powered up again. larb1 sits in the decoder's subsystem;
 * suspended while the reset is applied, it is resumed after it, which writes its configuration
 * (the ports' IOMMU enables) again. The decoder's registers are at their reset values after this;
 * the library programs them for each picture.
 */
static int vc_dec_reset(struct vc_block *b)
{
	unsigned long end;
	int ret;

	if (!b->rst || !b->larb)
		return -ENODEV;
	vc_power(b, false);
	/* the larb is our supplier: it is let go from the PM workqueue, after our suspend */
	end = jiffies + msecs_to_jiffies(200);
	while (!pm_runtime_suspended(b->larb) && time_before(jiffies, end))
		usleep_range(500, 1000);
	if (!pm_runtime_suspended(b->larb)) {
		dev_err(b->dev, "larb1 stays up; the decoder is not reset\n");
		ret = -EBUSY;
	} else {
		ret = reset_control_assert(b->rst);
		udelay(10);
		ret = reset_control_deassert(b->rst) ?: ret;
	}
	resets++;
	return vc_power(b, true) ?: ret;
}

/* with vc.lock held */
static void vc_unlock_block(struct vc_block *b)
{
	if (b == &vc.blk[VC_ENC] && b->powered)
		vc_enc_reset(b);
	vc_power(b, false);
	b->owner = NULL;
	b->handle = 0;
	b->driver_type = VAL_DRIVER_TYPE_NONE;
	wake_up_interruptible(&b->lock_wq);
}

static long vc_lockhw(struct file *f, struct vc32_hw_lock *l)
{
	int i = vc_block_of(l->driver_type);
	struct vc_block *b;
	long ret;

	if (i < 0)
		return -EFAULT;
	b = &vc.blk[i];
	for (;;) {
		mutex_lock(&vc.lock);
		if (b->owner == OWNER(f) && b->handle == l->handle) {
			mutex_unlock(&vc.lock);
			return 0;		/* held already */
		}
		if (!b->owner) {
			ret = vc_power(b, true);
			if (!ret) {
				b->owner = OWNER(f);
				b->handle = l->handle;
				b->driver_type = l->driver_type;
			}
			mutex_unlock(&vc.lock);
			return ret;
		}
		mutex_unlock(&vc.lock);
		/* Amazon's early way out, for the JPEG encoder: a timeout of 0 does not wait */
		if (i == VC_ENC && l->timeout_ms == 0)
			return 0;
		ret = wait_event_interruptible_timeout(b->lock_wq, !READ_ONCE(b->owner),
						       msecs_to_jiffies(1000));
		if (ret < 0)
			return ret;
	}
}

static long vc_unlockhw(struct file *f, struct vc32_hw_lock *l)
{
	int i = vc_block_of(l->driver_type);
	struct vc_block *b;
	long ret = 0;

	if (i < 0)
		return -EFAULT;
	b = &vc.blk[i];
	mutex_lock(&vc.lock);
	if (b->owner == OWNER(f) && b->handle == l->handle)
		vc_unlock_block(b);
	else
		ret = -EFAULT;			/* not the holder */
	mutex_unlock(&vc.lock);
	return ret;
}

static long vc_waitisr(struct file *f, struct vc32_isr *isr, bool *copy_back)
{
	int i = vc_block_of(isr->driver_type);
	struct vc_block *b;
	long ret;

	if (i < 0)
		return -EFAULT;
	b = &vc.blk[i];
	mutex_lock(&vc.lock);
	ret = b->owner == OWNER(f) && b->handle == isr->handle ? 0 : -EFAULT;
	mutex_unlock(&vc.lock);
	if (ret)
		return ret;			/* the hardware is not the caller's */
	ret = wait_event_interruptible_timeout(b->isr_wq, READ_ONCE(b->isr_done),
					       msecs_to_jiffies(isr->timeout_ms));
	b->isr_done = false;
	if (ret == 0 && i == VC_DEC && READ_ONCE(reset_on_timeout)) {
		u32 active, done;
		ktime_t t;

		mutex_lock(&vc.lock);
		if (b->owner == OWNER(f) && b->powered) {
			active = readl(b->regs + VDEC_GCON_ACTIVE);
			done = readl(b->misc + VDEC_DONE);
			t = ktime_get();
			ret = vc_dec_reset(b);
			dev_warn(b->dev, "no interrupt in %u ms (VDEC_SYS %08x, MISC 0xa4 %08x): reset%s, %lld us\n",
				 isr->timeout_ms, active, done, ret ? " failed" : "",
				 ktime_us_delta(ktime_get(), t));
			ret = 0;
		}
		mutex_unlock(&vc.lock);
	}
	if (ret == 0)
		return -2;			/* timed out: Amazon's driver returns -2 too */
	if (ret < 0)
		return ret;
	if (i == VC_ENC && isr->irq_status_num > 0) {
		isr->irq_status[0] = b->irq_status;
		*copy_back = true;
	}
	return 0;
}

static long vc_alloc(struct file *f, struct vc32_memory *m)
{
	struct device *dev = vc.blk[VC_DEC].dev;
	struct vc_buf *buf;
	size_t size = PAGE_ALIGN((size_t)m->size);

	if (!size || size > SZ_64M)
		return -EINVAL;
	buf = kzalloc(sizeof(*buf), GFP_KERNEL);
	if (!buf)
		return -ENOMEM;
	buf->cpu = dma_alloc_coherent(dev, size, &buf->iova, GFP_KERNEL);
	if (!buf->cpu) {
		kfree(buf);
		return -ENOMEM;
	}
	buf->size = size;
	buf->owner = OWNER(f);
	mutex_lock(&vc.lock);
	list_add(&buf->node, &vc.bufs);
	mutex_unlock(&vc.lock);
	m->pa = buf->iova;
	m->reserved_size = buf->iova;		/* Amazon's driver kept its kernel address here */
	return 0;
}

static void vc_buf_free(struct vc_buf *buf)
{
	dma_free_coherent(vc.blk[VC_DEC].dev, buf->size, buf->cpu, buf->iova);
	list_del(&buf->node);
	kfree(buf);
}

static long vc_free(struct file *f, struct vc32_memory *m)
{
	struct vc_buf *buf;
	long ret = -EINVAL;

	mutex_lock(&vc.lock);
	list_for_each_entry(buf, &vc.bufs, node)
		if (buf->owner == OWNER(f) && buf->iova == m->pa) {
			vc_buf_free(buf);
			ret = 0;
			break;
		}
	mutex_unlock(&vc.lock);
	m->pa = 0;
	m->reserved_size = 0;
	return ret;
}

/*
 * The stand-ins' buffers, exported as dma-bufs. Write-combined by default, which needs no cache
 * maintenance; cached on request (VCODEC_SHIM_ALLOC_CACHED, the frames the cores read back), as pages
 * the IOMMU maps into one range, synced for the cores and back around their access
 * (DMA_BUF_IOCTL_SYNC). A buffer lives until the last fd and mapping of it are gone; the stand-ins
 * make sure the hardware is done with it by then.
 */
struct vc_dmabuf {
	void *cpu;			/* write-combined */
	struct sg_table *sgt;		/* cached */
	dma_addr_t iova;
	size_t size;
};

static struct device *vc_dma_dev(void)
{
	return vc.blk[VC_DEC].dev;
}

static struct sg_table *vc_dmabuf_map(struct dma_buf_attachment *a, enum dma_data_direction dir)
{
	struct vc_dmabuf *d = a->dmabuf->priv;
	struct sg_table *sgt = kzalloc(sizeof(*sgt), GFP_KERNEL);
	struct scatterlist *src, *dst;
	int ret, i;

	if (!sgt)
		return ERR_PTR(-ENOMEM);
	if (d->sgt) {
		ret = sg_alloc_table(sgt, d->sgt->orig_nents, GFP_KERNEL);
		if (!ret) {
			dst = sgt->sgl;
			for_each_sgtable_sg(d->sgt, src, i) {
				sg_set_page(dst, sg_page(src), src->length, src->offset);
				dst = sg_next(dst);
			}
		}
	} else {
		ret = dma_get_sgtable_attrs(vc_dma_dev(), sgt, d->cpu, d->iova, d->size,
					    DMA_ATTR_WRITE_COMBINE);
	}
	if (!ret)
		ret = dma_map_sgtable(a->dev, sgt, dir, 0);
	if (ret) {
		sg_free_table(sgt);
		kfree(sgt);
		return ERR_PTR(ret);
	}
	return sgt;
}

static void vc_dmabuf_unmap(struct dma_buf_attachment *a, struct sg_table *sgt,
			    enum dma_data_direction dir)
{
	dma_unmap_sgtable(a->dev, sgt, dir, 0);
	sg_free_table(sgt);
	kfree(sgt);
}

static int vc_dmabuf_mmap(struct dma_buf *buf, struct vm_area_struct *vma)
{
	struct vc_dmabuf *d = buf->priv;

	if (d->sgt)
		return dma_mmap_noncontiguous(vc_dma_dev(), vma, d->size, d->sgt);
	return dma_mmap_attrs(vc_dma_dev(), vma, d->cpu, d->iova, d->size, DMA_ATTR_WRITE_COMBINE);
}

static int vc_dmabuf_begin_cpu(struct dma_buf *buf, enum dma_data_direction dir)
{
	struct vc_dmabuf *d = buf->priv;

	if (d->sgt)
		dma_sync_sgtable_for_cpu(vc_dma_dev(), d->sgt, DMA_BIDIRECTIONAL);
	return 0;
}

static int vc_dmabuf_end_cpu(struct dma_buf *buf, enum dma_data_direction dir)
{
	struct vc_dmabuf *d = buf->priv;

	if (d->sgt)
		dma_sync_sgtable_for_device(vc_dma_dev(), d->sgt, DMA_BIDIRECTIONAL);
	return 0;
}

static void vc_dmabuf_free(struct vc_dmabuf *d)
{
	if (d->sgt)
		dma_free_noncontiguous(vc_dma_dev(), d->size, d->sgt, DMA_BIDIRECTIONAL);
	else
		dma_free_attrs(vc_dma_dev(), d->size, d->cpu, d->iova, DMA_ATTR_WRITE_COMBINE);
	kfree(d);
}

static void vc_dmabuf_release(struct dma_buf *buf)
{
	vc_dmabuf_free(buf->priv);
}

static const struct dma_buf_ops vc_dmabuf_ops = {
	.map_dma_buf = vc_dmabuf_map,
	.unmap_dma_buf = vc_dmabuf_unmap,
	.mmap = vc_dmabuf_mmap,
	.begin_cpu_access = vc_dmabuf_begin_cpu,
	.end_cpu_access = vc_dmabuf_end_cpu,
	.release = vc_dmabuf_release,
};

/* Cached: the pages, zeroed, and the zeroes written back before the blocks see them */
static int vc_alloc_cached(struct vc_dmabuf *d)
{
	struct device *dev = vc_dma_dev();
	void *va;

	d->sgt = dma_alloc_noncontiguous(dev, d->size, DMA_BIDIRECTIONAL, GFP_KERNEL, 0);
	if (!d->sgt)
		return -ENOMEM;
	if (d->sgt->nents != 1) {	/* the blocks want one range: the IOMMU always gives one */
		dma_free_noncontiguous(dev, d->size, d->sgt, DMA_BIDIRECTIONAL);
		d->sgt = NULL;
		return -ENOMEM;
	}
	va = dma_vmap_noncontiguous(dev, d->size, d->sgt);
	if (!va) {
		dma_free_noncontiguous(dev, d->size, d->sgt, DMA_BIDIRECTIONAL);
		d->sgt = NULL;
		return -ENOMEM;
	}
	memset(va, 0, d->size);
	dma_vunmap_noncontiguous(dev, va);
	dma_sync_sgtable_for_device(dev, d->sgt, DMA_BIDIRECTIONAL);
	d->iova = sg_dma_address(d->sgt->sgl);
	return 0;
}

static long vc_shim_alloc(struct vcodec_shim_alloc *a)
{
	DEFINE_DMA_BUF_EXPORT_INFO(info);
	struct vc_dmabuf *d;
	struct dma_buf *buf;
	int fd, ret;

	if (!a->size || a->size > SZ_64M || a->reserved ||
	    (a->align && (!is_power_of_2(a->align) || a->align > PAGE_SIZE)) ||
	    (a->flags & ~(VCODEC_SHIM_ALLOC_CACHED | VCODEC_SHIM_ALLOC_VENC)))
		return -EINVAL;
	d = kzalloc(sizeof(*d), GFP_KERNEL);
	if (!d)
		return -ENOMEM;
	d->size = PAGE_ALIGN(a->size);
	if (a->flags & VCODEC_SHIM_ALLOC_CACHED) {
		ret = vc_alloc_cached(d);
		if (ret) {
			kfree(d);
			return ret;
		}
	} else {
		d->cpu = dma_alloc_attrs(vc_dma_dev(), d->size, &d->iova, GFP_KERNEL | __GFP_ZERO,
					 DMA_ATTR_WRITE_COMBINE);
		if (!d->cpu) {
			kfree(d);
			return -ENOMEM;
		}
	}
	info.ops = &vc_dmabuf_ops;
	info.size = d->size;
	info.flags = O_RDWR;
	info.priv = d;
	buf = dma_buf_export(&info);
	if (IS_ERR(buf)) {
		vc_dmabuf_free(d);
		return PTR_ERR(buf);
	}
	fd = dma_buf_fd(buf, O_CLOEXEC);
	if (fd < 0) {
		dma_buf_put(buf);
		return fd;
	}
	a->size = d->size;
	a->iova = d->iova;
	a->fd = fd;
	return 0;
}

static long vc_ioctl32(struct file *f, unsigned int cmd, unsigned long arg)
{
	void __user *p = compat_ptr(arg);
	long ret = 0;
	u32 v;

	switch (cmd) {
	case VCODEC_LOCKHW:
	case VCODEC_UNLOCKHW: {
		struct vc32_hw_lock l;

		if (copy_from_user(&l, p, sizeof(l)))
			return -EFAULT;
		ret = cmd == VCODEC_LOCKHW ? vc_lockhw(f, &l) : vc_unlockhw(f, &l);
		break;
	}
	case VCODEC_WAITISR: {
		struct vc32_isr isr;
		bool copy_back = false;

		if (copy_from_user(&isr, p, sizeof(isr)))
			return -EFAULT;
		ret = vc_waitisr(f, &isr, &copy_back);
		if (!ret && copy_back && copy_to_user(p, &isr, sizeof(isr)))
			return -EFAULT;
		break;
	}
	case VCODEC_ALLOC_NON_CACHE_BUFFER:
	case VCODEC_FREE_NON_CACHE_BUFFER: {
		struct vc32_memory m;

		if (copy_from_user(&m, p, sizeof(m)))
			return -EFAULT;
		ret = cmd == VCODEC_ALLOC_NON_CACHE_BUFFER ? vc_alloc(f, &m) : vc_free(f, &m);
		if (copy_to_user(p, &m, sizeof(m)))
			return -EFAULT;
		break;
	}
	case VCODEC_INC_DEC_EMI_USER:
	case VCODEC_DEC_DEC_EMI_USER:
	case VCODEC_INC_ENC_EMI_USER:
	case VCODEC_DEC_ENC_EMI_USER: {
		u32 *n = &vc.emi_users[cmd == VCODEC_INC_DEC_EMI_USER ||
				       cmd == VCODEC_DEC_DEC_EMI_USER ? VC_DEC : VC_ENC];

		mutex_lock(&vc.lock);
		if (cmd == VCODEC_INC_DEC_EMI_USER || cmd == VCODEC_INC_ENC_EMI_USER)
			(*n)++;
		else if (*n)
			(*n)--;
		v = *n;
		mutex_unlock(&vc.lock);
		if (copy_to_user(p, &v, sizeof(v)))
			return -EFAULT;
		break;
	}
	case VCODEC_GET_CORE_NUMBER:
		v = nr_cpu_ids;
		if (copy_to_user(p, &v, sizeof(v)))
			return -EFAULT;
		break;
	case VCODEC_GET_CPU_LOADING_INFO: {
		u8 zero[32] = {};	/* VAL_VCODEC_CPU_LOADING_INFO_T, left at zero as Amazon's is */

		if (copy_to_user(p, zero, sizeof(zero)))
			return -EFAULT;
		break;
	}
	case VCODEC_GET_CORE_LOADING: {
		struct vc32_core_loading cl;

		if (copy_from_user(&cl, p, sizeof(cl)))
			return -EFAULT;
		if (cl.cpu_id < 0 || cl.cpu_id > num_possible_cpus())
			return -EFAULT;
		if (copy_to_user(p, &cl, sizeof(cl)))
			return -EFAULT;
		break;
	}
	case VCODEC_MB:
		mb();
		break;
	case VCODEC_SET_THREAD_ID:
	case VCODEC_INITHWLOCK:
	case VCODEC_DEINITHWLOCK:
	case VCODEC_INC_PWR_USER:	/* power follows the hardware lock */
	case VCODEC_DEC_PWR_USER:
	case VCODEC_SET_CPU_OPP_LIMIT:
		break;
	default:
		return -ENOTTY;
	}
	/* Amazon's driver answers 0xff for a call that worked; the library expects nothing else */
	return ret ?: 0xff;
}

/*
 * Amazon's ioctls only from 32-bit callers: the libraries are 32-bit, and the structures above are
 * their layout. Ours has one layout for both.
 */
static long vc_ioctl(struct file *f, unsigned int cmd, unsigned long arg)
{
	if (cmd == VCODEC_SHIM_ALLOC) {
		void __user *p = in_compat_syscall() ? compat_ptr(arg) : (void __user *)arg;
		struct vcodec_shim_alloc a;
		long ret;

		if (copy_from_user(&a, p, sizeof(a)))
			return -EFAULT;
		ret = vc_shim_alloc(&a);
		if (!ret && copy_to_user(p, &a, sizeof(a)))
			return -EFAULT;	/* the fd stays with the caller, as close() would leave it */
		return ret;
	}
	if (!in_compat_syscall())
		return -ENOTTY;
	return vc_ioctl32(f, cmd, arg);
}

static int vc_mmap(struct file *f, struct vm_area_struct *vma)
{
	phys_addr_t addr = (phys_addr_t)vma->vm_pgoff << PAGE_SHIFT;
	size_t len = vma->vm_end - vma->vm_start;
	struct vc_buf *buf;
	int i, ret = -EINVAL;

	/* a buffer of this client's, by its address for the blocks */
	mutex_lock(&vc.lock);
	list_for_each_entry(buf, &vc.bufs, node)
		if (buf->owner == OWNER(f) && buf->iova == addr && len <= buf->size) {
			vma->vm_pgoff = 0;
			ret = dma_mmap_coherent(vc.blk[VC_DEC].dev, vma, buf->cpu, buf->iova, len);
			mutex_unlock(&vc.lock);
			return ret;
		}
	mutex_unlock(&vc.lock);
	/* or a window of registers */
	for (i = 0; i < ARRAY_SIZE(vc_windows); i++)
		if (addr >= vc_windows[i].base &&
		    addr + len <= vc_windows[i].base + PAGE_ALIGN(vc_windows[i].size)) {
			vma->vm_page_prot = pgprot_noncached(vma->vm_page_prot);
			vm_flags_set(vma, VM_IO | VM_DONTEXPAND | VM_DONTDUMP);
			return io_remap_pfn_range(vma, vma->vm_start, vma->vm_pgoff, len,
						  vma->vm_page_prot);
		}
	dev_warn_ratelimited(vc.misc.this_device, "mmap of %pa, %zu bytes, refused\n", &addr, len);
	return ret;
}

static int vc_open(struct inode *inode, struct file *f)
{
	struct vc_proc *p;

	mutex_lock(&vc.lock);
	list_for_each_entry(p, &vc.procs, node)
		if (p->tgid == current->tgid)
			goto found;
	p = kzalloc(sizeof(*p), GFP_KERNEL);
	if (!p) {
		mutex_unlock(&vc.lock);
		return -ENOMEM;
	}
	p->tgid = current->tgid;
	list_add(&p->node, &vc.procs);
found:
	p->opens++;
	f->private_data = p;
	mutex_unlock(&vc.lock);
	return 0;
}

static int vc_release(struct inode *inode, struct file *f)
{
	struct vc_proc *p = OWNER(f);
	struct vc_buf *buf, *n;
	int i;

	mutex_lock(&vc.lock);
	if (--p->opens == 0) {		/* the process's last: let go of what it held */
		for (i = 0; i < VC_N; i++)
			if (vc.blk[i].owner == p)
				vc_unlock_block(&vc.blk[i]);
		list_for_each_entry_safe(buf, n, &vc.bufs, node)
			if (buf->owner == p)
				vc_buf_free(buf);
		list_del(&p->node);
		kfree(p);
	}
	mutex_unlock(&vc.lock);
	return 0;
}

static const struct file_operations vc_fops = {
	.owner = THIS_MODULE,
	.open = vc_open,
	.unlocked_ioctl = vc_ioctl,
	.compat_ioctl = vc_ioctl,
	.mmap = vc_mmap,
	.release = vc_release,
};

static void vc_put_device(void *dev)
{
	put_device(dev);
}

static int vc_probe(struct platform_device *pdev)
{
	struct device *dev = &pdev->dev;
	int i = (uintptr_t)of_device_get_match_data(dev);
	struct vc_block *b = &vc.blk[i];
	struct resource *r;
	int ret;

	/* mapped, not claimed: VDEC_SYS is the vdecsys clock controller's too */
	r = platform_get_resource(pdev, IORESOURCE_MEM, 0);
	b->regs = r ? devm_ioremap(dev, r->start, resource_size(r)) : NULL;
	if (!b->regs)
		return -ENOMEM;
	if (i == VC_DEC) {
		r = platform_get_resource(pdev, IORESOURCE_MEM, 1);
		b->misc = r ? devm_ioremap(dev, r->start, resource_size(r)) : NULL;
		if (!b->misc)
			return -ENOMEM;
	}
	if (i == VC_DEC) {
		struct device_node *np;
		struct platform_device *larb;

		b->rst = devm_reset_control_get_optional_exclusive(dev, NULL);
		if (IS_ERR(b->rst))
			return dev_err_probe(dev, PTR_ERR(b->rst), "reset\n");
		np = of_parse_phandle(dev->of_node, "mediatek,larb", 0);
		larb = np ? of_find_device_by_node(np) : NULL;
		of_node_put(np);
		if (larb) {
			b->larb = &larb->dev;
			ret = devm_add_action_or_reset(dev, vc_put_device, b->larb);
			if (ret)
				return ret;
		}
		if (!b->rst || !b->larb)
			dev_warn(dev, "no reset or larb: a hung decoder stays hung\n");
	}
	ret = devm_clk_bulk_get_all(dev, &b->clks);
	if (ret < 0)
		return dev_err_probe(dev, ret, "clocks\n");
	b->nclks = ret;
	b->irq = platform_get_irq(pdev, 0);
	if (b->irq < 0)
		return b->irq;
	init_waitqueue_head(&b->isr_wq);
	init_waitqueue_head(&b->lock_wq);
	ret = devm_request_irq(dev, b->irq, i == VC_DEC ? vc_dec_irq : vc_enc_irq,
			       IRQF_NO_AUTOEN, dev_name(dev), b);
	if (ret)
		return ret;
	ret = dma_set_mask_and_coherent(dev, DMA_BIT_MASK(32));
	if (ret)
		return ret;
	ret = devm_pm_runtime_enable(dev);
	if (ret)
		return ret;
	mutex_lock(&vc.lock);
	b->dev = dev;
	if (vc.blk[VC_DEC].dev && vc.blk[VC_ENC].dev && !vc.registered) {
		vc.misc.minor = MISC_DYNAMIC_MINOR;
		vc.misc.name = "Vcodec";
		vc.misc.fops = &vc_fops;
		vc.misc.mode = 0660;
		ret = misc_register(&vc.misc);
		vc.registered = !ret;
	}
	mutex_unlock(&vc.lock);
	dev_info(dev, "%s, %d clocks%s\n", i == VC_DEC ? "H.264 decoder" : "H.264 encoder",
		 b->nclks, vc.registered ? "; /dev/Vcodec" : "");
	return ret;
}

static void vc_remove(struct platform_device *pdev)
{
	mutex_lock(&vc.lock);
	if (vc.registered) {
		misc_deregister(&vc.misc);
		vc.registered = false;
	}
	mutex_unlock(&vc.lock);
}

static const struct of_device_id vc_ids[] = {
	{ .compatible = "techo5,mt8163-vdec", .data = (void *)VC_DEC },
	{ .compatible = "techo5,mt8163-venc", .data = (void *)VC_ENC },
	{}
};
MODULE_DEVICE_TABLE(of, vc_ids);

static struct platform_driver vc_driver = {
	.probe = vc_probe,
	.remove = vc_remove,
	.driver = { .name = "mt8163-vcodec", .of_match_table = vc_ids },
};
module_platform_driver(vc_driver);
MODULE_DESCRIPTION("MT8163 video codec: /dev/Vcodec for MediaTek's codec libraries");
MODULE_LICENSE("GPL");
MODULE_IMPORT_NS("DMA_BUF");
