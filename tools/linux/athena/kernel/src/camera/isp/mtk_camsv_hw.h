/* SPDX-License-Identifier: GPL-2.0 */
/*
 * Copyright (c) 2020 BayLibre
 * Copyright (c) 2022 MediaTek Inc.
 */

#ifndef __MTK_CAMSV_HW_H__
#define __MTK_CAMSV_HW_H__

void mtk_camsv_setup(struct mtk_cam_dev *cam_dev, unsigned int cont,
		     u32 width, u32 height, u32 bpl, u32 mbus_fmt);
void mtk_camsv_update_buffers_add(struct mtk_cam_dev *cam_dev,
				  struct mtk_cam_dev_buffer *buf);
void mtk_camsv_cmos_vf_hw_enable(struct mtk_cam_dev *cam_dev);
void mtk_camsv_cmos_vf_hw_disable(struct mtk_cam_dev *cam_dev);
void mtk_camsv_fbc_init(struct mtk_cam_dev *cam_dev, unsigned int num_buffers);
void mtk_camsv_fbc_inc(struct mtk_cam_dev *cam_dev);
int mtk_camsv_scratch_alloc(struct mtk_cam_dev *cam_dev, size_t size);
void mtk_camsv_scratch_free(struct mtk_cam_dev *cam_dev);

#endif /* __MTK_CAMSV_HW_H__ */
