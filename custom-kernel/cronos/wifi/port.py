#!/usr/bin/env python3
"""Port the Amazon gen4 MT7668 driver's Linux glue to the 7.0 kernel API. Edits wifi-work in place;
every substitution asserts it matched so a silent miss cannot slip through."""
import re, sys, os
os.chdir(sys.argv[1])

def edit(path, pairs, count=None):
    s = open(path).read()
    for old, new in pairs:
        if isinstance(old, re.Pattern):
            s, n = old.subn(new if callable(new) else new, s)
        else:
            n = s.count(old); s = s.replace(old, new)
        assert n >= 1, '%s: no match for %r' % (path, old.pattern if isinstance(old, re.Pattern) else old[:60])
    open(path, 'w').write(s)

R = re.compile

# --- headers the kernel no longer provides ---------------------------------------------------
edit('common/wlan_oid.c', [('#include <stddef.h>', '#include <linux/stddef.h>')])
for p in ('os/linux/gl_kal.c', 'os/linux/gl_wext_priv.c', 'os/linux/gl_init.c', 'os/linux/gl_p2p_kal.c'):
    s = open(p).read()
    s = s.replace('#include <limits.h>', '#include <linux/limits.h>')
    open(p, 'w').write(s)
edit('os/linux/include/gl_kal.h', [
    ('#define KAL_GET_HOST_CLOCK()\t\tlocal_clock()', '#include <linux/sched/clock.h>\n#define KAL_GET_HOST_CLOCK()\t\tlocal_clock()'),
    ('VOID kalTimeoutHandler(unsigned long arg);', 'VOID kalTimeoutHandler(struct timer_list *t);'),
])

# --- module macros ----------------------------------------------------------------------------
edit('os/linux/gl_init.c', [('MODULE_SUPPORTED_DEVICE(NIC_NAME);\n', '')])

# --- access_ok lost its VERIFY_* argument (5.0) -----------------------------------------------
edit('os/linux/gl_wext_priv.c', [(R(r'access_ok\(VERIFY_(READ|WRITE), '), 'access_ok(')])

# --- wireless_dev: current_bss and cac_started moved into links[] (6.0/6.5), mtx is gone (6.5) ----
edit('mgmt/saa_fsm.c', [('wdev->current_bss', 'wdev->connected')])
edit('os/linux/gl_p2p_kal.c', [
    ('->prWdev->cac_started = FALSE;', '->prWdev->links[0].cac_started = FALSE;'),
    (R(r'cfg80211_cac_event\((prGlueInfo->prP2PInfo\[ucRoleIndex\]->prDevHandler,\s*prGlueInfo->prP2PInfo\[ucRoleIndex\]->chandef, NL80211_RADAR_CAC_FINISHED, GFP_KERNEL)\)'),
     r'cfg80211_cac_event(\1, 0)'),
])
edit('os/linux/gl_kal.c', [
    ('mutex_lock(&(pDev->ieee80211_ptr)->mtx);', 'wiphy_lock(pDev->ieee80211_ptr->wiphy);'),
    ('mutex_unlock(&(pDev->ieee80211_ptr)->mtx);', 'wiphy_unlock(pDev->ieee80211_ptr->wiphy);'),
    ('\tprNetDev->last_rx = jiffies;\n', ''),
    ('netif_rx_ni(prSkb);', 'netif_rx(prSkb);'),
])

# --- channel switch notifications carry a link id (5.19+) -------------------------------------
edit('mgmt/p2p_func.c', [(R(r'(cfg80211_ch_switch_notify\([^;]*?->chandef)\);'), r'\1, 0);')])
edit('os/linux/gl_kal.c', [('cfg80211_ch_switch_notify(prGlueInfo->prDevHandler, &chandef);',
                            'cfg80211_ch_switch_notify(prGlueInfo->prDevHandler, &chandef, 0);')])

# --- net_device destructor (4.12) -------------------------------------------------------------
edit('os/linux/gl_p2p_cfg80211.c', [
    ('prNewNetDevice->destructor = mtk_vif_destructor;',
     'prNewNetDevice->priv_destructor = mtk_vif_destructor;\n\t\tprNewNetDevice->needs_free_netdev = false;'),
])

# --- procfs: file_operations -> proc_ops (5.6) ------------------------------------------------
def proc_ops(path):
    s = open(path).read()
    def conv(m):
        body = m.group(2)
        body = re.sub(r'\s*\.owner\s*=\s*THIS_MODULE,', '', body)
        for a, b in (('.read', '.proc_read'), ('.write', '.proc_write'), ('.open', '.proc_open'),
                     ('.release', '.proc_release'), ('.llseek', '.proc_lseek')):
            body = re.sub(r'\.' + a[1:] + r'\b', b, body)
        return 'struct proc_ops %s = {%s};' % (m.group(1), body)
    s, n = re.subn(r'struct file_operations (\w+) = \{(.*?)\};', conv, s, flags=re.S)
    assert n, path
    open(path, 'w').write(s)
proc_ops('os/linux/gl_proc.c')
proc_ops('os/linux/gl_kal.c')

# --- file IO without set_fs (5.10) ------------------------------------------------------------
edit('os/linux/platform.c', [
    (R(r'static int nvram_read\(char \*filename, char \*buf, ssize_t len, int offset\)\n\{\n#if CFG_SUPPORT_NVRAM\n.*?\n#else /\* !CFG_SUPPORT_NVRAM \*/', re.S),
     lambda m: '''static int nvram_read(char *filename, char *buf, ssize_t len, int offset)
{
#if CFG_SUPPORT_NVRAM
	struct file *fd;
	loff_t pos = offset;
	int retLen;

	fd = filp_open(filename, O_RDONLY, 0644);
	if (IS_ERR(fd)) {
		DBGLOG(INIT, INFO, "[nvram_read] : failed to open!!\\n");
		return -1;
	}
	retLen = kernel_read(fd, buf, len, &pos);
	filp_close(fd, NULL);
	return retLen;

#else /* !CFG_SUPPORT_NVRAM */'''),
    (R(r'static int nvram_write\(char \*filename, char \*buf, ssize_t len, int offset\)\n\{\n#if CFG_SUPPORT_NVRAM\n.*?\n#else /\* !CFG_SUPPORT_NVRAMS \*/', re.S),
     lambda m: '''static int nvram_write(char *filename, char *buf, ssize_t len, int offset)
{
#if CFG_SUPPORT_NVRAM
	struct file *fd;
	loff_t pos = offset;
	int retLen;

	fd = filp_open(filename, O_WRONLY | O_CREAT, 0644);
	if (IS_ERR(fd)) {
		DBGLOG(INIT, INFO, "[nvram_write] : failed to open!!\\n");
		return -1;
	}
	retLen = kernel_write(fd, buf, len, &pos);
	filp_close(fd, NULL);
	return retLen;

#else /* !CFG_SUPPORT_NVRAMS */'''),
])
edit('os/linux/gl_kal.c', [
    (R(r'struct file \*kalFileOpen\(const char \*path, int flags, int rights\)\n\{.*?\n\}\n', re.S),
     '''struct file *kalFileOpen(const char *path, int flags, int rights)
{
	struct file *filp = filp_open(path, flags, rights);

	if (IS_ERR(filp))
		return NULL;
	return filp;
}
'''),
    (R(r'UINT_32 kalFileRead\(struct file \*file, unsigned long long offset, unsigned char \*data, unsigned int size\)\n\{.*?\n\}\n', re.S),
     '''UINT_32 kalFileRead(struct file *file, unsigned long long offset, unsigned char *data, unsigned int size)
{
	loff_t pos = offset;

	return kernel_read(file, data, size, &pos);
}
'''),
    (R(r'UINT_32 kalFileWrite\(struct file \*file, unsigned long long offset, unsigned char \*data, unsigned int size\)\n\{.*?\n\}\n', re.S),
     '''UINT_32 kalFileWrite(struct file *file, unsigned long long offset, unsigned char *data, unsigned int size)
{
	loff_t pos = offset;

	return kernel_write(file, data, size, &pos);
}
'''),
])

# --- timers: timer_setup / timer_container_of (4.15), timer_delete_sync (6.2) -----------------
edit('os/linux/gl_kal.c', [
    ('''	init_timer(&(prGlueInfo->tickfn));
	prGlueInfo->tickfn.function = prTimerHandler;
	prGlueInfo->tickfn.data = (unsigned long)prGlueInfo;''',
     '''	timer_setup(&prGlueInfo->tickfn, (void (*)(struct timer_list *))prTimerHandler, 0);'''),
    ('del_timer_sync(&(prGlueInfo->tickfn))', 'timer_delete_sync(&(prGlueInfo->tickfn))'),
    ('''VOID kalTimeoutHandler(unsigned long arg)
{

	P_GLUE_INFO_T prGlueInfo = (P_GLUE_INFO_T) arg;''',
     '''VOID kalTimeoutHandler(struct timer_list *t)
{

	P_GLUE_INFO_T prGlueInfo = timer_container_of(prGlueInfo, t, tickfn);'''),
])
edit('os/linux/gl_init.c', [
    ('''				init_timer(&prAdapter->rTxDirectSkbTimer);
				prAdapter->rTxDirectSkbTimer.data = (unsigned long)prGlueInfo;
				prAdapter->rTxDirectSkbTimer.function = nicTxDirectTimerCheckSkbQ;

				init_timer(&prAdapter->rTxDirectHifTimer);
				prAdapter->rTxDirectHifTimer.data = (unsigned long)prGlueInfo;
				prAdapter->rTxDirectHifTimer.function = nicTxDirectTimerCheckHifQ;''',
     '''				timer_setup(&prAdapter->rTxDirectSkbTimer, nicTxDirectTimerCheckSkbQ, 0);
				timer_setup(&prAdapter->rTxDirectHifTimer, nicTxDirectTimerCheckHifQ, 0);'''),
    ('del_timer_sync(&prAdapter->rTxDirectSkbTimer);', 'timer_delete_sync(&prAdapter->rTxDirectSkbTimer);'),
    ('del_timer_sync(&prAdapter->rTxDirectHifTimer);', 'timer_delete_sync(&prAdapter->rTxDirectHifTimer);'),
])
edit('nic/nic_tx.c', [
    ('''void nicTxDirectTimerCheckSkbQ(unsigned long data)
{
	P_GLUE_INFO_T prGlueInfo = (P_GLUE_INFO_T)data;
	P_ADAPTER_T prAdapter = prGlueInfo->prAdapter;''',
     '''void nicTxDirectTimerCheckSkbQ(struct timer_list *t)
{
	P_ADAPTER_T prAdapter = timer_container_of(prAdapter, t, rTxDirectSkbTimer);
	P_GLUE_INFO_T prGlueInfo = prAdapter->prGlueInfo;'''),
    ('''void nicTxDirectTimerCheckHifQ(unsigned long data)
{
	P_GLUE_INFO_T prGlueInfo = (P_GLUE_INFO_T)data;
	P_ADAPTER_T prAdapter = prGlueInfo->prAdapter;''',
     '''void nicTxDirectTimerCheckHifQ(struct timer_list *t)
{
	P_ADAPTER_T prAdapter = timer_container_of(prAdapter, t, rTxDirectHifTimer);
	P_GLUE_INFO_T prGlueInfo = prAdapter->prGlueInfo;'''),
])
edit('include/nic/nic_tx.h', [
    ('void nicTxDirectTimerCheckSkbQ(unsigned long data);', 'void nicTxDirectTimerCheckSkbQ(struct timer_list *t);'),
    ('void nicTxDirectTimerCheckHifQ(unsigned long data);', 'void nicTxDirectTimerCheckHifQ(struct timer_list *t);'),
])

# --- time -------------------------------------------------------------------------------------
edit('os/linux/gl_kal.c', [
    ('''	struct timespec ts;
	UINT_64 bootTime = 0;

#if KERNEL_VERSION(2, 6, 39) <= LINUX_VERSION_CODE
	get_monotonic_boottime(&ts);
#else
	ts = ktime_to_timespec(ktime_get());
#endif''',
     '''	struct timespec64 ts;
	UINT_64 bootTime = 0;

	ktime_get_boottime_ts64(&ts);'''),
])

# --- cfg80211 events --------------------------------------------------------------------------
edit('os/linux/gl_kal.c', [
    ('''				cfg80211_roamed(prGlueInfo->prDevHandler,
						prChannel,
						arBssid,
						prGlueInfo->aucReqIe,
						prGlueInfo->u4ReqIeLength,
						prGlueInfo->aucRspIe, prGlueInfo->u4RspIeLength, GFP_KERNEL);''',
     '''				{
					struct cfg80211_roam_info rRoamInfo = {};

					rRoamInfo.links[0].channel = prChannel;
					rRoamInfo.links[0].bssid = arBssid;
					rRoamInfo.req_ie = prGlueInfo->aucReqIe;
					rRoamInfo.req_ie_len = prGlueInfo->u4ReqIeLength;
					rRoamInfo.resp_ie = prGlueInfo->aucRspIe;
					rRoamInfo.resp_ie_len = prGlueInfo->u4RspIeLength;
					cfg80211_roamed(prGlueInfo->prDevHandler, &rRoamInfo, GFP_KERNEL);
				}'''),
    ('cfg80211_sched_scan_results(priv_to_wiphy(prGlueInfo));', 'cfg80211_sched_scan_results(priv_to_wiphy(prGlueInfo), 0);'),
])
edit('os/linux/gl_init.c', [
    ('cfg80211_sched_scan_stopped(priv_to_wiphy(prGlueInfo));', 'cfg80211_sched_scan_stopped(priv_to_wiphy(prGlueInfo), 0);'),
    ('''#if (KERNEL_VERSION(3, 18, 0) <= CFG80211_VERSION_CODE)
					cfg80211_rx_assoc_resp(prParamWDevLock->pDev,
								prParamWDevLock->pBss,
								prParamWDevLock->pFrameBuf,
								prParamWDevLock->frameLen,
								prParamWDevLock->uapsd_queues);
#elif''',
     '''#if (KERNEL_VERSION(3, 18, 0) <= CFG80211_VERSION_CODE)
					{
						struct cfg80211_rx_assoc_resp_data rResp = {};

						rResp.buf = prParamWDevLock->pFrameBuf;
						rResp.len = prParamWDevLock->frameLen;
						rResp.uapsd_queues = prParamWDevLock->uapsd_queues;
						rResp.links[0].bss = prParamWDevLock->pBss;
						cfg80211_rx_assoc_resp(prParamWDevLock->pDev, &rResp);
					}
#elif'''),
    ('''					cfg80211_tx_mlme_mgmt(prParamWDevLock->pDev,
								prParamWDevLock->pFrameBuf,
								prParamWDevLock->frameLen);''',
     '''					cfg80211_tx_mlme_mgmt(prParamWDevLock->pDev,
								prParamWDevLock->pFrameBuf,
								prParamWDevLock->frameLen, false);'''),
    ('''#if (KERNEL_VERSION(4, 4, 41) <= CFG80211_VERSION_CODE)
					cfg80211_abandon_assoc(prParamWDevLock->pDev,
								prParamWDevLock->pBss);
					break;
#endif''',
     '''					{
						struct cfg80211_assoc_failure rFail = {};

						rFail.bss[0] = prParamWDevLock->pBss;
						cfg80211_assoc_failure(prParamWDevLock->pDev, &rFail);
					}
					break;'''),
    ('''#if (KERNEL_VERSION(3, 11, 0) <= CFG80211_VERSION_CODE)
					cfg80211_assoc_timeout(prParamWDevLock->pDev,
								prParamWDevLock->pBss);
#else''',
     '''#if (KERNEL_VERSION(3, 11, 0) <= CFG80211_VERSION_CODE)
					{
						struct cfg80211_assoc_failure rFail = {};

						rFail.bss[0] = prParamWDevLock->pBss;
						rFail.timeout = true;
						cfg80211_assoc_failure(prParamWDevLock->pDev, &rFail);
					}
#else'''),
])

# --- ndo_select_queue (4.19) ------------------------------------------------------------------
edit('os/linux/gl_init.c', [
    ('''u16 wlanSelectQueue(struct net_device *dev, struct sk_buff *skb,
		    void *accel_priv, select_queue_fallback_t fallback)''',
     '''u16 wlanSelectQueue(struct net_device *dev, struct sk_buff *skb,
		    struct net_device *sb_dev)'''),
])
edit('os/linux/include/gl_os.h', [
    ('''u16 wlanSelectQueue(struct net_device *dev, struct sk_buff *skb,
		    void *accel_priv, select_queue_fallback_t fallback);''',
     '''u16 wlanSelectQueue(struct net_device *dev, struct sk_buff *skb,
		    struct net_device *sb_dev);'''),
])

# --- cfg80211_ops: link ids, radio index, dropped flags (4.12 .. 6.13) ------------------------
# STA: keys gain link_id; change_virtual_intf loses flags; mgmt_frame_register becomes a bitmap update.
edit('os/linux/gl_cfg80211.c', [
    (R(r'(mtk_cfg80211_add_key\(struct wiphy \*wiphy,\s*struct net_device \*ndev,\s*)u8 key_index'), r'\1int link_id, u8 key_index'),
    (R(r'(mtk_cfg80211_get_key\(struct wiphy \*wiphy,\s*struct net_device \*ndev,\s*)u8 key_index'), r'\1int link_id, u8 key_index'),
    ('int mtk_cfg80211_del_key(struct wiphy *wiphy, struct net_device *ndev, u8 key_index',
     'int mtk_cfg80211_del_key(struct wiphy *wiphy, struct net_device *ndev, int link_id, u8 key_index'),
    ('mtk_cfg80211_set_default_key(struct wiphy *wiphy, struct net_device *ndev, u8 key_index',
     'mtk_cfg80211_set_default_key(struct wiphy *wiphy, struct net_device *ndev, int link_id, u8 key_index'),
    ('struct net_device *ndev, enum nl80211_iftype type, u32 *flags, struct vif_params *params)',
     'struct net_device *ndev, enum nl80211_iftype type, struct vif_params *params)'),
    ('rScanRequest.arChnlInfoList[i].ucChnlBw = request->scan_width;', 'rScanRequest.arChnlInfoList[i].ucChnlBw = 0;'),
    (R(r'params->(supported_rates_len|supported_rates|ht_capa|vht_capa)\b'), r'params->link_sta_params.\1'),
])
edit('os/linux/include/gl_cfg80211.h', [
    (R(r'(mtk_cfg80211_add_key\(struct wiphy \*wiphy,\s*struct net_device \*ndev,\s*)u8 key_index'), r'\1int link_id, u8 key_index'),
    (R(r'(mtk_cfg80211_get_key\(struct wiphy \*wiphy,\s*struct net_device \*ndev,\s*)u8 key_index'), r'\1int link_id, u8 key_index'),
    ('mtk_cfg80211_del_key(struct wiphy *wiphy, struct net_device *ndev, u8 key_index',
     'mtk_cfg80211_del_key(struct wiphy *wiphy, struct net_device *ndev, int link_id, u8 key_index'),
    ('mtk_cfg80211_set_default_key(struct wiphy *wiphy, struct net_device *ndev, u8 key_index',
     'mtk_cfg80211_set_default_key(struct wiphy *wiphy, struct net_device *ndev, int link_id, u8 key_index'),
    ('struct net_device *ndev, enum nl80211_iftype type, u32 *flags, struct vif_params *params)',
     'struct net_device *ndev, enum nl80211_iftype type, struct vif_params *params)'),
    ('void mtk_cfg80211_mgmt_frame_register(IN struct wiphy *wiphy,',
     '''void mtk_cfg80211_update_mgmt_frame_registrations(struct wiphy *wiphy, struct wireless_dev *wdev,
						   struct mgmt_frame_regs *upd);
void mtk_cfg80211_mgmt_frame_register(IN struct wiphy *wiphy,'''),
])
# A bitmap-of-subtypes update replays the old per-frame-type registration for the two types the
# driver handles (probe request and action).
MGMT_WRAPPER = '''
void %(name)s_update_mgmt_frame_registrations(struct wiphy *wiphy, struct wireless_dev *wdev,
						   struct mgmt_frame_regs *upd)
{
	%(name)s_mgmt_frame_register(wiphy, wdev, MAC_FRAME_PROBE_REQ,
		!!(upd->interface_stypes & BIT(IEEE80211_STYPE_PROBE_REQ >> 4)));
	%(name)s_mgmt_frame_register(wiphy, wdev, MAC_FRAME_ACTION,
		!!(upd->interface_stypes & BIT(IEEE80211_STYPE_ACTION >> 4)));
}
'''
for path, name in (('os/linux/gl_cfg80211.c', 'mtk_cfg80211'), ('os/linux/gl_p2p_cfg80211.c', 'mtk_p2p_cfg80211')):
    s = open(path).read()
    s += MGMT_WRAPPER % {'name': name}
    open(path, 'w').write(s)
edit('os/linux/gl_init.c', [('.mgmt_frame_register = mtk_cfg80211_mgmt_frame_register,',
                             '.update_mgmt_frame_registrations = mtk_cfg80211_update_mgmt_frame_registrations,')])
edit('os/linux/gl_p2p.c', [('.mgmt_frame_register = mtk_p2p_cfg80211_mgmt_frame_register,',
                            '.update_mgmt_frame_registrations = mtk_p2p_cfg80211_update_mgmt_frame_registrations,')])

# P2P side.
edit('os/linux/gl_p2p_cfg80211.c', [
    (R(r'(enum nl80211_iftype type), u32 \*flags, (struct vif_params \*params\))'), r'\1, \2'),
    (R(r'(IN enum nl80211_iftype type), IN u32 \*flags, (IN struct vif_params \*params\))'), r'\1, \2'),
    ('int mtk_p2p_cfg80211_change_beacon(struct wiphy *wiphy, struct net_device *dev, struct cfg80211_beacon_data *info)\n{',
     'int mtk_p2p_cfg80211_change_beacon(struct wiphy *wiphy, struct net_device *dev, struct cfg80211_ap_update *update)\n{\n\tstruct cfg80211_beacon_data *info = &update->beacon;'),
    ('int mtk_p2p_cfg80211_stop_ap(struct wiphy *wiphy, struct net_device *dev)\n',
     'int mtk_p2p_cfg80211_stop_ap(struct wiphy *wiphy, struct net_device *dev, unsigned int link_id)\n'),
    ('int mtk_p2p_cfg80211_set_wiphy_params(struct wiphy *wiphy, u32 changed)\n',
     'int mtk_p2p_cfg80211_set_wiphy_params(struct wiphy *wiphy, int radio_idx, u32 changed)\n'),
    (R(r'(mtk_p2p_cfg80211_set_bitrate_mask\(IN struct wiphy \*wiphy,\s*IN struct net_device \*dev,\s*)IN const u8 \*peer'), r'\1unsigned int link_id, IN const u8 *peer'),
    (R(r'(int mtk_p2p_cfg80211_add_key\(struct wiphy \*wiphy,\s*struct net_device \*ndev,\s*)u8 key_index'), r'\1int link_id, u8 key_index'),
    (R(r'(int mtk_p2p_cfg80211_get_key\(struct wiphy \*wiphy,\s*struct net_device \*ndev,\s*)u8 key_index'), r'\1int link_id, u8 key_index'),
    (R(r'(int mtk_p2p_cfg80211_del_key\(struct wiphy \*wiphy,\s*struct net_device \*ndev, )u8 key_index'), r'\1int link_id, u8 key_index'),
    (R(r'(mtk_p2p_cfg80211_set_default_key\(struct wiphy \*wiphy,\s*struct net_device \*netdev, )u8 key_index'), r'\1int link_id, u8 key_index'),
    ('int mtk_p2p_cfg80211_set_mgmt_key(struct wiphy *wiphy, struct net_device *dev, u8 key_index)\n',
     'int mtk_p2p_cfg80211_set_mgmt_key(struct wiphy *wiphy, struct net_device *dev, int link_id, u8 key_index)\n'),
    (R(r'(int mtk_p2p_cfg80211_set_txpower\(struct wiphy \*wiphy,\s*struct wireless_dev \*wdev, )enum nl80211_tx_power_setting type'), r'\1int radio_idx, enum nl80211_tx_power_setting type'),
    ('int mtk_p2p_cfg80211_get_txpower(struct wiphy *wiphy, struct wireless_dev *wdev, int *dbm)\n',
     'int mtk_p2p_cfg80211_get_txpower(struct wiphy *wiphy, struct wireless_dev *wdev, int radio_idx, unsigned int link_id, int *dbm)\n'),
    ('''int mtk_p2p_cfg80211_start_radar_detection(struct wiphy *wiphy, struct net_device *dev,
					struct cfg80211_chan_def *chandef, unsigned int cac_time_ms)
{''',
     '''int mtk_p2p_cfg80211_start_radar_detection(struct wiphy *wiphy, struct net_device *dev,
					struct cfg80211_chan_def *chandef, u32 cac_time_ms, int link_id)
{'''),
])
edit('os/linux/include/gl_p2p_ioctl.h', [
    (R(r'(enum nl80211_iftype type), u32 \*flags, (struct vif_params \*params\))'), r'\1, \2'),
    (R(r'int mtk_p2p_cfg80211_change_beacon\(struct wiphy \*wiphy, struct net_device \*dev, struct cfg80211_beacon_data \*info\);'),
     'int mtk_p2p_cfg80211_change_beacon(struct wiphy *wiphy, struct net_device *dev, struct cfg80211_ap_update *update);'),
    ('int mtk_p2p_cfg80211_stop_ap(struct wiphy *wiphy, struct net_device *dev);',
     'int mtk_p2p_cfg80211_stop_ap(struct wiphy *wiphy, struct net_device *dev, unsigned int link_id);'),
    ('int mtk_p2p_cfg80211_set_wiphy_params(struct wiphy *wiphy, u32 changed);',
     'int mtk_p2p_cfg80211_set_wiphy_params(struct wiphy *wiphy, int radio_idx, u32 changed);'),
    (R(r'(mtk_p2p_cfg80211_set_bitrate_mask\(IN struct wiphy \*wiphy,\s*IN struct net_device \*dev,\s*)IN const u8 \*peer'), r'\1unsigned int link_id, IN const u8 *peer'),
    (R(r'(mtk_p2p_cfg80211_add_key\(struct wiphy \*wiphy,\s*struct net_device \*ndev,\s*)u8 key_index'), r'\1int link_id, u8 key_index'),
    (R(r'(mtk_p2p_cfg80211_get_key\(struct wiphy \*wiphy,\s*struct net_device \*ndev,\s*)u8 key_index'), r'\1int link_id, u8 key_index'),
    (R(r'(mtk_p2p_cfg80211_del_key\(struct wiphy \*wiphy,\s*struct net_device \*ndev, )u8 key_index'), r'\1int link_id, u8 key_index'),
    (R(r'(mtk_p2p_cfg80211_set_default_key\(struct wiphy \*wiphy,\s*struct net_device \*netdev, )u8 key_index'), r'\1int link_id, u8 key_index'),
    ('mtk_p2p_cfg80211_set_mgmt_key(struct wiphy *wiphy, struct net_device *dev, u8 key_index);',
     'mtk_p2p_cfg80211_set_mgmt_key(struct wiphy *wiphy, struct net_device *dev, int link_id, u8 key_index);'),
    (R(r'(mtk_p2p_cfg80211_set_txpower\(struct wiphy \*wiphy,\s*struct wireless_dev \*wdev, )enum nl80211_tx_power_setting type'), r'\1int radio_idx, enum nl80211_tx_power_setting type'),
    ('int mtk_p2p_cfg80211_get_txpower(struct wiphy *wiphy, struct wireless_dev *wdev, int *dbm);',
     'int mtk_p2p_cfg80211_get_txpower(struct wiphy *wiphy, struct wireless_dev *wdev, int radio_idx, unsigned int link_id, int *dbm);'),
    ('void mtk_p2p_cfg80211_mgmt_frame_register(IN struct wiphy *wiphy,',
     '''void mtk_p2p_cfg80211_update_mgmt_frame_registrations(struct wiphy *wiphy, struct wireless_dev *wdev,
						   struct mgmt_frame_regs *upd);
void mtk_p2p_cfg80211_mgmt_frame_register(IN struct wiphy *wiphy,'''),
])

# --- late additions: radar detection prototype, TDLS management link id -----------------------
edit('os/linux/include/gl_p2p_ioctl.h', [
    (R(r'(mtk_p2p_cfg80211_start_radar_detection\(struct wiphy \*wiphy,\s*struct net_device \*dev,\s*struct cfg80211_chan_def \*chandef,\s*)unsigned int cac_time_ms\);'),
     r'\1u32 cac_time_ms, int link_id);'),
])
for path in ('os/linux/gl_cfg80211.c', 'os/linux/include/gl_cfg80211.h'):
    edit(path, [
        (R(r'(mtk_cfg80211_tdls_mgmt\(struct wiphy \*wiphy, struct net_device \*dev,\s*const u8 \*peer, )u8 action_code, u8 dialog_token,(\s*u16 status_code, u32 peer_capability,\s*bool initiator)'),
         r'\1int link_id, u8 action_code, u8 dialog_token,\2'),
    ])

# --- sched_setscheduler is no longer exported (5.9): the kernel's own low FIFO priority instead ------
edit('os/linux/gl_init.c', [
    (R(r'sched_setscheduler\((prGlueInfo->(?:main|hif|rx)_thread),\s*prGlueInfo->prAdapter->rWifiVar\.ucThreadScheduling, &param\);'),
     r'sched_set_fifo_low(\1);'),
    ('\t\t\tstruct sched_param param = {.sched_priority = prGlueInfo->prAdapter->rWifiVar.ucThreadPriority\n\t\t\t};\n', ''),
])
print('port applied')

# --- nl80211 vendor commands need a netlink policy (5.3): cfg80211 refuses the wiphy otherwise ----
for path in ('os/linux/gl_init.c', 'os/linux/gl_p2p.c'):
    edit(path, [
        (R(r'(\.flags = WIPHY_VENDOR_CMD_NEED_WDEV \| WIPHY_VENDOR_CMD_NEED_NETDEV,\n(\s*)\.doit = )'),
         r'\1'),  # anchor check only
        (R(r'\.flags = WIPHY_VENDOR_CMD_NEED_WDEV \| WIPHY_VENDOR_CMD_NEED_NETDEV,\n(\s*)\.doit = (\w+)'),
         r'.flags = WIPHY_VENDOR_CMD_NEED_WDEV | WIPHY_VENDOR_CMD_NEED_NETDEV,\n\1.policy = VENDOR_CMD_RAW_DATA,\n\1.doit = \2'),
    ])
print('vendor command policies applied')

# --- the kernel applies its regdomain after our init-time parse, which then saw every channel disabled
#     and told the firmware "no channels"; when the core's regdomain arrives, parse and send again ------
edit('os/linux/gl_cfg80211.c', [
    ('''		else
			/* Change to same state or same country, ignore */
			return;''',
     '''		else if (pRequest->initiator == NL80211_REGDOM_SET_BY_CORE)
			/* Linux 7.0: the core's regdomain lands after the init-time parse, which saw every
			 * channel disabled and sent the firmware an empty list. Parse and send again. */
			DBGLOG(RLM, INFO, "core regdomain after init: parsing channels again\\n");
		else
			/* Change to same state or same country, ignore */
			return;'''),
])
print('regdomain re-parse applied')

# --- the unit's own MAC: Amazon's build reads /proc/idme/mac_addr with set_fs() (gone since 5.10);
#     on this kernel the bootloader's IDME area is in the device tree, /idme/mac_addr/value -----------
s = open('os/linux/gl_kal.c').read()
a = s.index('#ifdef CONFIG_IDME\n#ifdef CFG_SUPPORT_DUAL_CARD_DUAL_DRIVER')
b = s.index('#endif\n', s.index('bailout:', a)) + len('#endif\n')
s = s[:a] + '''#if 1 /* TECHO5: IDME from the device tree */
#include <linux/of.h>
#include <linux/hex.h>
/* The 12 hex digits the bootloader leaves in /idme/mac_addr/value, what 4.9's /proc/idme/mac_addr showed. */
static int idme_get_mac_addr(unsigned char *mac_addr, size_t addr_len)
{
	struct device_node *np;
	const char *v;
	int i, hi, lo;

	if (!mac_addr || addr_len < IFHWADDRLEN)
		return -1;
	np = of_find_node_by_path("/idme/mac_addr");
	if (!np) {
		DBGLOG(INIT, ERROR, "no /idme/mac_addr in the device tree\\n");
		return -1;
	}
	v = of_get_property(np, "value", NULL);
	of_node_put(np);
	if (!v || strlen(v) != IFHWADDRLEN * 2) {
		DBGLOG(INIT, ERROR, "bad /idme/mac_addr value\\n");
		return -1;
	}
	for (i = 0; i < IFHWADDRLEN; i++) {
		hi = hex_to_bin(v[i * 2]);
		lo = hex_to_bin(v[i * 2 + 1]);
		if (hi < 0 || lo < 0)
			return -1;
		mac_addr[i] = (hi << 4) | lo;
	}
	return 0;
}
#endif
''' + s[b:]
s = s.replace('#ifdef CONFIG_IDME\nstatic BOOLEAN g_fgIsIdmeMacAddrExist', '#if 1 /* TECHO5: IDME from the device tree */\nstatic BOOLEAN g_fgIsIdmeMacAddrExist')
s = s.replace('#ifdef CONFIG_IDME\n\tif (prMacAddr && 0 == idme_get_mac_addr(', '#if 1 /* TECHO5: IDME from the device tree */\n\tif (prMacAddr && 0 == idme_get_mac_addr(')
assert s.count('TECHO5: IDME from the device tree') == 3
# dev_addr is const since 5.17: go through eth_hw_addr_set()
s = s.replace('''	if (UNEQUAL_MAC_ADDR(prGlueInfo->prDevHandler->dev_addr, pucMacAddr))
		memcpy(prGlueInfo->prDevHandler->dev_addr, pucMacAddr, PARAM_MAC_ADDR_LEN);''',
'''	if (UNEQUAL_MAC_ADDR(prGlueInfo->prDevHandler->dev_addr, pucMacAddr))
		eth_hw_addr_set(prGlueInfo->prDevHandler, pucMacAddr);''')
open('os/linux/gl_kal.c', 'w').write(s)
edit('os/linux/gl_init.c', [
    ('kalMemCopy(prGlueInfo->prDevHandler->dev_addr, &MacAddr.sa_data, ETH_ALEN);',
     'eth_hw_addr_set(prGlueInfo->prDevHandler, (const u8 *)&MacAddr.sa_data);'),
])
edit('os/linux/gl_p2p.c', [
    ('kalMemCopy(prGlueInfo->prP2PInfo[i]->prDevHandler->dev_addr, rMacAddr, ETH_ALEN);',
     'eth_hw_addr_set(prGlueInfo->prP2PInfo[i]->prDevHandler, rMacAddr);'),
])
print('IDME MAC and eth_hw_addr_set applied')
