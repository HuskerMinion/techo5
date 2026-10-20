/*
 * Replacement for MediaTek libbwc.so (/dev/MTK_SMI bandwidth "scenarios").
 * libvcodec_utility (eHalEMICtrl / eHalEMICtrlForRecordSize) imports:
 *   _ZN3BWC14Profile_ChangeE16BWC_PROFILE_TYPEb
 *   _ZN3BWC19VideoRecordSize_SetERK8BWC_SIZE
 *   _ZN3BWC20VideoDecodeCodec_SetE15BWC_VCODEC_TYPE
 *   _ZN3BWC20VideoEncodeCodec_SetE15BWC_VCODEC_TYPE
 * It calls them on a BWC object on its own stack ("this" is never
 * dereferenced). On mainline, SMI/EMI bandwidth is handled by the
 * mtk-smi driver/interconnect, so these succeed and do nothing.
 */
#define EXPORT __attribute__((visibility("default")))

enum BWC_PROFILE_TYPE { BWC_PROFILE_DUMMY };
enum BWC_VCODEC_TYPE { BWC_VCODEC_DUMMY };
struct BWC_SIZE { long w, h; };

class EXPORT BWC {
public:
	int Profile_Change(BWC_PROFILE_TYPE profile, bool on);
	int VideoRecordSize_Set(const BWC_SIZE &size);
	int VideoDecodeCodec_Set(BWC_VCODEC_TYPE type);
	int VideoEncodeCodec_Set(BWC_VCODEC_TYPE type);
};

int BWC::Profile_Change(BWC_PROFILE_TYPE, bool) { return 0; }
int BWC::VideoRecordSize_Set(const BWC_SIZE &) { return 0; }
int BWC::VideoDecodeCodec_Set(BWC_VCODEC_TYPE) { return 0; }
int BWC::VideoEncodeCodec_Set(BWC_VCODEC_TYPE) { return 0; }
