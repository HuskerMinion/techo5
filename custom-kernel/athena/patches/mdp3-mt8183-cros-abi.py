#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Patch mainline mtk-mdp3: talk to linux-firmware's MT8183 SCP in the layout it was built for.

    mdp3-mt8183-cros-abi.py KTREE

The MDP3 driver and the SCP firmware share a few structures in DRAM: the frame the driver asks for
(img_ipi_frameparam), and the config the firmware works out for it (img_config: the engines, their
registers per tile, the MMSYS mux settings), which the driver turns into GCE commands. Mainline's
MT8183 layout is the one of an SCP prebuild that was never published (mdp_sm_mt8183.h names its MD5,
2d995ddb...). linux-firmware's mediatek/mt8183/scp.img (kukui_scp_v2.0.13324, also what ChromeOS
ships) speaks the layout of the ChromeOS 5.10 kernel instead, the one the kukui Chromebooks run:

  - each img_output ends in a u64 pqid, and the frame in a u32 frame_change: 32 + 4 bytes more, so
    the firmware read the config address 32 bytes past where mainline wrote it, found 0 and failed
    every frame ("MDP_FRAME Error Data: config:0 ..." on the SCP's console, "VPU MDP failure:-22");
  - the config is the 5.10 one, every field 32-bit, its per-engine union sized for all of the
    MT8195's engines too (mainline's MT8183 config is narrower, with u16 ids and sizes);
  - the frame is read from the working buffer given at MDP_INIT, the frame copied to its start,
    and self_data is the firmware's own path scratch (0x78000 bytes, cleared when frame_change says
    the frame differs: here always, as nothing tracks it);
  - the third word of a mux setting is a VPP index (0 on the MT8183), not a GCE subsys: the writes go
    to MMSYS, at 0x14000000, which is GCE subsys SUBSYS_1400XXXX.

So on the MT8183 the structures in mdp_sm_mt8183.h take the 5.10 layout (the field names the driver
uses are all there), the frame grows the two fields, and the frame and mux handling follow 5.10. The
MT8195 and MT8188 keep mainline's handling; their frames grow by the same 36 bytes, which only this
kernel's MT8183 build is built for. Idempotent.
"""
import sys

MARK = "athena: ChromeOS 5.10 layout for the MT8183 SCP"
D = sys.argv[1] + "/drivers/media/platform/mediatek/mdp3/"


def edit(name, pairs):
    p = D + name
    s = open(p).read()
    for old, new in pairs:
        if s.count(old) != 1:
            sys.exit("mdp3-mt8183-cros-abi: %s: unexpected source near %r" % (p, old[:60]))
        s = s.replace(old, new)
    open(p, "w").write(s)


if MARK in open(D + "mdp_sm_mt8183.h").read():
    print(D + ": already patched")
    sys.exit(0)

# 1. The frame: pqid per output, frame_change at the end
edit("mtk-img-ipi.h", [
    ("""	s32 rotation;
	u32 flags; /* H-flip, sharpness, dither */
} __packed;""", """	s32 rotation;
	u32 flags; /* H-flip, sharpness, dither */
	u64 pqid; /* %s */
} __packed;""" % MARK),
    ("""	struct img_sw_addr config_data;
	struct img_sw_addr self_data;
} __packed;""", """	struct img_sw_addr config_data;
	struct img_sw_addr self_data;
	u32 frame_change; /* %s */
} __packed;""" % MARK),
])

# 2. The config: the 5.10 structures, under the names the driver uses
SM = """/* SPDX-License-Identifier: GPL-2.0-only */
/*
 * Copyright (c) 2023 MediaTek Inc.
 * Author: Ping-Hsun Wu <ping-hsun.wu@mediatek.com>
 */

#ifndef __MDP_SM_MT8183_H__
#define __MDP_SM_MT8183_H__

#include "mtk-mdp3-type.h"

/*
 * ISP-MDP generic output information, %s:
 * the layout of the ChromeOS 5.10 kernel, which linux-firmware's mediatek/mt8183/scp.img
 * (kukui_scp_v2.0.13324) writes.
 */

#define IMG_MAX_SUBFRAMES_8183      14

struct img_comp_frame_8183 {
	u32 output_disable;
	u32 bypass;
	u32 in_width;
	u32 in_height;
	u32 out_width;
	u32 out_height;
	struct img_crop crop;
	u32 in_total_width;
	u32 out_total_width;
} __packed;

struct img_comp_subfrm_8183 {
	u32 tile_disable;
	struct img_region in;
	struct img_region out;
	struct img_offset luma;
	struct img_offset chroma;
	s32 out_vertical; /* Output vertical index */
	s32 out_horizontal; /* Output horizontal index */
} __packed;

struct mdp_rdma_subfrm_8183 {
	u32 offset[IMG_MAX_PLANES];
	u32 offset_0_p;
	u32 src;
	u32 clip;
	u32 clip_ofst;
	u32 in_tile_xleft;
	u32 in_tile_ytop;
} __packed;

struct mdp_rdma_data_8183 {
	u32 src_ctrl;
	u32 comp_ctrl;
	u32 control;
	u32 iova[IMG_MAX_PLANES];
	u32 iova_end[IMG_MAX_PLANES];
	u32 mf_bkgd;
	u32 mf_bkgd_in_pxl;
	u32 sf_bkgd;
	u32 ufo_dec_y;
	u32 ufo_dec_c;
	u32 transform;
	u32 dmabuf_con0;
	u32 ultra_th_high_con0;
	u32 ultra_th_low_con0;
	u32 dmabuf_con1;
	u32 ultra_th_high_con1;
	u32 ultra_th_low_con1;
	u32 dmabuf_con2;
	u32 ultra_th_high_con2;
	u32 ultra_th_low_con2;
	u32 dmabuf_con3;
	struct mdp_rdma_subfrm_8183 subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct mdp_fg_data_8183 {
	u32 ctrl_0;
	u32 ck_en;
	struct { u32 info_0, info_1; } __packed subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct mdp_hdr_data_8183 {
	u32 top;
	u32 relay;
	struct {
		u32 win_size, src, clip_ofst0, clip_ofst1, hist_ctrl_0, hist_ctrl_1, hdr_top, hist_addr;
	} __packed subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct mdp_aal_data_8183 {
	u32 cfg_main;
	u32 cfg;
	struct { u32 src, clip, clip_ofst; } __packed subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct mdp_rsz_subfrm_8183 {
	u32 control2;
	u32 src;
	u32 clip;
	u32 hdmirx_en;
	u32 luma_h_int_ofst;
	u32 luma_h_sub_ofst;
	u32 luma_v_int_ofst;
	u32 luma_v_sub_ofst;
	u32 chroma_h_int_ofst;
	u32 chroma_h_sub_ofst;
	u32 rsz_switch;
	u32 merge_cfg;
} __packed;

struct mdp_rsz_data_8183 {
	u32 coeff_step_x;
	u32 coeff_step_y;
	u32 control1;
	u32 control2;
	u32 etc_control;
	u32 prz_enable;
	u32 ibse_softclip;
	u32 tap_adapt;
	u32 ibse_gaincontrol1;
	u32 ibse_gaincontrol2;
	u32 ibse_ylevel_1;
	u32 ibse_ylevel_2;
	u32 ibse_ylevel_3;
	u32 ibse_ylevel_4;
	u32 ibse_ylevel_5;
	struct mdp_rsz_subfrm_8183 subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct mdp_tdshp_data_8183 {
	u32 cfg;
	struct { u32 src, clip, clip_ofst, hist_cfg_0, hist_cfg_1; } __packed
		subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct mdp_color_data_8183 {
	u32 start;
	struct { u32 in_hsize, in_vsize; } __packed subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct mdp_ovl_data_8183 {
	u32 L0_con;
	u32 src_con;
	struct { u32 L0_src_size, roi_size; } __packed subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct mdp_pad_data_8183 {
	struct { u32 pic_size; } __packed subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct mdp_tcc_data_8183 {
	struct { u32 pic_size; } __packed subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct mdp_wrot_subfrm_8183 {
	u32 offset[IMG_MAX_PLANES];
	u32 src;
	u32 clip;
	u32 clip_ofst;
	u32 main_buf;
} __packed;

struct mdp_wrot_data_8183 {
	u32 iova[IMG_MAX_PLANES];
	u32 control;
	u32 stride[IMG_MAX_PLANES];
	u32 mat_ctrl;
	u32 fifo_test;
	u32 filter;
	u32 pre_ultra;
	u32 framesize;
	u32 afbc_yuvtrans;
	u32 scan_10bit;
	u32 pending_zero;
	u32 bit_number;
	u32 pvric;
	u32 vpp02vpp1;
	struct mdp_wrot_subfrm_8183 subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct mdp_wdma_subfrm_8183 {
	u32 offset[IMG_MAX_PLANES];
	u32 src;
	u32 clip;
	u32 clip_ofst;
} __packed;

struct mdp_wdma_data_8183 {
	u32 wdma_cfg;
	u32 iova[IMG_MAX_PLANES];
	u32 w_in_byte;
	u32 uv_stride;
	struct mdp_wdma_subfrm_8183 subfrms[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct isp_data_8183 {
	u64 dl_flags; /* 1 << (enum mdp_comp_type) */
	u32 smxi_iova[4];
	u32 cq_idx;
	u32 cq_iova;
	u32 tpipe_iova[IMG_MAX_SUBFRAMES_8183];
} __packed;

struct img_compparam_8183 {
	u32 type; /* enum mdp_comp_id */
	u32 id; /* engine alias_id */
	u32 input;
	u32 outputs[IMG_MAX_HW_OUTPUTS];
	u32 num_outputs;
	struct img_comp_frame_8183 frame;
	struct img_comp_subfrm_8183 subfrms[IMG_MAX_SUBFRAMES_8183];
	u32 num_subfrms;
	union {
		struct mdp_rdma_data_8183 rdma;
		struct mdp_fg_data_8183 fg;
		struct mdp_hdr_data_8183 hdr;
		struct mdp_aal_data_8183 aal;
		struct mdp_rsz_data_8183 rsz;
		struct mdp_tdshp_data_8183 tdshp;
		struct mdp_color_data_8183 color;
		struct mdp_ovl_data_8183 ovl;
		struct mdp_pad_data_8183 pad;
		struct mdp_tcc_data_8183 tcc;
		struct mdp_wrot_data_8183 wrot;
		struct mdp_wdma_data_8183 wdma;
		struct isp_data_8183 isp;
	};
} __packed;

struct img_config_8183 {
	struct img_compparam_8183 components[IMG_MAX_COMPONENTS];
	u32 num_components;
	struct img_mmsys_ctrl ctrls[IMG_MAX_SUBFRAMES_8183];
	u32 num_subfrms;
} __packed;

#endif  /* __MDP_SM_MT8183_H__ */
""" % MARK
open(D + "mdp_sm_mt8183.h", "w").write(SM)

# 3. The frame in the working buffer, self_data the path scratch
edit("mtk-mdp3-vpu.h", [
    ("""	size_t			config_size;
	u32			status;""", """	size_t			config_size;
	void			*path;		/* %s */
	dma_addr_t		path_addr;
	u32			status;""" % MARK),
])
edit("mtk-mdp3-vpu.c", [
    ("""#define MDP_VPU_MESSAGE_TIMEOUT 500U
""", """#define MDP_VPU_MESSAGE_TIMEOUT 500U
/* %s: the firmware's path scratch, self_data */
#define MDP_VPU_PATH_SIZE_8183	0x78000
""" % MARK),
    ("""	if (vpu->config && vpu->config_addr)
		dma_free_wc(dev, vpu->config_size, vpu->config, vpu->config_addr);""",
     """	if (vpu->config && vpu->config_addr)
		dma_free_wc(dev, vpu->config_size, vpu->config, vpu->config_addr);

	if (vpu->path && vpu->path_addr)
		dma_free_wc(dev, MDP_VPU_PATH_SIZE_8183, vpu->path, vpu->path_addr);"""),
    ("""	memcpy(vpu->param, param, sizeof(*param));

	addr.pa = vpu->param_addr;
	addr.va = (unsigned long)vpu->param;
""", """	if (CFG_CHECK(MT8183, mdp->mdp_data->mdp_plat_id)) {
		/* %s: as 5.10 sent it */
		if (!vpu->path)
			vpu->path = dma_alloc_wc(scp_get_device(vpu->scp), MDP_VPU_PATH_SIZE_8183,
						 &vpu->path_addr, GFP_KERNEL);
		if (!vpu->path) {
			dev_err(&mdp->pdev->dev, "VPU memory alloc fail!");
			return -ENOMEM;
		}
		memset(vpu->path, 0, MDP_VPU_PATH_SIZE_8183);
		param->self_data.va = (unsigned long)vpu->path;
		param->self_data.pa = vpu->path_addr;
		param->frame_change = 1;
		memcpy(vpu->work, param, sizeof(*param));
		addr.pa = vpu->work_addr;
		addr.va = (unsigned long)vpu->work;
	} else {
		memcpy(vpu->param, param, sizeof(*param));
		addr.pa = vpu->param_addr;
		addr.va = (unsigned long)vpu->param;
	}
""" % MARK),
])

# 4. The mux settings go to MMSYS
edit("mtk-mdp3-cmdq.c", [
    ("""		set = &ctrl->sets[index];
		cmdq_pkt_write(&cmd->pkt, set->subsys_id, set->reg, set->value);""",
     """		set = &ctrl->sets[index];
		cmdq_pkt_write(&cmd->pkt, mdp_mux_subsys(p_id, set), set->reg, set->value);"""),
    ("""		set = &ctrl->sets[index];
		cmdq_pkt_write(&cmd->pkt, set->subsys_id, set->reg, 0);""",
     """		set = &ctrl->sets[index];
		cmdq_pkt_write(&cmd->pkt, mdp_mux_subsys(p_id, set), set->reg, 0);"""),
])
p = D + "mtk-mdp3-cmdq.c"
s = open(p).read()
anchor = "static int mdp_path_subfrm_run("
if s.count(anchor) != 1:
    sys.exit("mdp3-mt8183-cros-abi: %s: no mdp_path_subfrm_run" % p)
s = s.replace(anchor, """/*
 * %s: the MT8183 firmware's third word is a VPP index; its mux registers are MMSYS's
 * (0x14000000, GCE subsys SUBSYS_1400XXXX).
 */
static u8 mdp_mux_subsys(int p_id, const struct img_mux *set)
{
	return CFG_CHECK(MT8183, p_id) ? 1 : set->subsys_id;
}

""" % MARK + anchor, 1)
open(p, "w").write(s)
print("patched")
