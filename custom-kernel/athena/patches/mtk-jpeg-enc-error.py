#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Finish an mtk-jpeg encode that ends in anything but done, rather than leave it for ever.

    mtk-jpeg-enc-error.py <linux tree>

mtk_jpeg_enc_irq cancels the job's timeout, then returns at once when the interrupt is not the done
one. The encoder raises another when the bitstream buffer fills before the picture ends (status 0x2,
read on the 2nd gen Show 8 with the driver's default 1 MB buffer and a noisy 2112x1568 frame at
quality 90), so the job is never finished: the buffers never come back, and STREAMOFF then waits in
v4l2_m2m_cancel_job, uninterruptibly, with the process holding the device. Any status but done now
finishes the job with both buffers in error, as a timed-out job is. Idempotent.
"""
import os
import sys

ANCHOR = """	if (!(irq_status & JPEG_ENC_INT_STATUS_DONE))
		return ret;

	ret = mtk_jpeg_enc_done(jpeg);
	return ret;
}
"""
PATCHED = """	if (!(irq_status & JPEG_ENC_INT_STATUS_DONE))
		return irq_status ? mtk_jpeg_enc_failed(jpeg) : ret;

	ret = mtk_jpeg_enc_done(jpeg);
	return ret;
}
"""
DONE = "static irqreturn_t mtk_jpeg_enc_done(struct mtk_jpeg_dev *jpeg)\n"
FAILED = """/* The encode ended in a stall or an error: both buffers go back in error and the job is finished. */
static irqreturn_t mtk_jpeg_enc_failed(struct mtk_jpeg_dev *jpeg)
{
	struct mtk_jpeg_ctx *ctx = v4l2_m2m_get_curr_priv(jpeg->m2m_dev);
	struct vb2_v4l2_buffer *src_buf, *dst_buf;

	if (!ctx)
		return IRQ_HANDLED;
	mtk_jpeg_enc_reset(jpeg->reg_base);
	src_buf = v4l2_m2m_src_buf_remove(ctx->fh.m2m_ctx);
	dst_buf = v4l2_m2m_dst_buf_remove(ctx->fh.m2m_ctx);
	vb2_set_plane_payload(&dst_buf->vb2_buf, 0, 0);
	v4l2_m2m_buf_done(src_buf, VB2_BUF_STATE_ERROR);
	v4l2_m2m_buf_done(dst_buf, VB2_BUF_STATE_ERROR);
	v4l2_m2m_job_finish(jpeg->m2m_dev, ctx->fh.m2m_ctx);
	pm_runtime_put(ctx->jpeg->dev);
	return IRQ_HANDLED;
}

"""

path = os.path.join(sys.argv[1], "drivers/media/platform/mediatek/jpeg/mtk_jpeg_core.c")
src = open(path).read()
if PATCHED in src:
    sys.exit(0)
if src.count(ANCHOR) != 1 or src.count(DONE) != 1:
    sys.exit("%s: not where mtk-jpeg-enc-error.py expects it" % path)
src = src.replace(DONE, FAILED + DONE).replace(ANCHOR, PATCHED)
open(path, "w").write(src)
