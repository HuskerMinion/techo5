#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Patch mainline mtk-jpeg: finish an encode that ends in anything but done, rather than leave it
waiting for ever.

mtk_jpeg_enc_irq cancels the job's timeout, then returns at once when the interrupt is not the done
one. The MT8183's encoder raises another one when the bitstream buffer fills before the picture
ends (status 0x2: seen on athena with the driver's default 1 MB buffer and a noisy 2112x1568 camera
frame at quality 90), so the job is never finished: its buffers never come back, and STREAMOFF then
waits in v4l2_m2m_cancel_job, uninterruptibly, with the process holding the device. Any status but
done now finishes the job with both buffers in error, the way the timeout does (the caller can
encode the picture again with a bigger buffer, or another way).

Usage: patch-jpeg-enc-stall.py KTREE   (idempotent)
"""
import sys

tree = sys.argv[1]
MARK = "athena: a stalled encode"
p = tree + "/drivers/media/platform/mediatek/jpeg/mtk_jpeg_core.c"
s = open(p).read()
if MARK in s:
    print(p + ": already patched")
    sys.exit(0)

irq_a = """	if (!(irq_status & JPEG_ENC_INT_STATUS_DONE))
		return ret;

	ret = mtk_jpeg_enc_done(jpeg);
	return ret;
}
"""
irq_b = """	if (!(irq_status & JPEG_ENC_INT_STATUS_DONE))
		return irq_status ? mtk_jpeg_enc_failed(jpeg) : ret;

	ret = mtk_jpeg_enc_done(jpeg);
	return ret;
}
"""
done = "static irqreturn_t mtk_jpeg_enc_done(struct mtk_jpeg_dev *jpeg)\n"
failed = """/*
 * athena: a stalled encode (a full bitstream buffer) or an error. Both buffers go back in error and
 * the job is finished, as mtk_jpeg_job_timeout_work does, instead of waiting for a done interrupt
 * that never comes.
 */
static irqreturn_t mtk_jpeg_enc_failed(struct mtk_jpeg_dev *jpeg)
{
	struct mtk_jpeg_ctx *ctx;
	struct vb2_v4l2_buffer *src_buf, *dst_buf;

	ctx = v4l2_m2m_get_curr_priv(jpeg->m2m_dev);
	if (!ctx) {
		v4l2_err(&jpeg->v4l2_dev, "Context is NULL\\n");
		return IRQ_HANDLED;
	}

	src_buf = v4l2_m2m_src_buf_remove(ctx->fh.m2m_ctx);
	dst_buf = v4l2_m2m_dst_buf_remove(ctx->fh.m2m_ctx);

	jpeg->variant->hw_reset(jpeg->reg_base);

	vb2_set_plane_payload(&dst_buf->vb2_buf, 0, 0);
	v4l2_m2m_buf_done(src_buf, VB2_BUF_STATE_ERROR);
	v4l2_m2m_buf_done(dst_buf, VB2_BUF_STATE_ERROR);
	v4l2_m2m_job_finish(jpeg->m2m_dev, ctx->fh.m2m_ctx);
	pm_runtime_put(ctx->jpeg->dev);
	return IRQ_HANDLED;
}

"""
for anchor in (irq_a, done):
    if s.count(anchor) != 1:
        sys.exit(p + ": anchor not found once: " + anchor.splitlines()[0])
s = s.replace(done, failed + done).replace(irq_a, irq_b)
open(p, "w").write(s)
print(p + ": patched")
