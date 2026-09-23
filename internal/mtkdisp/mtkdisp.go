//go:build linux

// Package mtkdisp drives the MediaTek display controller on the Echo Show 5 (MT8163, "cronos")
// the way Android's hardware composer does: a display session on /dev/mtk_disp_mgr, pixel
// buffers from the ION multimedia heap, and one overlay layer pointed at a buffer per frame.
//
// The kernel framebuffer (/dev/graphics/fb0) cannot be used on this device. The mtkfb driver
// exports a framebuffer with no memory behind it (smem_len 0), so mmap is refused however it is
// asked. The overlay path below is what actually reaches the panel.
//
// The userspace on this kernel is 32-bit, so every structure here follows the compat layouts in
// drivers/misc/mediatek/video/mt8163/videox/compat_mtk_disp_mgr.h and the ioctl numbers are
// built from those sizes. One detail is not in the header: the kernel's compat_s64/compat_u64 are
// 8-byte aligned here (arm64 kernel), so the structures carrying a timestamp are padded, which
// the sizes the driver accepts confirmed (168, 1432 and 24 bytes; see ScanIoctlSize). Sizes are
// checked once at Open so a layout slip fails loudly instead of as a silent -ENOTTY.
package mtkdisp

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	dispDev = "/dev/mtk_disp_mgr"
	ionDev  = "/dev/ion"

	sessionPrimary = 1 // DISP_SESSION_PRIMARY
	modeDirectLink = 1 // DISP_SESSION_DIRECT_LINK_MODE
	userHWC        = 0 // SESSION_USER_HWC

	bufferION = 0 // DISP_BUFFER_ION
	bufferMVA = 2 // DISP_BUFFER_MVA — src_phy_addr is a ready M4U address, no ion fd or fence

	noIndex = 0xffffffff // "no buffer" / "no present fence"

	// Layers is how many overlay layers the primary display has. Every post configures all
	// of them, so layers a previous owner left enabled are switched off.
	Layers = 4

	ionHeapMultimedia = 10 // ION_HEAP_TYPE_MULTIMEDIA, heap id == type on this kernel
)

// Pixel formats as the display controller names them. In memory, BGRA8888 is the byte order
// B, G, R, A — the little-endian uint32 0xAARRGGBB, the same layout mtkfb advertises for fb0.
const (
	FormatRGBA8888 uint32 = 6<<8 | 4
	FormatBGRA8888 uint32 = 7<<8 | 4
	FormatRGBX8888 uint32 = 11<<8 | 4
	FormatBGRX8888 uint32 = 12<<8 | 4
)

// compat_disp_session_config
type sessionConfig struct {
	Type, DeviceID, Mode, SessionID, User, PresentFenceIdx, DCType uint32
	NeedMerge, TriggerMode                                        int32
}

// compat_disp_buffer_info
type bufferInfo struct {
	SessionID, LayerID, LayerEn uint32
	IonFd                       int32
	CacheSync, Index            uint32
	FenceFd                     int32
	InterfaceIndex              uint32
	InterfaceFenceFd            int32
}

// compat_disp_input_config
type inputConfig struct {
	LayerID, LayerEnable, BufferSource     uint32
	SrcBaseAddr, SrcPhyAddr                uint32
	SrcDirectLink, SrcFmt                  uint32
	SrcUseColorKey, SrcColorKey, SrcPitch  uint32
	SrcOffsetX, SrcOffsetY                 uint32
	SrcWidth, SrcHeight                    uint32
	TgtOffsetX, TgtOffsetY                 uint32
	TgtWidth, TgtHeight                    uint32
	LayerRotation, LayerType, VideoRotation uint32
	IsTdshp                                uint32
	NextBuffIdx                            uint32
	Identity, ConnectedType                int32
	Security, AlphaEnable, Alpha, SurAen   uint32
	SrcAlpha, DstAlpha, FrmSequence        uint32
	DimColor, YuvRange, Fps                uint32
	_                                      uint32 // compat_s64 is 8-byte aligned in this kernel
	Timestamp                              int64
	ExtSelLayer                            uint32
	SrcFenceFd                             int32
	DirtyRoiAddr, DirtyRoiNum              uint32
}

// compat_disp_ccorr_config
type ccorrConfig struct {
	IsDirty     uint8
	_           [3]byte
	Mode        int32
	ColorMatrix [16]int32
}

// compat_disp_session_input_config
type sessionInputConfig struct {
	Setter, SessionID, ConfigLayerNum uint32
	Config                            [8]inputConfig
	Ccorr                             ccorrConfig
	_                                 uint32 // padded to 8-byte alignment
}

// compat_disp_session_info
type sessionInfo struct {
	SessionID, MaxLayerNum, IsHwVsyncAvailable, DisplayType uint32
	DisplayWidth, DisplayHeight, DisplayFormat, DisplayMode uint32
	VsyncFPS, PhysicalWidth, PhysicalHeight                 uint32
	PhysicalWidthUm, PhysicalHeightUm, Density              uint32
	IsConnected, IsHDCPSupported, IsOVLDisabled, Is3DSupport uint32
	ConstLayerNum                                           uint32
}

// compat_disp_session_vsync_config
type vsyncConfig struct {
	SessionID, VsyncCnt uint32
	VsyncTs             uint64
	LcmFps              int32
	_                   uint32 // padded to 8-byte alignment
}

// compat_ion_allocation_data
type ionAlloc struct {
	Len, Align, HeapIDMask, Flags uint32
	Handle                        int32
}

// ion_fd_data
type ionFd struct {
	Handle, Fd int32
}

// ion_handle_data
type ionHandle struct {
	Handle int32
}

const (
	iocWrite = 1
	iocRead  = 2
)

func ioc(dir, typ, nr, size uintptr) uintptr { return dir<<30 | size<<16 | typ<<8 | nr }

// DISP_IOW(nr, type) — 'O'
func dispIOW(nr, size uintptr) uintptr { return ioc(iocWrite, 'O', nr, size) }

var (
	dispCreateSession      = dispIOW(201, unsafe.Sizeof(sessionConfig{}))
	dispTriggerSession     = dispIOW(203, unsafe.Sizeof(sessionConfig{}))
	dispPrepareInputBuffer = dispIOW(204, unsafe.Sizeof(bufferInfo{}))
	dispSetInputBuffer     = dispIOW(206, unsafe.Sizeof(sessionInputConfig{}))
	dispGetSessionInfo     = dispIOW(208, unsafe.Sizeof(sessionInfo{}))
	dispSetSessionMode     = dispIOW(209, unsafe.Sizeof(sessionConfig{}))
	dispWaitForVsync       = dispIOW(213, unsafe.Sizeof(vsyncConfig{}))

	ionIOCAlloc  = ioc(iocWrite|iocRead, 'I', 0, unsafe.Sizeof(ionAlloc{}))
	ionIOCFree   = ioc(iocWrite|iocRead, 'I', 1, unsafe.Sizeof(ionHandle{}))
	ionIOCShare  = ioc(iocWrite|iocRead, 'I', 4, unsafe.Sizeof(ionFd{}))
	ionIOCImport = ioc(iocWrite|iocRead, 'I', 5, unsafe.Sizeof(ionFd{}))
	ionIOCCustom = ioc(iocWrite|iocRead, 'I', 6, unsafe.Sizeof(ionCustom{}))
)

// ION custom commands. The MediaTek ION driver hangs its multimedia and system operations off
// ION_IOC_CUSTOM: cmd selects the family (system or multimedia), and the argument is that
// family's own tagged union.
const (
	ionCmdSystem     = 0 // ION_CMD_SYSTEM
	ionCmdMultimedia = 1 // ION_CMD_MULTIMEDIA

	ionSysCmdGetPhys  = 1 // ION_SYS_GET_PHYS
	ionMMConfigBuffer = 0 // ION_MM_CONFIG_BUFFER

	moduleDispOvl0 = 0 // module id the display's own import uses
)

// ion_custom_data
type ionCustom struct {
	Cmd uint32
	Arg uint32 // compat: unsigned long is 4 bytes
}

// ion_mm_config_buffer_param, the head of the ion_mm_data union.
type ionMMConfig struct {
	Handle                                         int32
	ModuleID, Security, Coherent, IovaStart, IovaEnd uint32
}

// ion_mm_data: the mm command then the config-buffer parameters (largest member of the union).
type ionMMData struct {
	MMCmd  uint32
	Config ionMMConfig
	_      [64]byte // room for the other, larger union members (debug info)
}

// ion_sys_get_phys_param, the head of the ion_sys_data union.
type ionSysGetPhys struct {
	Handle  int32
	PhyAddr uint32
	Len     uint32 // compat: unsigned long is 4 bytes
}

// ion_sys_data
type ionSysData struct {
	SysCmd uint32
	Phys   ionSysGetPhys
	_      [128]byte // room for the larger union members (record param)
}

// checkLayouts compares the Go structures with the sizes the kernel's compat handlers expect.
func checkLayouts() error {
	want := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"disp_session_config", unsafe.Sizeof(sessionConfig{}), 36},
		{"disp_buffer_info", unsafe.Sizeof(bufferInfo{}), 36},
		{"disp_input_config", unsafe.Sizeof(inputConfig{}), 168},
		{"disp_session_input_config", unsafe.Sizeof(sessionInputConfig{}), 1432},
		{"disp_session_info", unsafe.Sizeof(sessionInfo{}), 76},
		{"disp_session_vsync_config", unsafe.Sizeof(vsyncConfig{}), 24},
		{"ion_allocation_data", unsafe.Sizeof(ionAlloc{}), 20},
		{"ion_fd_data", unsafe.Sizeof(ionFd{}), 8},
	}
	for _, w := range want {
		if w.got != w.want {
			return fmt.Errorf("mtkdisp: %s is %d bytes here, kernel expects %d", w.name, w.got, w.want)
		}
	}
	return nil
}

// heapNew returns a new T that is certain to live on the heap, where it does not move. Converting
// a pointer to uintptr does not make its object escape, so it is forced here: escape analysis
// cannot see through the call to a function variable.
func heapNew[T any]() *T {
	p := new(T)
	escape(unsafe.Pointer(p))
	return p
}

var escape = func(unsafe.Pointer) {}

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// Info describes the primary display as the driver reports it.
type Info struct {
	Width, Height int    // panel pixels, portrait on the Show (480 wide, 960 tall)
	VsyncFPS      int    // refresh rate
	Format        uint32 // native format id
	PhysicalMM    [2]int // physical size, millimetres, if the driver knows it
	MaxLayers     int
}

// Display is an open primary display session.
type Display struct {
	disp    *os.File
	ion     *os.File
	session uint32
	seq     uint32
	Info    Info
}

// Open creates (or joins) the primary display session. Another owner — Android's composer —
// must not be posting frames at the same time; both would be steering the same overlay layers.
func Open() (*Display, error) {
	if err := checkLayouts(); err != nil {
		return nil, err
	}
	disp, err := os.OpenFile(dispDev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	ion, err := os.OpenFile(ionDev, os.O_RDWR, 0)
	if err != nil {
		disp.Close()
		return nil, err
	}
	d := &Display{disp: disp, ion: ion}

	cfg := sessionConfig{Type: sessionPrimary, Mode: modeDirectLink, User: userHWC, PresentFenceIdx: noIndex}
	if err := ioctl(disp, dispCreateSession, unsafe.Pointer(&cfg)); err != nil {
		d.Close()
		return nil, fmt.Errorf("create session: %w", err)
	}
	d.session = cfg.SessionID

	var info sessionInfo
	info.SessionID = d.session
	if err := ioctl(disp, dispGetSessionInfo, unsafe.Pointer(&info)); err != nil {
		d.Close()
		return nil, fmt.Errorf("session info: %w", err)
	}
	d.Info = Info{
		Width: int(info.DisplayWidth), Height: int(info.DisplayHeight),
		VsyncFPS: int(info.VsyncFPS), Format: info.DisplayFormat,
		PhysicalMM: [2]int{int(info.PhysicalWidth), int(info.PhysicalHeight)},
		MaxLayers: int(info.MaxLayerNum),
	}
	return d, nil
}

// Session is the driver's id for the display session.
func (d *Display) Session() uint32 { return d.session }

// Session modes. Direct link scans the overlay straight out to the panel; decouple composes the
// overlay into memory first and scans that out, which is what the driver's idle logic switches
// to on its own after a second without frames.
const (
	ModeDirectLink = 1
	ModeDecouple   = 2
)

// SetMode asks the driver to run the primary path in the given mode.
func (d *Display) SetMode(mode uint32) error {
	cfg := sessionConfig{Type: sessionPrimary, Mode: mode, SessionID: d.session, User: userHWC, PresentFenceIdx: noIndex}
	if err := ioctl(d.disp, dispSetSessionMode, unsafe.Pointer(&cfg)); err != nil {
		return fmt.Errorf("set session mode %d: %w", mode, err)
	}
	return nil
}

// Close releases the device handles. The session itself stays registered in the driver, as it
// does for the composer: there is exactly one primary display and creating it again just returns it.
func (d *Display) Close() error {
	var err error
	if d.ion != nil {
		err = errors.Join(err, d.ion.Close())
	}
	if d.disp != nil {
		err = errors.Join(err, d.disp.Close())
	}
	return err
}

// Buffer is one frame of pixels the display controller can scan out. Pixels are written through
// Mem; Pitch is in pixels and equals Width here.
type Buffer struct {
	d      *Display
	handle int32
	fd     int
	Mem    []byte
	Width  int
	Height int
	Pitch  int
	mva    uint32
	// release is the fence the driver hands back on prepare: it signals once the controller has
	// finished reading the buffer, so it can be drawn into again.
	release int
}

// NewBuffer allocates a 32-bit-per-pixel buffer of the given size from the multimedia heap and
// maps it for the CPU. The mapping is uncached, which is what CPU-drawn frames want: no cache
// maintenance before the controller reads them.
func (d *Display) NewBuffer(width, height int) (*Buffer, error) {
	size := width * height * 4
	a := ionAlloc{Len: uint32(size), HeapIDMask: 1 << ionHeapMultimedia}
	if err := ioctl(d.ion, ionIOCAlloc, unsafe.Pointer(&a)); err != nil {
		return nil, fmt.Errorf("ion alloc %d bytes: %w", size, err)
	}
	share := ionFd{Handle: a.Handle}
	if err := ioctl(d.ion, ionIOCShare, unsafe.Pointer(&share)); err != nil {
		ioctl(d.ion, ionIOCFree, unsafe.Pointer(&ionHandle{Handle: a.Handle}))
		return nil, fmt.Errorf("ion share: %w", err)
	}
	mem, err := unix.Mmap(int(share.Fd), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		unix.Close(int(share.Fd))
		ioctl(d.ion, ionIOCFree, unsafe.Pointer(&ionHandle{Handle: a.Handle}))
		return nil, fmt.Errorf("mmap ion buffer: %w", err)
	}
	b := &Buffer{d: d, handle: a.Handle, fd: int(share.Fd), Mem: mem, Width: width, Height: height, Pitch: width, release: -1}
	// Resolve the device address once. The overlay is handed this directly, so the buffer needs no
	// per-frame ion import or fence — the fragile path that leaves a posted layer dark.
	mva, err := b.MVA()
	if err != nil {
		b.Free()
		return nil, err
	}
	if mva == 0 {
		b.Free()
		return nil, errors.New("buffer got no device address from the multimedia heap")
	}
	b.mva = mva
	return b, nil
}

// MVA configures the buffer for the display's M4U port and returns the device-visible address
// the overlay would read from. Zero means the multimedia heap could not give the buffer an M4U
// mapping, which is exactly what makes a posted layer stay dark: the driver's own import does the
// same two steps and disables the layer when this comes back zero.
func (b *Buffer) MVA() (uint32, error) {
	// The ion argument points at cfg and phys through a uint32 field, which the stack copier
	// cannot update, so both are heap objects (heapNew) kept alive across their ioctl.
	cfg := heapNew[ionMMData]()
	cfg.MMCmd = ionMMConfigBuffer
	cfg.Config = ionMMConfig{Handle: b.handle, ModuleID: moduleDispOvl0}
	c := ionCustom{Cmd: ionCmdMultimedia, Arg: uint32(uintptr(unsafe.Pointer(cfg)))}
	err := ioctl(b.d.ion, ionIOCCustom, unsafe.Pointer(&c))
	runtime.KeepAlive(cfg)
	if err != nil {
		return 0, fmt.Errorf("config buffer: %w", err)
	}
	phys := heapNew[ionSysData]()
	phys.SysCmd = ionSysCmdGetPhys
	phys.Phys = ionSysGetPhys{Handle: b.handle}
	c = ionCustom{Cmd: ionCmdSystem, Arg: uint32(uintptr(unsafe.Pointer(phys)))}
	err = ioctl(b.d.ion, ionIOCCustom, unsafe.Pointer(&c))
	runtime.KeepAlive(phys)
	if err != nil {
		return 0, fmt.Errorf("get phys: %w", err)
	}
	return phys.Phys.PhyAddr, nil
}

// Wait blocks until the display controller is done with the buffer's last post, or the timeout
// passes. It returns immediately when the buffer was never posted.
func (b *Buffer) Wait(timeout time.Duration) error {
	if b.release < 0 {
		return nil
	}
	fds := []unix.PollFd{{Fd: int32(b.release), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, int(timeout/time.Millisecond))
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return errors.New("release fence timed out")
		}
		unix.Close(b.release)
		b.release = -1
		return nil
	}
}

// Free unmaps and frees the buffer. Post must not be in flight.
func (b *Buffer) Free() error {
	if b.release >= 0 {
		unix.Close(b.release)
		b.release = -1
	}
	var err error
	if b.Mem != nil {
		err = errors.Join(err, unix.Munmap(b.Mem))
		b.Mem = nil
	}
	if b.fd >= 0 {
		err = errors.Join(err, unix.Close(b.fd))
		b.fd = -1
	}
	if b.handle != 0 {
		err = errors.Join(err, ioctl(b.d.ion, ionIOCFree, unsafe.Pointer(&ionHandle{Handle: b.handle})))
		b.handle = 0
	}
	return err
}

// Post shows the buffer on the given overlay layer and switches every other layer off. The
// buffer's device address is handed to the overlay directly (DISP_BUFFER_MVA), so the driver never
// imports the ion fd for the layer. Prepare still hands back a release fence, which is kept on the
// buffer and closed on the next post; Buffer.Wait is what blocks on it, and a caller that paces
// itself with the vsync clock instead can leave it alone.
func (d *Display) Post(layer int, b *Buffer, format uint32) error {
	if layer < 0 || layer >= Layers {
		return fmt.Errorf("layer %d out of range", layer)
	}
	if b.release >= 0 {
		unix.Close(b.release)
		b.release = -1
	}
	// Prepare registers the buffer with the layer's fence timeline and returns a buffer index; the
	// kernel keeps its own bookkeeping keyed on that index even though the address below is what
	// the overlay actually reads.
	prep := bufferInfo{SessionID: d.session, LayerID: uint32(layer), LayerEn: 1, IonFd: int32(b.fd), FenceFd: -1, InterfaceFenceFd: -1}
	if err := ioctl(d.disp, dispPrepareInputBuffer, unsafe.Pointer(&prep)); err != nil {
		return fmt.Errorf("prepare buffer: %w", err)
	}
	if prep.Index == 0 {
		return errors.New("prepare buffer: driver refused the buffer (no index)")
	}
	b.release = int(prep.FenceFd)

	d.seq++
	var in sessionInputConfig
	in.Setter = userHWC
	in.SessionID = d.session
	in.ConfigLayerNum = Layers
	for i := 0; i < Layers; i++ {
		c := &in.Config[i]
		c.LayerID = uint32(i)
		c.NextBuffIdx = noIndex
		c.SrcFenceFd = -1
		if i != layer {
			continue
		}
		c.LayerEnable = 1
		c.BufferSource = bufferION
		// Hand the overlay the resolved device address directly. set_primary_buffer uses a
		// non-zero src_phy_addr verbatim and skips the fence lookup, which is the step that was
		// returning zero and leaving the layer disabled.
		c.SrcPhyAddr = b.mva
		c.SrcFmt = format
		c.SrcPitch = uint32(b.Pitch)
		c.SrcWidth, c.SrcHeight = uint32(b.Width), uint32(b.Height)
		c.TgtWidth, c.TgtHeight = uint32(b.Width), uint32(b.Height)
		c.Alpha = 0xff
		c.NextBuffIdx = prep.Index
		c.FrmSequence = d.seq
	}
	if err := ioctl(d.disp, dispSetInputBuffer, unsafe.Pointer(&in)); err != nil {
		return fmt.Errorf("set input buffer: %w", err)
	}

	trig := sessionConfig{Type: sessionPrimary, Mode: modeDirectLink, SessionID: d.session, User: userHWC, PresentFenceIdx: noIndex}
	if err := ioctl(d.disp, dispTriggerSession, unsafe.Pointer(&trig)); err != nil {
		return fmt.Errorf("trigger: %w", err)
	}
	return nil
}

const fbDev = "/dev/graphics/fb0"

var (
	// MTKFB_CAPTURE_FRAMEBUFFER: the argument points at a 32-bit user address of a buffer the
	// controller's write-DMA fills with the composed screen (BGRA8888).
	fbCaptureFramebuffer = ioc(iocWrite, 'O', 3, 4)
	// MTKFB_GET_POWERSTATE: 1 when the panel path is awake, 0 when it is asleep.
	fbGetPowerState = ioc(iocRead, 'O', 21, 4)
)

// Awake reports whether the primary display path is powered. Frames posted while it sleeps are
// dropped by the driver.
func Awake() (bool, error) {
	f, err := os.OpenFile(fbDev, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var state uint32
	if err := ioctl(f, fbGetPowerState, unsafe.Pointer(&state)); err != nil {
		return false, err
	}
	return state != 0, nil
}

// Capture reads back what the display controller is composing, as BGRA8888 rows of width
// pixels: the same thing the panel shows, whoever drew it. The mtkfb driver still offers this
// even though its framebuffer cannot be mapped.
func Capture(width, height int) ([]byte, error) {
	f, err := os.OpenFile(fbDev, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	size := width * height * 4
	// The kernel maps the user pages for DMA, so the buffer must be page-aligned and resident.
	buf, err := unix.Mmap(-1, 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_PRIVATE|unix.MAP_ANON|unix.MAP_LOCKED)
	if err != nil {
		return nil, err
	}
	defer unix.Munmap(buf)
	for i := 0; i < size; i += 4096 {
		buf[i] = 0 // fault every page in before the DMA is pointed at it
	}
	addr := uint32(uintptr(unsafe.Pointer(&buf[0])))
	if err := ioctl(f, fbCaptureFramebuffer, unsafe.Pointer(&addr)); err != nil {
		return nil, fmt.Errorf("capture: %w", err)
	}
	out := make([]byte, size)
	copy(out, buf)
	return out, nil
}

// Register peeking. The display manager lets a process map the display-subsystem register
// window (physical 0x14007000-0x14018000 on MT8163: OVL0, OVL1, RDMA0, RDMA1, WDMA0, color
// pipeline, DSI). Reading it shows what the hardware is actually scanning out, whatever the
// driver's logs say — and the driver's logs are compiled out on this kernel.
const (
	regWindowBase = 0x14007000
	regWindowSize = 0x11000
	ovl0Base      = 0x14007000
	rdma0Base     = 0x14009000
)

// Regs is a snapshot of the overlay and read-DMA registers that decide what the panel shows.
type Regs struct {
	OvlSta, OvlEn, OvlRoiSize, OvlBgColor, OvlSrcCon uint32
	Layer                                            [4]LayerRegs
	RdmaGlobalCon, RdmaSizeCon0, RdmaSizeCon1        uint32
	RdmaMemCon, RdmaMemStart, RdmaMemPitch           uint32
}

// LayerRegs is one overlay layer's configuration.
type LayerRegs struct {
	Con, SrcSize, Offset, Pitch, Addr uint32
}

// Enabled reports whether the layer is switched on in OVL_SRC_CON.
func (r Regs) Enabled(layer int) bool { return r.OvlSrcCon&(1<<layer) != 0 }

// ReadRegs maps the register window and reads the overlay and RDMA state.
func ReadRegs() (Regs, error) {
	var r Regs
	f, err := os.OpenFile(dispDev, os.O_RDWR, 0)
	if err != nil {
		return r, err
	}
	defer f.Close()
	mem, err := unix.Mmap(int(f.Fd()), regWindowBase, regWindowSize, unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return r, fmt.Errorf("map display registers: %w", err)
	}
	defer unix.Munmap(mem)
	rd := func(base, off int) uint32 {
		p := base - regWindowBase + off
		return *(*uint32)(unsafe.Pointer(&mem[p]))
	}
	r.OvlSta = rd(ovl0Base, 0x000)
	r.OvlEn = rd(ovl0Base, 0x00c)
	r.OvlRoiSize = rd(ovl0Base, 0x020)
	r.OvlBgColor = rd(ovl0Base, 0x028)
	r.OvlSrcCon = rd(ovl0Base, 0x02c)
	for i := range r.Layer {
		l := &r.Layer[i]
		l.Con = rd(ovl0Base, 0x030+i*0x20)
		l.SrcSize = rd(ovl0Base, 0x038+i*0x20)
		l.Offset = rd(ovl0Base, 0x03c+i*0x20)
		l.Pitch = rd(ovl0Base, 0x044+i*0x20)
		l.Addr = rd(ovl0Base, 0xf40+i*0x20)
	}
	r.RdmaGlobalCon = rd(rdma0Base, 0x010)
	r.RdmaSizeCon0 = rd(rdma0Base, 0x014)
	r.RdmaSizeCon1 = rd(rdma0Base, 0x018)
	r.RdmaMemCon = rd(rdma0Base, 0x024)
	r.RdmaMemStart = rd(rdma0Base, 0xf00)
	r.RdmaMemPitch = rd(rdma0Base, 0x02c)
	return r, nil
}

// ScanIoctlSize is a debugging aid: it asks the display driver which argument size it accepts
// for a DISP ioctl number by trying every size in [min, max] with a zeroed argument. The
// driver answers ENOTTY for every size but the one its compat handler was built with. A zeroed
// argument is harmless for the session ioctls: no session id, no layers.
func ScanIoctlSize(nr, min, max int) []int {
	f, err := os.OpenFile(dispDev, os.O_RDWR, 0)
	if err != nil {
		return nil
	}
	defer f.Close()
	buf := make([]byte, max+1)
	var hits []int
	for size := min; size <= max; size++ {
		err := ioctl(f, dispIOW(uintptr(nr), uintptr(size)), unsafe.Pointer(&buf[0]))
		if err != unix.ENOTTY {
			hits = append(hits, size)
		}
	}
	return hits
}

// WaitVsync blocks until the next vertical sync and returns its count and timestamp.
func (d *Display) WaitVsync() (count uint32, ts time.Duration, err error) {
	v := vsyncConfig{SessionID: d.session}
	if err := ioctl(d.disp, dispWaitForVsync, unsafe.Pointer(&v)); err != nil {
		return 0, 0, err
	}
	return v.VsyncCnt, time.Duration(v.VsyncTs), nil
}
