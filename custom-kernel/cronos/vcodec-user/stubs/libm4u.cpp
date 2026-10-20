/*
 * Replacement for MediaTek libm4u.so (/proc/m4u client). Only the imports of
 * libvcodec_utility.so are provided. The C++ declarations are chosen so that
 * the compiler emits exactly the vendor mangled names (so the parameter types
 * are checked by the dynamic linker):
 *
 *   _ZN9MTKM4UDrvC1Ev / D1Ev
 *   _ZN9MTKM4UDrv13m4u_alloc_mvaEimjjjPj
 *   _ZN9MTKM4UDrv14m4u_cache_syncEi19M4U_CACHE_SYNC_ENUMmjj
 *   _ZN9MTKM4UDrv15m4u_config_portEP9_M4U_PORT
 *   _ZN9MTKM4UDrv15m4u_dealloc_mvaEimjj
 *   _ZN9MTKM4UDrv18m4u_port_array_addEP14m4u_port_arrayiii
 *   _ZN9MTKM4UDrv19m4u_port_array_initEP14m4u_port_array
 *   _ZN9MTKM4UDrv20m4u_insert_tlb_rangeEijj23M4U_RANGE_PRIORITY_ENUMj
 *   _ZN9MTKM4UDrv21m4u_config_port_arrayEP14m4u_port_array
 *   _ZN9MTKM4UDrv21m4u_invalid_tlb_rangeEijj
 *   _ZN9MTKM4UDrv23m4u_manual_insert_entryEijb
 *
 * Semantics on mainline: ports are switched to IOMMU ("virtual") mode by the
 * kernel (the larb/iommu DT bindings), so port configuration is a no-op that
 * reports success. m4u_alloc_mva() (used only by eVideoAllocMVA() and
 * eVideoMp4EncAllocMVA(), i.e. OMX/MPEG4 paths) can only "map" memory that
 * is already one of our buffers: it returns the IOVA of that VA.
 * libvcodec_utility does `new MTKM4UDrv` with sizeof == 4, so the object must
 * stay 4 bytes.
 */
#include "vcshim.h"

enum M4U_CACHE_SYNC_ENUM { M4U_CACHE_SYNC_DUMMY };
enum M4U_RANGE_PRIORITY_ENUM { M4U_RANGE_PRIORITY_DUMMY };
struct _M4U_PORT;
struct m4u_port_array;

class VCSHIM_EXPORT MTKM4UDrv {
public:
	MTKM4UDrv();
	~MTKM4UDrv();
	int m4u_alloc_mva(int port, unsigned long va, unsigned int size,
			  unsigned int prot, unsigned int flags, unsigned int *mva);
	int m4u_cache_sync(int port, M4U_CACHE_SYNC_ENUM type, unsigned long va,
			   unsigned int size, unsigned int mva);
	int m4u_config_port(_M4U_PORT *port);
	int m4u_dealloc_mva(int port, unsigned long va, unsigned int size, unsigned int mva);
	int m4u_port_array_add(m4u_port_array *arr, int port, int virt, int sec);
	int m4u_port_array_init(m4u_port_array *arr);
	int m4u_insert_tlb_range(int port, unsigned int start, unsigned int end,
				 M4U_RANGE_PRIORITY_ENUM prio, unsigned int entries);
	int m4u_config_port_array(m4u_port_array *arr);
	int m4u_invalid_tlb_range(int port, unsigned int start, unsigned int end);
	int m4u_manual_insert_entry(int port, unsigned int mva, bool lock);
private:
	int mFd; /* keeps sizeof == 4 */
};

static_assert(sizeof(MTKM4UDrv) == 4, "vendor code allocates 4 bytes");

MTKM4UDrv::MTKM4UDrv() : mFd(-1) {}
MTKM4UDrv::~MTKM4UDrv() { mFd = -1; }

int MTKM4UDrv::m4u_alloc_mva(int port, unsigned long va, unsigned int size,
			     unsigned int, unsigned int, unsigned int *mva)
{
	uint32_t iova;
	if (vcshim_find_va(va, &iova) < 0) {
		VCS_ERR("m4u_alloc_mva(port %d, va 0x%lx, 0x%x): VA is not a vcshim buffer",
			port, va, size);
		return -1;
	}
	*mva = iova;
	VCS_DBG("m4u_alloc_mva(port %d, va 0x%lx, 0x%x) -> 0x%08x", port, va, size, iova);
	return 0;
}

int MTKM4UDrv::m4u_cache_sync(int, M4U_CACHE_SYNC_ENUM, unsigned long va, unsigned int, unsigned int)
{
	int h = vcshim_find_va(va, nullptr);
	vcshim_sync(h > 0 ? h : 0, 3);
	return 0;
}

int MTKM4UDrv::m4u_config_port(_M4U_PORT *) { return 0; }
int MTKM4UDrv::m4u_dealloc_mva(int, unsigned long, unsigned int, unsigned int) { return 0; }
/* the caller's array may be smaller than the vendor's: do not touch it */
int MTKM4UDrv::m4u_port_array_add(m4u_port_array *, int, int, int) { return 0; }
int MTKM4UDrv::m4u_port_array_init(m4u_port_array *) { return 0; }
int MTKM4UDrv::m4u_insert_tlb_range(int, unsigned int, unsigned int, M4U_RANGE_PRIORITY_ENUM, unsigned int) { return 0; }
int MTKM4UDrv::m4u_config_port_array(m4u_port_array *) { return 0; }
int MTKM4UDrv::m4u_invalid_tlb_range(int, unsigned int, unsigned int) { return 0; }
int MTKM4UDrv::m4u_manual_insert_entry(int, unsigned int, bool) { return 0; }
