// SPDX-License-Identifier: GPL-2.0
/*
 * Copyright (c) 2020 BayLibre
 * Copyright (c) 2022 MediaTek Inc.
 */

#include <linux/bits.h>
#include <linux/device.h>
#include <linux/interrupt.h>
#include <linux/iommu.h>
#include <linux/iopoll.h>
#include <linux/ktime.h>
#include <linux/module.h>
#include <linux/platform_device.h>
#include <linux/property.h>
#include <linux/debugfs.h>
#include <linux/dma-mapping.h>
#include <linux/seq_file.h>

#include "mtk_camsv.h"
#include "mtk_camsv_hw.h"
#include "mtk_camsv_reg.h"


#define INT_ST_MASK_CAMSV (CAMSV_IRQ_PASS1_DON)
#define CAMSV_IRQ_SOF	BIT(7)	/* MT6771/MT8183 SV_SOF_INT_ST */

/* Bring-up knobs (MT8183 camsv register semantics are partly inferred). */
/* athena: packed 10-bit by default, the layout TECHO5's camera code reads */
static unsigned int out_mode = 1;
module_param(out_mode, uint, 0444);
MODULE_PARM_DESC(out_mode, "10-bit sensor output: 0=RAW8 (PAK RAW_8), 1=PAK RAW_10 (MTK packed), 2=16-bit (PAK off)");
static int fmt_sel_override = -1;
module_param(fmt_sel_override, int, 0644);
MODULE_PARM_DESC(fmt_sel_override, "write this to FMT_SEL instead of the table value (-1 = table)");
static int stride_bits_override = -1;
module_param(stride_bits_override, int, 0644);
MODULE_PARM_DESC(stride_bits_override, "OR these bits into IMGO STRIDE instead of the table bus-size bits (-1 = table)");
static int module_en_override = -1;
module_param(module_en_override, int, 0644);
MODULE_PARM_DESC(module_en_override, "MODULE_EN value before IMGO_EN is added (-1 = conf | table PAK bits)");
static int pak_override = -1;
module_param(pak_override, int, 0644);
MODULE_PARM_DESC(pak_override, "PAK register value (-1 = table)");
static int tg_sen_mode_override = -1;
module_param(tg_sen_mode_override, int, 0644);
MODULE_PARM_DESC(tg_sen_mode_override, "TG_SEN_MODE base value (-1 = SoC default; MT8183 1-pixel = 0)");

#define INT_ST_MASK_CAMSV_ERR \
	(CAMSV_IRQ_TG_ERR | CAMSV_IRQ_TG_GBERR | CAMSV_IRQ_IMGO_ERR)

#define MTK_CAMSV_AUTOSUSPEND_DELAY_MS 100

static const struct mtk_cam_conf camsv_conf = {
	.imgo_xsize = CAMSV_IMGO_SV_XSIZE,
	.imgo_ysize = CAMSV_IMGO_SV_YSIZE,
	.imgo_stride = CAMSV_IMGO_SV_STRIDE,
	.imgo_con_reg = CAMSV_IMGO_SV_CON,
	.imgo_con2_reg = CAMSV_IMGO_SV_CON2,
	.has_fbc = true,
	.tg_sen_mode = CAMSV_TG_SEN_MODE_DBL_DATA_BUS_EN,
	.module_en = CAMSV_MODULE_EN_DB_EN | CAMSV_MODULE_EN_TG_EN,
	.imgo_con = ((0x8  << CAMSV_IMGO_SV_CON_MAX_BURST_LEN_SHIFT) &
		CAMSV_IMGO_SV_CON_MAX_BURST_LEN_MASK) |
		((0x80 << CAMSV_IMGO_SV_CON_FIFO_SIZE_SHIFT) &
		CAMSV_IMGO_SV_CON_FIFO_SIZE_MASK),
	.imgo_con2 = ((0x2 << CAMSV_IMGO_SV_CON2_FIFO_PREP_PRI_THRL_SHIFT) &
		CAMSV_IMGO_SV_CON2_FIFO_PREP_PRI_THRL_MASK) |
		((0x2 << CAMSV_IMGO_SV_CON2_FIFO_PREP_PRI_THRH_SHIFT) |
		CAMSV_IMGO_SV_CON2_FIFO_PREP_PRI_THRH_MASK),
};

/*
 * MT8183: same camsv family as MT8365 (top/TG blocks are relocated through the
 * DT reg windows: top = base + 0x510, TG = base + 0x220, IMGO = base + 0x20),
 * but the IMGO DMA uses the ISP5 sub-layout and a different FBC (left off).
 * 1-pixel mode (no DBL_DATA_BUS), matching seninf.
 */
static const struct mtk_cam_conf camsv_8183_conf = {
	.imgo_xsize = CAMSV_8183_IMGO_XSIZE,
	.imgo_ysize = CAMSV_8183_IMGO_YSIZE,
	.imgo_stride = CAMSV_8183_IMGO_STRIDE,
	.imgo_con_reg = CAMSV_8183_IMGO_CON,
	.imgo_con2_reg = CAMSV_8183_IMGO_CON2,
	.has_fbc = false,
	.pak_reversed = true,
	.tg_sen_mode = 0,
	.module_en = CAMSV_MODULE_EN_DB_EN | CAMSV_MODULE_EN_TG_EN,
	.imgo_con = ((0x8  << CAMSV_IMGO_SV_CON_MAX_BURST_LEN_SHIFT) &
		CAMSV_IMGO_SV_CON_MAX_BURST_LEN_MASK) |
		((0x80 << CAMSV_IMGO_SV_CON_FIFO_SIZE_SHIFT) &
		CAMSV_IMGO_SV_CON_FIFO_SIZE_MASK),
	.imgo_con2 = 0x00800080,	/* keep the reset value */
};

static u32 mtk_camsv_read(struct mtk_cam_dev *priv, u32 reg)
{
	return readl(priv->regs + reg);
}

static void mtk_camsv_write(struct mtk_cam_dev *priv, u32 reg, u32 value)
{
	writel(value, priv->regs + reg);
}

static void mtk_camsv_img0_write(struct mtk_cam_dev *priv, u32 reg, u32 value)
{
	writel(value, priv->regs_img0 + reg);
}

static u32 mtk_camsv_tg_read(struct mtk_cam_dev *priv, u32 reg)
{
	return readl(priv->regs_tg + reg);
}

static void mtk_camsv_tg_write(struct mtk_cam_dev *priv, u32 reg, u32 value)
{
	writel(value, priv->regs_tg + reg);
}

void mtk_camsv_update_buffers_add(struct mtk_cam_dev *cam_dev,
				  struct mtk_cam_dev_buffer *buf)
{
	mtk_camsv_img0_write(cam_dev, CAMSV_IMGO_SV_BASE_ADDR, buf->daddr);
}

static void mtk_camsv_set_addr(struct mtk_cam_dev *cam_dev, dma_addr_t addr)
{
	mtk_camsv_img0_write(cam_dev, CAMSV_IMGO_SV_BASE_ADDR, (u32)addr);
}

/*
 * No-FBC buffer handling (MT8183), called with buf_list_lock held:
 * take the next queued buffer as the one the DMA writes the next frame into,
 * or park the DMA on the scratch buffer. With MODULE_EN.DB_EN the address is
 * latched at the next frame start.
 */
static void mtk_camsv_arm_next(struct mtk_cam_dev *cam_dev)
{
	struct mtk_cam_dev_buffer *buf;

	buf = list_first_entry_or_null(&cam_dev->buf_list,
				       struct mtk_cam_dev_buffer, list);
	if (buf) {
		list_del(&buf->list);
		cam_dev->cur = buf;
		mtk_camsv_set_addr(cam_dev, buf->daddr);
	} else {
		cam_dev->cur = NULL;
		mtk_camsv_set_addr(cam_dev, cam_dev->scratch_dma);
	}
}

void mtk_camsv_cmos_vf_hw_enable(struct mtk_cam_dev *cam_dev)
{
	unsigned int fbc_val;
	u32 clk_en = CAMSV_TG_DP_CLK_EN | CAMSV_DMA_DP_CLK_EN |
		     CAMSV_PAK_DP_CLK_EN;

	if (cam_dev->conf->has_fbc) {
		fbc_val = mtk_camsv_read(cam_dev, CAMSV_IMGO_FBC);
		fbc_val |= CAMSV_IMGO_FBC_EN;
		mtk_camsv_write(cam_dev, CAMSV_IMGO_FBC, fbc_val);
	}

	mtk_camsv_write(cam_dev,  CAMSV_CLK_EN, clk_en);
	mtk_camsv_tg_write(cam_dev, CAMSV_TG_SEN_MODE,
			     mtk_camsv_tg_read(cam_dev, CAMSV_TG_SEN_MODE) |
			     CAMSV_TG_SEN_MODE_CMOS_EN);
	mtk_camsv_tg_write(cam_dev, CAMSV_TG_VF_CON,
			     mtk_camsv_tg_read(cam_dev, CAMSV_TG_VF_CON) |
			     CAMSV_TG_VF_CON_VFDATA_EN);
	dev_dbg(cam_dev->dev,
		 "stream on: MODULE_EN %08x FMT_SEL %08x PAK %08x CLK_EN %08x TG_SEN_MODE %08x VF_CON %08x GRAB %08x/%08x\n",
		 mtk_camsv_read(cam_dev, CAMSV_MODULE_EN),
		 mtk_camsv_read(cam_dev, CAMSV_FMT_SEL),
		 mtk_camsv_read(cam_dev, CAMSV_PAK),
		 mtk_camsv_read(cam_dev, CAMSV_CLK_EN),
		 mtk_camsv_tg_read(cam_dev, CAMSV_TG_SEN_MODE),
		 mtk_camsv_tg_read(cam_dev, CAMSV_TG_VF_CON),
		 mtk_camsv_tg_read(cam_dev, CAMSV_TG_SEN_GRAB_PXL),
		 mtk_camsv_tg_read(cam_dev, CAMSV_TG_SEN_GRAB_LIN));
	dev_dbg(cam_dev->dev, "stream on: IMGO base %08x xsize %08x ysize %08x stride %08x con %08x con2 %08x\n",
		 readl(cam_dev->regs_img0 + CAMSV_IMGO_SV_BASE_ADDR),
		 readl(cam_dev->regs_img0 + cam_dev->conf->imgo_xsize),
		 readl(cam_dev->regs_img0 + cam_dev->conf->imgo_ysize),
		 readl(cam_dev->regs_img0 + cam_dev->conf->imgo_stride),
		 readl(cam_dev->regs_img0 + cam_dev->conf->imgo_con_reg),
		 readl(cam_dev->regs_img0 + cam_dev->conf->imgo_con2_reg));
}

void mtk_camsv_cmos_vf_hw_disable(struct mtk_cam_dev *cam_dev)
{
	unsigned int fbc_val;

	mtk_camsv_tg_write(cam_dev, CAMSV_TG_SEN_MODE,
			     mtk_camsv_tg_read(cam_dev, CAMSV_TG_SEN_MODE) &
			     ~CAMSV_TG_SEN_MODE_CMOS_EN);
	mtk_camsv_tg_write(cam_dev, CAMSV_TG_VF_CON,
			     mtk_camsv_tg_read(cam_dev, CAMSV_TG_VF_CON) &
			     ~CAMSV_TG_VF_CON_VFDATA_EN);
	if (cam_dev->conf->has_fbc) {
		fbc_val = mtk_camsv_read(cam_dev, CAMSV_IMGO_FBC);
		fbc_val &= ~CAMSV_IMGO_FBC_EN;
		mtk_camsv_write(cam_dev, CAMSV_IMGO_FBC, fbc_val);
	}
	dev_dbg(cam_dev->dev, "stream off: irqs %u frames %u scratch %u errors %u last status %08x\n",
		 cam_dev->irq_cnt, cam_dev->done_cnt, cam_dev->scratch_cnt,
		 cam_dev->err_cnt, cam_dev->last_status);
}

/*
 * Camsv module is able to handle underrruns using the FBC
 *
 *     - FBC_FB_NUM: the number of userspace buffers available (fifo depth)
 *     - RCNT_INC: user writes 1 when buffer is queued/unqueued
 *     - FBC_CNT: decreased at SOF indicating that the number of buffers filled
 *       by write-DMA decreases. Increased when write-dma done indicating that
 *       the number of buffers filled by write-DMA increases
 *     - RCNT: increased at the end of enqueue indicating that the software
 *       moves to the next buffer for reading
 *     - WCNT: increased to indicate that write-dma writes to the next buffer
 *     Drop condition: FBC_CNT == FBC_NUM
 */

void mtk_camsv_fbc_init(struct mtk_cam_dev *cam_dev,
				 unsigned int num_buffers)
{
	unsigned int fbc_val;

	if (!cam_dev->conf->has_fbc)
		return;

	if (pm_runtime_resume_and_get(cam_dev->dev) < 0) {
		dev_err(cam_dev->dev, "failed to get pm_runtime\n");
		return;
	}

	fbc_val = mtk_camsv_read(cam_dev, CAMSV_IMGO_FBC);
	fbc_val &= ~CAMSV_IMGO_FBC_FB_NUM;
	fbc_val |= CAMSV_IMGO_FBC_EN;
	fbc_val |= FIELD_PREP(CAMSV_IMGO_FBC_FB_NUM, num_buffers);
	mtk_camsv_write(cam_dev, CAMSV_IMGO_FBC, fbc_val);

	pm_runtime_mark_last_busy(cam_dev->dev);
	pm_runtime_put_autosuspend(cam_dev->dev);
}

void mtk_camsv_fbc_inc(struct mtk_cam_dev *cam_dev)
{
	unsigned int fbc_val;

	if (!cam_dev->conf->has_fbc)
		return;

	fbc_val = mtk_camsv_read(cam_dev, CAMSV_IMGO_FBC);
	fbc_val |= CAMSV_IMGO_FBC_RCNT_INC;
	mtk_camsv_write(cam_dev, CAMSV_IMGO_FBC, fbc_val);
	fbc_val &= ~CAMSV_IMGO_FBC_RCNT_INC;
	mtk_camsv_write(cam_dev, CAMSV_IMGO_FBC, fbc_val);
}

int mtk_camsv_scratch_alloc(struct mtk_cam_dev *cam_dev, size_t size)
{
	if (cam_dev->conf->has_fbc)
		return 0;

	cam_dev->scratch_size = PAGE_ALIGN(size);
	cam_dev->scratch_va = dma_alloc_coherent(cam_dev->dev,
						 cam_dev->scratch_size,
						 &cam_dev->scratch_dma,
						 GFP_KERNEL);
	if (!cam_dev->scratch_va)
		return -ENOMEM;
	return 0;
}

void mtk_camsv_scratch_free(struct mtk_cam_dev *cam_dev)
{
	if (!cam_dev->scratch_va)
		return;
	dma_free_coherent(cam_dev->dev, cam_dev->scratch_size,
			  cam_dev->scratch_va, cam_dev->scratch_dma);
	cam_dev->scratch_va = NULL;
}

void mtk_camsv_setup(struct mtk_cam_dev *cam_dev, unsigned int count,
		     u32 w, u32 h, u32 bpl, u32 mbus_fmt)
{
	const struct mtk_cam_conf *conf = cam_dev->conf;
	const struct mtk_cam_format_info *fmtinfo;
	struct mtk_cam_dev_buffer *buf;
	unsigned long flags;
	unsigned int i;
	u32 tmp, stride_bits, fmt_sel, tg_sen_mode, pak;

	for (i = 0; i < count; i++)
		mtk_camsv_fbc_inc(cam_dev);

	fmtinfo = mtk_cam_format_info_by_code(mbus_fmt);

	tg_sen_mode = tg_sen_mode_override >= 0 ? tg_sen_mode_override :
		      conf->tg_sen_mode;
	mtk_camsv_tg_write(cam_dev, CAMSV_TG_SEN_MODE, tg_sen_mode);

	mtk_camsv_tg_write(cam_dev, CAMSV_TG_SEN_GRAB_PXL,
			     (w * fmtinfo->w_factor) << 16U);

	mtk_camsv_tg_write(cam_dev, CAMSV_TG_SEN_GRAB_LIN, h << 16U);

	/* YUV_U2S_DIS: disable YUV sensor unsigned to signed */
	mtk_camsv_tg_write(cam_dev, CAMSV_TG_PATH_CFG, 0x1000U);

	/* Reset cam */
	mtk_camsv_write(cam_dev, CAMSV_SW_CTL, CAMSV_SW_RST);
	mtk_camsv_write(cam_dev, CAMSV_SW_CTL, 0x0U);
	mtk_camsv_write(cam_dev, CAMSV_SW_CTL, CAMSV_IMGO_RST_TRIG);

	if (readl_poll_timeout_atomic(cam_dev->regs + CAMSV_SW_CTL, tmp,
				      (tmp == (CAMSV_IMGO_RST_TRIG |
					       CAMSV_IMGO_RST_ST)), 10, 200))
		dev_warn(cam_dev->dev, "IMGO reset handshake timeout, SW_CTL %08x\n",
			 tmp);

	mtk_camsv_write(cam_dev, CAMSV_SW_CTL, 0x0U);

	cam_dev->irq_cnt = 0;
	cam_dev->done_cnt = 0;
	cam_dev->err_cnt = 0;
	cam_dev->scratch_cnt = 0;
	cam_dev->last_status = 0;

	/*
	 * athena: no error interrupts. On MT8183 the bit taken as IMGO_ERR (16, inferred) stays
	 * asserted through a stream whose every frame arrives whole: thousands of interrupts before
	 * the first frame (the TG is armed while the sensor is still being set up) and more after,
	 * until the storm guard below masked them. They said nothing about the frames.
	 */
	mtk_camsv_write(cam_dev, CAMSV_INT_EN, INT_ST_MASK_CAMSV);

	mtk_camsv_write(cam_dev, CAMSV_MODULE_EN, module_en_override >= 0 ?
			module_en_override :
			conf->module_en | fmtinfo->module_en_pak);
	fmt_sel = fmt_sel_override >= 0 ? fmt_sel_override : fmtinfo->fmt_sel;
	mtk_camsv_write(cam_dev, CAMSV_FMT_SEL, fmt_sel);
	pak = fmtinfo->pak;
	if (conf->pak_reversed && (fmtinfo->module_en_pak & CAMSV_MODULE_EN_PAK_EN))
		pak = CAMSV_PAK_PAK_MODE_RAW_8 - pak;
	if (pak_override >= 0)
		pak = pak_override;
	mtk_camsv_write(cam_dev, CAMSV_PAK, pak);

	mtk_camsv_img0_write(cam_dev, conf->imgo_xsize, bpl - 1U);
	mtk_camsv_img0_write(cam_dev, conf->imgo_ysize, h - 1U);

	stride_bits = stride_bits_override >= 0 ? stride_bits_override :
		      fmtinfo->imgo_stride;
	mtk_camsv_img0_write(cam_dev, conf->imgo_stride, stride_bits | bpl);

	mtk_camsv_img0_write(cam_dev, conf->imgo_con_reg, conf->imgo_con);
	mtk_camsv_img0_write(cam_dev, conf->imgo_con2_reg, conf->imgo_con2);

	/* Set buf addr */
	spin_lock_irqsave(&cam_dev->buf_list_lock, flags);
	if (conf->has_fbc) {
		buf = list_first_entry_or_null(&cam_dev->buf_list,
					       struct mtk_cam_dev_buffer,
					       list);
		if (buf)
			mtk_camsv_update_buffers_add(cam_dev, buf);
	} else {
		mtk_camsv_arm_next(cam_dev);
	}
	spin_unlock_irqrestore(&cam_dev->buf_list_lock, flags);

	dev_dbg(cam_dev->dev, "setup %ux%u bpl %u mbus 0x%x fmt_sel %08x pak %08x stride %08x tg_sen_mode %08x\n",
		 w, h, bpl, mbus_fmt, fmt_sel, pak, stride_bits | bpl,
		 tg_sen_mode);

	/* CMOS_EN first */
	mtk_camsv_tg_write(cam_dev, CAMSV_TG_SEN_MODE,
			     mtk_camsv_tg_read(cam_dev, CAMSV_TG_SEN_MODE) |
			     CAMSV_TG_SEN_MODE_CMOS_EN);

	/* finally, CAMSV_MODULE_EN : IMGO_EN */
	mtk_camsv_write(cam_dev, CAMSV_MODULE_EN,
			  mtk_camsv_read(cam_dev, CAMSV_MODULE_EN) |
			  CAMSV_MODULE_EN_IMGO_EN);
}

static irqreturn_t isp_irq_camsv(int irq, void *data)
{
	struct mtk_cam_dev *cam_dev = (struct mtk_cam_dev *)data;
	struct mtk_cam_dev_buffer *processed_buf, *buf;
	unsigned int irq_status;

	irq_status = mtk_camsv_read(cam_dev, CAMSV_INT_STATUS);
	cam_dev->irq_cnt++;
	cam_dev->last_status = irq_status;

	if (irq_status & INT_ST_MASK_CAMSV_ERR) {
		cam_dev->err_cnt++;
		dev_err_ratelimited(cam_dev->dev, "irq error 0x%lx\n",
				    irq_status & INT_ST_MASK_CAMSV_ERR);
		/* error bits can re-assert continuously: stop listening */
		if (cam_dev->err_cnt == 64) {
			mtk_camsv_write(cam_dev, CAMSV_INT_EN, INT_ST_MASK_CAMSV);
			dev_err(cam_dev->dev, "error irq storm, masking error irqs (status %08x)\n",
				irq_status);
		}
	}

	if (cam_dev->irq_cnt <= 5)
		dev_dbg(cam_dev->dev, "irq #%u status %08x TG_INTER_ST %08x\n",
			 cam_dev->irq_cnt, irq_status,
			 mtk_camsv_tg_read(cam_dev, 0x4c));

	if (!(irq_status & CAMSV_IRQ_PASS1_DON))
		return IRQ_HANDLED;

	if (!cam_dev->conf->has_fbc) {
		spin_lock(&cam_dev->buf_list_lock);
		cam_dev->sequence++;
		processed_buf = cam_dev->cur;
		mtk_camsv_arm_next(cam_dev);
		if (!processed_buf)
			cam_dev->scratch_cnt++;
		spin_unlock(&cam_dev->buf_list_lock);

		if (processed_buf) {
			cam_dev->done_cnt++;
			processed_buf->v4l2_buf.sequence = cam_dev->sequence;
			processed_buf->v4l2_buf.vb2_buf.timestamp = ktime_get_ns();
			vb2_buffer_done(&processed_buf->v4l2_buf.vb2_buf,
					VB2_BUF_STATE_DONE);
		}
		return IRQ_HANDLED;
	}

	/* De-queue frame */
	spin_lock(&cam_dev->buf_list_lock);
	cam_dev->sequence++;

	processed_buf = list_first_entry_or_null(&cam_dev->buf_list,
						 struct mtk_cam_dev_buffer,
						 list);
	if (processed_buf)
		list_del(&processed_buf->list);

	buf = list_first_entry_or_null(&cam_dev->buf_list,
				       struct mtk_cam_dev_buffer,
				       list);

	spin_unlock(&cam_dev->buf_list_lock);

	if (processed_buf) {
		cam_dev->done_cnt++;
		processed_buf->v4l2_buf.sequence = cam_dev->sequence;
		processed_buf->v4l2_buf.vb2_buf.timestamp =
		ktime_get_ns();
		vb2_buffer_done(&processed_buf->v4l2_buf.vb2_buf,
				VB2_BUF_STATE_DONE);
		mtk_camsv_fbc_inc(cam_dev);
	}
	/*
	 * If there is no user buffer available, hardware will drop
	 * automatically frames until buf_queue is called.
	 */
	if (buf)
		mtk_camsv_update_buffers_add(cam_dev, buf);

	return IRQ_HANDLED;
}

static int mtk_camsv_stats_show(struct seq_file *m, void *unused)
{
	struct mtk_cam_dev *cam_dev = m->private;

	seq_printf(m, "irqs %u frames %u scratch %u errors %u last_status %08x sequence %u\n",
		   cam_dev->irq_cnt, cam_dev->done_cnt, cam_dev->scratch_cnt,
		   cam_dev->err_cnt, cam_dev->last_status, cam_dev->sequence);
	return 0;
}
DEFINE_SHOW_ATTRIBUTE(mtk_camsv_stats);

static int mtk_camsv_regs_show(struct seq_file *m, void *unused)
{
	struct mtk_cam_dev *cam_dev = m->private;
	unsigned int i;

	if (!pm_runtime_get_if_in_use(cam_dev->dev)) {
		seq_puts(m, "not powered (only readable while streaming)\n");
		return 0;
	}
	seq_puts(m, "top (base+0x510):");
	for (i = 0; i < 0x40; i += 4)
		seq_printf(m, "%s%08x", i % 16 ? " " : "\n  ", readl(cam_dev->regs + i));
	seq_puts(m, "\nimgo (base+0x20):");
	for (i = 0; i < 0x28; i += 4)
		seq_printf(m, "%s%08x", i % 16 ? " " : "\n  ", readl(cam_dev->regs_img0 + i));
	seq_puts(m, "\ntg (base+0x220):");
	for (i = 0; i < 0x90; i += 4)
		seq_printf(m, "%s%08x", i % 16 ? " " : "\n  ", readl(cam_dev->regs_tg + i));
	seq_puts(m, "\n");
	pm_runtime_put(cam_dev->dev);
	return 0;
}
DEFINE_SHOW_ATTRIBUTE(mtk_camsv_regs);

static int mtk_camsv_probe(struct platform_device *pdev)
{
	static const char * const clk_names[] = { "cam", "camtg", "camsv"};

	struct mtk_cam_dev *cam_dev;
	struct device *dev = &pdev->dev;
	unsigned int i;
	int ret;

	if (!device_iommu_mapped(dev))
		return -EPROBE_DEFER;

	cam_dev = devm_kzalloc(dev, sizeof(*cam_dev), GFP_KERNEL);
	if (!cam_dev)
		return -ENOMEM;

	cam_dev->conf = device_get_match_data(dev);
	if (!cam_dev->conf)
		return -ENODEV;

	cam_dev->dev = dev;
	dev_set_drvdata(dev, cam_dev);

	/* IMGO_BASE_ADDR is a 32-bit IOVA */
	ret = dma_set_mask_and_coherent(dev, DMA_BIT_MASK(32));
	if (ret)
		return dev_err_probe(dev, ret, "failed to set DMA mask\n");

	mtk_cam_format_info_apply_out_mode(out_mode);

	cam_dev->regs = devm_platform_ioremap_resource(pdev, 0);
	if (IS_ERR(cam_dev->regs))
		return dev_err_probe(dev, PTR_ERR(cam_dev->regs),
				     "failed to map register base\n");

	cam_dev->regs_img0 = devm_platform_ioremap_resource(pdev, 1);
	if (IS_ERR(cam_dev->regs_img0))
		return dev_err_probe(dev, PTR_ERR(cam_dev->regs_img0),
				     "failed to map img0 register base\n");

	cam_dev->regs_tg = devm_platform_ioremap_resource(pdev, 2);
	if (IS_ERR(cam_dev->regs_tg))
		return dev_err_probe(dev, PTR_ERR(cam_dev->regs_tg),
				     "failed to map TG register base\n");

	cam_dev->num_clks = ARRAY_SIZE(clk_names);
	cam_dev->clks = devm_kcalloc(dev, cam_dev->num_clks,
				     sizeof(*cam_dev->clks), GFP_KERNEL);
	if (!cam_dev->clks)
		return -ENOMEM;

	for (i = 0; i < cam_dev->num_clks; ++i)
		cam_dev->clks[i].id = clk_names[i];

	ret = devm_clk_bulk_get(dev, cam_dev->num_clks, cam_dev->clks);
	if (ret)
		return dev_err_probe(dev, ret, "failed to get clocks: %i\n",
				     ret);

	cam_dev->irq = platform_get_irq(pdev, 0);
	ret = devm_request_irq(dev, cam_dev->irq, isp_irq_camsv, 0,
			       dev_name(dev), cam_dev);
	if (ret)
		return dev_err_probe(dev, ret, "failed to request irq=%d\n",
				     cam_dev->irq);

	spin_lock_init(&cam_dev->buf_list_lock);

	/* initialise runtime power management */
	pm_runtime_set_autosuspend_delay(dev, MTK_CAMSV_AUTOSUSPEND_DELAY_MS);
	pm_runtime_use_autosuspend(dev);
	pm_runtime_set_suspended(dev);
	devm_pm_runtime_enable(dev);

	cam_dev->dbg = debugfs_create_dir(dev_name(dev), NULL);
	debugfs_create_file("stats", 0444, cam_dev->dbg, cam_dev,
			    &mtk_camsv_stats_fops);
	debugfs_create_file("regs", 0400, cam_dev->dbg, cam_dev,
			    &mtk_camsv_regs_fops);

	dev_info(dev, "camsv irq %u fbc %s out_mode %u\n", cam_dev->irq,
		 cam_dev->conf->has_fbc ? "on" : "off (scratch buffer)",
		 out_mode);

	/* Initialize the v4l2 common part */
	ret = mtk_cam_dev_init(cam_dev);
	if (ret)
		debugfs_remove_recursive(cam_dev->dbg);
	return ret;
}

static void mtk_camsv_remove(struct platform_device *pdev)
{
	struct device *dev = &pdev->dev;
	struct mtk_cam_dev *cam_dev = dev_get_drvdata(dev);

	debugfs_remove_recursive(cam_dev->dbg);
	mtk_cam_dev_cleanup(cam_dev);
	/* runtime PM is disabled by the devm action of devm_pm_runtime_enable() */
	pm_runtime_dont_use_autosuspend(cam_dev->dev);

}

#ifdef CONFIG_PM
static int mtk_camsv_runtime_suspend(struct device *dev)
{
	struct mtk_cam_dev *cam_dev = dev_get_drvdata(dev);
	struct vb2_queue *vbq = &cam_dev->vdev.vbq;

	if (vb2_is_streaming(vbq)) {
		v4l2_subdev_disable_streams(&cam_dev->subdev,
					    cam_dev->subdev_pads[MTK_CAM_CIO_PAD_VIDEO].index,
					    BIT(0));
	}

	clk_bulk_disable_unprepare(cam_dev->num_clks, cam_dev->clks);

	return 0;
}

static int mtk_camsv_runtime_resume(struct device *dev)
{
	struct mtk_cam_dev *cam_dev = dev_get_drvdata(dev);
	struct vb2_queue *vbq = &cam_dev->vdev.vbq;
	struct mtk_cam_dev_buffer *buf;
	unsigned long flags = 0;
	int ret;

	ret = clk_bulk_prepare_enable(cam_dev->num_clks, cam_dev->clks);
	if (ret) {
		dev_err(dev, "failed to enable clock:%d\n", ret);
		return ret;
	}

	if (vb2_is_streaming(vbq)) {

		spin_lock_irqsave(&cam_dev->buf_list_lock, flags);
		buf = list_first_entry_or_null(&cam_dev->buf_list,
					       struct mtk_cam_dev_buffer,
					       list);
		if (buf)
			mtk_camsv_update_buffers_add(cam_dev, buf);

		spin_unlock_irqrestore(&cam_dev->buf_list_lock, flags);

		/* Stream on the sub-device */
		ret = v4l2_subdev_enable_streams(&cam_dev->subdev,
						 cam_dev->subdev_pads[MTK_CAM_CIO_PAD_VIDEO].index,
						 BIT(0));

		if (ret)
			goto fail_no_stream;
	}

	return 0;

fail_no_stream:
	mtk_cam_vb2_return_all_buffers(cam_dev, VB2_BUF_STATE_QUEUED);
	return ret;
}

static const struct dev_pm_ops mtk_camsv_pm_ops = {
	SET_SYSTEM_SLEEP_PM_OPS(pm_runtime_force_suspend,
				pm_runtime_force_resume)
	SET_RUNTIME_PM_OPS(mtk_camsv_runtime_suspend,
			   mtk_camsv_runtime_resume, NULL)
};
#endif

static const struct of_device_id mtk_camsv_of_ids[] = {
	{ .compatible = "mediatek,mt8365-camsv", .data = &camsv_conf },
	{ .compatible = "mediatek,mt8183-camsv", .data = &camsv_8183_conf },
	{ /* sentinel */ }
};
MODULE_DEVICE_TABLE(of, mtk_camsv_of_ids);

static struct platform_driver mtk_camsv_driver = {
	.probe = mtk_camsv_probe,
	.remove = mtk_camsv_remove,
	.driver = {
		.name = "mtk-camsv-isp",
		.of_match_table = mtk_camsv_of_ids,
		.pm = &mtk_camsv_pm_ops,
	}
};

module_platform_driver(mtk_camsv_driver);

MODULE_DESCRIPTION("MediaTek CAMSV ISP3.0 driver");
MODULE_AUTHOR("Florian Sylvestre <fsylvestre@baylibre.com>");
MODULE_LICENSE("GPL");
