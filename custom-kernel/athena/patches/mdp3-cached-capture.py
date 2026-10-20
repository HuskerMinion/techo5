#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Give the MDP3's output pictures cacheable memory, so the cores read them at full speed.

    mdp3-cached-capture.py KTREE

The MDP3 (/dev/video* "mtk-mdp3:m2m") hands its pictures to programs that read them with the cores:
the video player turns the hardware decoder's tiled frames into the panel's pixels with it and pads
them (echod's feature/video). vb2-dma-contig's buffers are write-combined by default, and a core
reading one runs at a fraction of the speed of cached memory: a 1280x720 BGRA frame took ~77 ms to
pad. A capture queue's MMAP buffers are allocated non-coherent instead (V4L2_MEMORY_FLAG_NON_COHERENT,
as if the program had asked for it): cached, with the cache cleaned and invalidated around each
transfer by vb2. The input queue is left as it is (the cores write it, or it is the decoder's).
Idempotent.
"""
import sys

MARK = "athena: the MDP3's output pictures are cacheable"
p = sys.argv[1] + "/drivers/media/platform/mediatek/mdp3/mtk-mdp3-m2m.c"
s = open(p).read()
if MARK in s:
    print(p + ": already patched")
    sys.exit(0)

edits = [
    ("\t.vidioc_reqbufs\t\t\t= v4l2_m2m_ioctl_reqbufs,\n",
     "\t.vidioc_reqbufs\t\t\t= mdp_m2m_reqbufs,\n"),
    ("static const struct v4l2_ioctl_ops mdp_m2m_ioctl_ops = {\n",
     "/* %s: a capture queue's MMAP buffers are non-coherent (cached) */\n"
     "static int mdp_m2m_reqbufs(struct file *file, void *fh, struct v4l2_requestbuffers *rb)\n"
     "{\n"
     "\tif (V4L2_TYPE_IS_CAPTURE(rb->type) && rb->memory == V4L2_MEMORY_MMAP)\n"
     "\t\trb->flags |= V4L2_MEMORY_FLAG_NON_COHERENT;\n"
     "\treturn v4l2_m2m_ioctl_reqbufs(file, fh, rb);\n"
     "}\n\n"
     "static const struct v4l2_ioctl_ops mdp_m2m_ioctl_ops = {\n" % MARK),
    ("\tdst_vq->lock = &ctx->ctx_lock;\n\n\treturn vb2_queue_init(dst_vq);\n",
     "\tdst_vq->lock = &ctx->ctx_lock;\n\tdst_vq->allow_cache_hints = 1;\n\n\treturn vb2_queue_init(dst_vq);\n"),
]
for old, new in edits:
    if s.count(old) != 1:
        sys.exit("mdp3-cached-capture: %s: %r is not there once" % (p, old.strip()))
    s = s.replace(old, new)
open(p, "w").write(s)
print("patched")
