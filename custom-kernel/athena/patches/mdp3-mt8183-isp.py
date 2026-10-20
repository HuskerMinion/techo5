#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-2.0
"""Patch mainline mtk-mdp3: run the MT8183's ISP pass 2 (DIP) as the head of an MDP path.

    mdp3-mt8183-isp.py KTREE

The DIP develops a RAW picture from memory (demosaic, color, noise reduction) and hands it to the MDP
over a direct link, which scales and writes it out. The SCP works out the whole job, the DIP's command
queue (CQ) and tile programs in memory included, and the AP's GCE starts it: it points the DIP at the
CQ, triggers the CQ thread and waits for its done event, then runs the MDP engines as for any frame.
The SCP never writes a register, so this works where the codecs do not (README.md).

Mainline's MDP3 kept the MT8183's ISP components in its tables (MDP_COMP_ISP_IMGI, the CAMIN direct
links, the IMGI pipe) but not their operations, nor a way to find them in the device tree. This puts
back the IMGI operations of the ChromeOS 5.10 kernel (mtk-mdp3-comp.c: init_isp, config_isp_frame,
config_isp_subfrm, wait_isp_event) and two compatibles: "mediatek,mt8183-mdp3-imgi" (the ISP, its
registers at 0x15020000 through GCE subsys SUBSYS_1502XXXX) and "mediatek,mt8183-mdp3-dl" (the direct
links into the MDP, CAMIN then CAMIN2, each with its two clocks). Their table entries are made to read
their clocks and registers from their own nodes. The CQ thread's done events are GCE's
CMDQ_EVENT_ISP_FRAME_DONE_P2_0 onwards (257 + thread).

The direct links get the operations ChromeOS 5.10 ran from mtk-mmsys (mtk_mmsys_mdp_isp_ctrl,
mtk_mmsys_mdp_camin_ctrl): async reset, SOF mode and each tile's size. And the MDP's packets now hold
the GCE awake (runtime PM) while they run, which mainline's MDP3 never did. Idempotent.
"""
import sys

MARK = "athena: the MT8183's ISP pass 2"
D = sys.argv[1] + "/drivers/media/platform/mediatek/mdp3/"


def edit(name, pairs):
    p = D + name
    s = open(p).read()
    for old, new in pairs:
        if s.count(old) != 1:
            sys.exit("mdp3-mt8183-isp: %s: unexpected source near %r" % (p, old[:60]))
        s = s.replace(old, new)
    open(p, "w").write(s)


if MARK in open(D + "mtk-mdp3-comp.c").read():
    print(D + ": already patched")
    sys.exit(0)

OPS = """
/*
 * MARK: the IMGI component, as the ChromeOS 5.10 kernel ran it. The SCP's config gives the
 * DIP's CQ thread and the CQ and tile programs it wrote; the DIP loads its registers from them.
 */
#define MDP_ISP_CQ_THREADS	12
#define MDP_ISP_P2_DONE_EVENT	257	/* CMDQ_EVENT_ISP_FRAME_DONE_P2_0 */

static int init_isp(struct mdp_comp_ctx *ctx, struct mdp_cmdq_cmd *cmd)
{
	return 0;
}

static int config_isp_frame(struct mdp_comp_ctx *ctx, struct mdp_cmdq_cmd *cmd,
			    const struct v4l2_rect *compose)
{
	struct device *dev = &ctx->comp->mdp_dev->pdev->dev;
	phys_addr_t base = ctx->comp->reg_base;
	u8 subsys_id = ctx->comp->subsys_id;
	u32 cq = CFG_COMP(MT8183, ctx->param, isp.cq_idx);
	int i;
	/* DIP_X_SMXnI_BASE_ADDR, DIP_X_SMXnO_BASE_ADDR */
	static const u16 smxi[4] = { 0x2890, 0x28c0, 0x28f0, 0x2920 };
	static const u16 smxo[4] = { 0x27d0, 0x2800, 0x2830, 0x2860 };

	for (i = 0; i < 4; i++) {
		u32 iova = CFG_COMP(MT8183, ctx->param, isp.smxi_iova[i]);

		cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + smxi[i], iova, 0xffffffff);
		cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + smxo[i], iova, 0xffffffff);
	}
	if (cq >= MDP_ISP_CQ_THREADS) {
		dev_err(dev, "ISP CQ thread %u is not one of the DIP's\\n", cq);
		return -EINVAL;
	}
	/* DIP_X_CQ_THRn_BASEADDR */
	cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + 0x2208 + 0xc * cq,
			    CFG_COMP(MT8183, ctx->param, isp.cq_iova), 0xffffffff);
	return 0;
}

static int config_isp_subfrm(struct mdp_comp_ctx *ctx, struct mdp_cmdq_cmd *cmd, u32 index)
{
	phys_addr_t base = ctx->comp->reg_base;
	u8 subsys_id = ctx->comp->subsys_id;

	/* DIP_X_TDRI_BASE_ADDR: this tile's program */
	cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + 0x2304,
			    CFG_COMP(MT8183, ctx->param, isp.tpipe_iova[index]), 0xffffffff);
	return 0;
}

static int wait_isp_event(struct mdp_comp_ctx *ctx, struct mdp_cmdq_cmd *cmd)
{
	phys_addr_t base = ctx->comp->reg_base;
	u8 subsys_id = ctx->comp->subsys_id;
	u64 dl = CFG_COMP(MT8183, ctx->param, isp.dl_flags);
	u32 cq = CFG_COMP(MT8183, ctx->param, isp.cq_idx);

	/* MDP_DL_SEL, MDP2_DL_SEL: the MDP crops take the DIP's link */
	if (dl & BIT(MDP_COMP_CAMIN))
		cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + 0x30, 0x0, BIT(9));
	if (dl & BIT(MDP_COMP_CAMIN2))
		cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + 0x30, 0x0, BIT(10) | BIT(11));
	if (cq >= MDP_ISP_CQ_THREADS)
		return -EINVAL;
	/* DIP_X_CTL_START: run the CQ thread, then wait for its frame */
	cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + 0x2000, BIT(cq), BIT(cq));
	MM_REG_WAIT(cmd, MDP_ISP_P2_DONE_EVENT + cq);
	return 0;
}

static const struct mdp_comp_ops imgi_ops = {
	.get_comp_flag = get_comp_flag,
	.init_comp = init_isp,
	.config_frame = config_isp_frame,
	.config_subfrm = config_isp_subfrm,
	.wait_comp_event = wait_isp_event,
};

/*
 * The direct links from the ISP into the MDP (CAMIN, CAMIN2), whose registers are MMSYS's
 * (0x14000000, SUBSYS_1400XXXX), as ChromeOS 5.10's mtk_mmsys_mdp_isp_ctrl() and
 * mtk_mmsys_mdp_camin_ctrl() set them (mt8183-mmsys.h): reset the link's async TX and RX,
 * take its relay out of no-SOF mode, and give the async and relay each tile's size.
 */
#define MMSYS_SW0_RST_B			0x140
#define MMSYS_SW1_RST_B			0x144
#define MDP_ASYNC_CFG_WD		0x934
#define MDP_ASYNC_IPU_CFG_WD		0x93c
#define ISP_RELAY_CFG_WD		0x994
#define IPU_RELAY_CFG_WD		0x9a0
#define RELAY_NO_SOF_MODE		BIT(31)

static int init_camin(struct mdp_comp_ctx *ctx, struct mdp_cmdq_cmd *cmd)
{
	phys_addr_t base = ctx->comp->reg_base;
	u8 subsys_id = ctx->comp->subsys_id;
	bool two = ctx->comp->public_id == MDP_COMP_CAMIN2;
	u32 tx = two ? BIT(4) : BIT(3), rx = two ? BIT(11) : BIT(10);

	cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + MMSYS_SW0_RST_B, 0x0, tx);
	cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + MMSYS_SW0_RST_B, tx, tx);
	cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + MMSYS_SW1_RST_B, 0x0, rx);
	cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + MMSYS_SW1_RST_B, rx, rx);
	cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + (two ? IPU_RELAY_CFG_WD : ISP_RELAY_CFG_WD),
			    0x0, RELAY_NO_SOF_MODE);
	return 0;
}

static int config_camin_subfrm(struct mdp_comp_ctx *ctx, struct mdp_cmdq_cmd *cmd, u32 index)
{
	phys_addr_t base = ctx->comp->reg_base;
	u8 subsys_id = ctx->comp->subsys_id;
	bool two = ctx->comp->public_id == MDP_COMP_CAMIN2;
	u32 w = CFG_COMP(MT8183, ctx->param, subfrms[index].in.right) -
		CFG_COMP(MT8183, ctx->param, subfrms[index].in.left) + 1;
	u32 h = CFG_COMP(MT8183, ctx->param, subfrms[index].in.bottom) -
		CFG_COMP(MT8183, ctx->param, subfrms[index].in.top) + 1;

	cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + (two ? MDP_ASYNC_IPU_CFG_WD : MDP_ASYNC_CFG_WD),
			    (h << 16) | w, 0x3fff3fff);
	cmdq_pkt_write_mask(&cmd->pkt, subsys_id, base + (two ? IPU_RELAY_CFG_WD : ISP_RELAY_CFG_WD),
			    (h << 16) | w, 0x3fff3fff);
	return 0;
}

static const struct mdp_comp_ops camin_ops = {
	.get_comp_flag = get_comp_flag,
	.init_comp = init_camin,
	.config_subfrm = config_camin_subfrm,
};
""".replace("MARK", MARK)

edit("mtk-mdp3-comp.c", [
    ("static const struct mdp_comp_ops *mdp_comp_ops[MDP_COMP_TYPE_COUNT] = {\n",
     OPS + "\nstatic const struct mdp_comp_ops *mdp_comp_ops[MDP_COMP_TYPE_COUNT] = {\n"
     "\t[MDP_COMP_TYPE_IMGI] =\t\t&imgi_ops,\n"
     "\t[MDP_COMP_TYPE_DL_PATH] =\t&camin_ops,\n"),
    ("""		.compatible = "mediatek,mt8183-mdp3-rdma",
		.data = (void *)MDP_COMP_TYPE_RDMA,""",
     """		.compatible = "mediatek,mt8183-mdp3-rdma",
		.data = (void *)MDP_COMP_TYPE_RDMA,
	}, {
		/* %s */
		.compatible = "mediatek,mt8183-mdp3-imgi",
		.data = (void *)MDP_COMP_TYPE_IMGI,
	}, {
		.compatible = "mediatek,mt8183-mdp3-dl",
		.data = (void *)MDP_COMP_TYPE_DL_PATH,""" % MARK),
])

# Components with no mutex module (the ISP head, the direct links, the PATH sout muxes; ChromeOS 5.10
# adds none for them) have no entry in mt8183_mutex_idx, so mainline's lookup gives them entry 0,
# MDP_RDMA0, and starts an idle RDMA0 with the job. Mainline only ever sets mod bits (by CPU), so an
# ISP job also clears the RDMA0 bit a previous run may have left in its mutex.
edit("mtk-mdp3-cmdq.c", [
    ("""	mutex = __get_mutex(path->mdp_dev, p);

	/* Set mutex mod */
	for (index = 0; index < num_comp; index++) {""",
     """	mutex = __get_mutex(path->mdp_dev, p);
	if (path->comps[0].comp->type == MDP_COMP_TYPE_IMGI)	/* %s */
		mtk_mutex_write_mod(mutex, MUTEX_MOD_IDX_MDP_RDMA0, true);

	/* Set mutex mod */
	for (index = 0; index < num_comp; index++) {""" % MARK),
    ("""		mutex_idx = data->mdp_mutex_table_idx;
		id = ctx->comp->public_id;
		mtk_mutex_write_mod(mutex, mutex_idx[id], false);""",
     """		mutex_idx = data->mdp_mutex_table_idx;
		id = ctx->comp->public_id;
		if (mutex_idx[id] == MUTEX_MOD_IDX_MDP_RDMA0 && id != MDP_COMP_RDMA0)
			continue;	/* %s: no mutex module */
		mtk_mutex_write_mod(mutex, mutex_idx[id], false);""" % MARK),
])

# The GCE's power. Since the mailbox stopped taking runtime PM in its send_data (it can run atomic),
# each client resumes the GCE around its packets, as mtk_crtc.c does; mainline's MDP3 never does, so
# its packets only run while something else (the display) happens to hold the GCE awake. Asleep,
# the GCE's clock is off, the thread is never started and the callback never comes.
edit("mtk-mdp3-cmdq.c", [
    ("""#include <linux/mailbox_controller.h>
#include <linux/platform_device.h>
""",
     """#include <linux/mailbox_controller.h>
#include <linux/platform_device.h>
#include <linux/pm_runtime.h>
"""),
    ("""	data = (struct cmdq_cb_data *)mssg;
	cmd = container_of(data->pkt, struct mdp_cmdq_cmd, pkt);""",
     """	data = (struct cmdq_cb_data *)mssg;
	cmd = container_of(data->pkt, struct mdp_cmdq_cmd, pkt);
	/* %s: the GCE may sleep again */
	pm_runtime_mark_last_busy(cmd->mdp->cmdq_clt[cmd->pp_idx]->chan->mbox->dev);
	pm_runtime_put_autosuspend(cmd->mdp->cmdq_clt[cmd->pp_idx]->chan->mbox->dev);
""" % MARK),
    ("""		dma_sync_single_for_device(mdp->cmdq_clt[i]->chan->mbox->dev,
					   cmd[i]->pkt.pa_base, cmd[i]->pkt.cmd_buf_size,
					   DMA_TO_DEVICE);

		ret = mbox_send_message(mdp->cmdq_clt[i]->chan, &cmd[i]->pkt);
		if (ret < 0) {
			dev_err(dev, "mbox send message fail %d!\\n", ret);""",
     """		struct device *gce = mdp->cmdq_clt[i]->chan->mbox->dev;

		dma_sync_single_for_device(mdp->cmdq_clt[i]->chan->mbox->dev,
					   cmd[i]->pkt.pa_base, cmd[i]->pkt.cmd_buf_size,
					   DMA_TO_DEVICE);

		ret = pm_runtime_resume_and_get(gce);	/* %s */
		if (ret < 0) {
			dev_err(dev, "the GCE did not wake (%%d)\\n", ret);
			i = pp_used;
			goto err_clock_off;
		}
		ret = mbox_send_message(mdp->cmdq_clt[i]->chan, &cmd[i]->pkt);
		if (ret < 0) {
			pm_runtime_put_autosuspend(gce);
			dev_err(dev, "mbox send message fail %%d!\\n", ret);""" % MARK),
])

# A job that never finishes (the DIP waits on a done event that does not come): its client aborts
# the packet. mbox_flush drops what the GCE thread is running; the callback then comes with
# -ECONNABORTED and the job is released as any other.
edit("mtk-mdp3-cmdq.c", [
    ("""EXPORT_SYMBOL_GPL(mdp_cmdq_send);""",
     """EXPORT_SYMBOL_GPL(mdp_cmdq_send);

void mdp_cmdq_abort(struct mdp_dev *mdp)
{
	struct mbox_chan *chan = mdp->cmdq_clt[0]->chan;
	struct device *gce = chan->mbox->dev;
	int ret;

	if (pm_runtime_resume_and_get(gce) < 0)
		return;
	ret = mbox_flush(chan, 2000);
	if (ret)
		dev_err(&mdp->pdev->dev, "the GCE did not drop the job (%d)\\n", ret);
	pm_runtime_mark_last_busy(gce);
	pm_runtime_put_autosuspend(gce);
}
EXPORT_SYMBOL_GPL(mdp_cmdq_abort);"""),
])
edit("mtk-mdp3-cmdq.h", [
    ("""int mdp_cmdq_send(struct mdp_dev *mdp, struct mdp_cmdq_param *param);""",
     """int mdp_cmdq_send(struct mdp_dev *mdp, struct mdp_cmdq_param *param);
void mdp_cmdq_abort(struct mdp_dev *mdp);	/* athena: a stuck ISP job */"""),
])

# The callback's status is kept in the command: the cmdq_cb_data it points at lives on the mailbox
# driver's stack and is gone by the time the release work reads it (mainline reads garbage).
edit("mtk-mdp3-cmdq.h", [
    ("""	struct cmdq_cb_data *data;""",
     """	struct cmdq_cb_data *data;
	int sta;	/* %s: data->sta, read in the callback */""" % MARK),
])
edit("mtk-mdp3-cmdq.c", [
    ("""	cmd->data = data;""",
     """	cmd->data = data;
	cmd->sta = data->sta;"""),
    ("""			user_cb_data.sta = cmd->data->sta;
			user_cb_data.pkt = cmd->data->pkt;""",
     """			user_cb_data.sta = cmd->sta;
			user_cb_data.pkt = &cmd->pkt;"""),
])

# Their clocks and registers in their own nodes
edit("mdp_cfg_data.c", [
    ("""		{MDP_COMP_TYPE_IMGI, 0, MT8183_MDP_COMP_ISP_IMGI, MDP_MM_SUBSYS_0},
		{0, 0, 4}""", """		{MDP_COMP_TYPE_IMGI, 0, MT8183_MDP_COMP_ISP_IMGI, MDP_MM_SUBSYS_0},
		{0, 0, 0}"""),
    ("""		{MDP_COMP_TYPE_DL_PATH, 0, MT8183_MDP_COMP_CAMIN, MDP_MM_SUBSYS_0},
		{2, 2, 1}""", """		{MDP_COMP_TYPE_DL_PATH, 0, MT8183_MDP_COMP_CAMIN, MDP_MM_SUBSYS_0},
		{2, 0, 0}"""),
    ("""		{MDP_COMP_TYPE_DL_PATH, 1, MT8183_MDP_COMP_CAMIN2, MDP_MM_SUBSYS_0},
		{2, 4, 1}""", """		{MDP_COMP_TYPE_DL_PATH, 1, MT8183_MDP_COMP_CAMIN2, MDP_MM_SUBSYS_0},
		{2, 0, 0}"""),
])
print("patched")
