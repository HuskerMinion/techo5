//go:build !dot && !spot

// Package camera is the Echo Show's front camera, driven without Android.
//
// The OV02B10 sits behind MediaTek's imgsensor driver (power, clock mux, the init table over
// I2C) and the ISP driver (clocks, register windows, the IMGO DMA). Neither offers V4L2; the
// sensor interface, CSI-2 receiver, timing generator and DMA are programmed here through the ISP
// driver's mmap, the way MediaTek's libcamdrv does on MT8163. docs/camera-research.md has the
// map and the dead ends; cmd/camframe is the standalone version of this file.
//
// Frames arrive as 1600x1200 packed 10-bit Bayer at about 14 frames a second and are handed out
// two ways: an 800x600 RGBA made on every frame (one pixel per Bayer cell, grey-world white
// balance, a gamma curve) for the live view and the stream, and the packed frame itself, which
// Full() demosaics to 1600x1200 on demand for stills. The sensor runs only while something holds
// it (Acquire), and a little longer, so a run of snapshots does not restart it each time.
package camera

import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// sensorSpec is what differs between the two Echo Show 5 generations. Everything else — the
// imgsensor driver that powers the sensor and writes its register table, the CSI-2 receiver, the
// timing generator, the DMA, the picture pipeline — is the same on both.
//
// The 2nd gen (cronos) has an OmniVision OV02B10: 1600x1200 at about 14 frames a second, the first
// pixel of each Bayer cell red. The 1st gen (checkers) has an OV9734: 1280x720 at up to 30, the
// first pixel blue, and its own exposure limits (framelength 802 lines less a margin of 4, shutter
// from 1 line; both from the driver in the kernel source). The gain scale and its 15.5x ceiling are
// the same on both, and both use one MIPI lane with an 85 ns settle.
//
// The Show 8 (crown) has the OV9734 as well, so it takes the same profile. Its own kernel says so
// while powering the camera up, read off a unit 2026-09-22:
//
//	[kdSetDriver] :[0][1][1][ov9734_mipi_raw][32]
//	[PowerON]pinSetIdx:0, currSensorName: ov9734_mipi_raw
//
// Worth recording why this needed saying at all: while a Show 8 was still being read as a 1st gen
// Show 5, it picked the right sensor by accident. Telling the boards apart is what turned that into
// a choice someone had to make, and the symptom of making it wrongly is a camera that opens, streams
// nothing, and times out.
type sensorSpec struct {
	w, h       int  // what the sensor hands over
	outW, outH int  // one pixel per Bayer cell: the live view and the stream
	minShut    int  // shortest exposure the driver takes, in lines
	frameLines int  // shutter lines that fit a frame at the sensor's own rate
	blueFirst  bool // the first pixel of each cell is blue, not red
}

// hasOV9734 is whether this board carries the OV9734 rather than the OV02B10.
func hasOV9734() bool { return layout.Checkers() || layout.Crown() }

func pickSensor(ov9734 bool) sensorSpec {
	if ov9734 {
		return sensorSpec{w: 1280, h: 720, outW: 640, outH: 360, minShut: 1, frameLines: 798, blueFirst: true}
	}
	return sensorSpec{w: 1600, h: 1200, outW: 800, outH: 600, minShut: 4, frameLines: 1200}
}

var sensor = pickSensor(hasOV9734())

// Width and Height are the frames handed out.
var Width, Height = sensor.outW, sensor.outH

// ---- the hardware ----

var (
	sensorW, sensorH = sensor.w, sensor.h
	bytesPerLine     = sensorW * 10 / 8 // packed 10-bit Bayer: four pixels in five bytes
	frameBytes       = bytesPerLine * sensorH
)

const (
	slots = 3 // DMA targets in rotation: a finished frame rests two periods

	sensMagic   = 'i'
	nrOpen      = 0
	nrControl   = 20
	nrClose     = 25
	nrSetDriver = 35
	nrSetMCLK   = 60
	nrSetCur    = 70
	nrFeature   = 15
	sensorMain  = 1

	// Sensor features (ACDK_SENSOR_FEATURE_ENUM, from 3000): exposure in lines, gain in 1/64.
	featShutter = 3004
	featGain    = 3006

	ispMagic   = 'k'
	nrIspReset = 0

	ispBase, ispLen       = 0x15000000, 0x10000
	seninfBase, seninfLen = 0x15008000, 0x4000
	mipiBase, mipiLen     = 0x10217000, 0x3000

	// CAM registers (offsets in the CAMINF window).
	regCtlEn1      = 0x4004
	regCtlEn2      = 0x4008
	regCtlDmaEn    = 0x400C
	regCtlFmtSel   = 0x4010
	regCtlSel      = 0x4018
	regCtlPixID    = 0x401C
	regCtlIntEn    = 0x4020
	regCtlMuxSel2  = 0x4078
	regImgoFbc     = 0x40F4
	regImgoBase    = 0x4300
	regImgoOfst    = 0x4304
	regImgoXsize   = 0x4308
	regImgoYsize   = 0x430C
	regImgoStride  = 0x4310
	regImgoCon     = 0x4314
	regImgoCon2    = 0x4318
	regCtlImgoSize = 0x414C
	regCtlClkEn    = 0x4150
	regTgSenMode   = 0x4410
	regTgVfCon     = 0x4414
	regTgGrabPxl   = 0x4418
	regTgGrabLin   = 0x441C
	regTgPathCfg   = 0x4420
	regTgInterSt   = 0x444C

	// SENINF registers (offsets in the SENINF window).
	regSeninfTopCtrl = 0x0000
	regSeninfTopMux  = 0x0008
	regSeninf1Ctrl   = 0x0100
	regSeninf1Mux    = 0x0120
	regTG1PhCnt      = 0x0200
	regTG1SenCk      = 0x0204
	regMipiRxCon38   = 0x0338
	regMipiRxCon3C   = 0x033C
	regMipiRxCon44   = 0x0344
	regMipiRxCon48   = 0x0348
	regNcsi2Ctl      = 0x03A0
	regNcsi2LnrdTim  = 0x03A8
	regNcsi2Dpcm     = 0x03AC
	regNcsi2IntEn    = 0x03B0
	regNcsi2HsrxDbg  = 0x03D8
	regCsi2Ctrl      = 0x0360

	ionHeapMultimedia = 10
	ionCmdSystem      = 0
	ionCmdMultimedia  = 1
	ionSysGetPhys     = 1
	ionMMConfigBuffer = 0
	m4uPortIMGO       = 24
)

type ionAlloc struct {
	Len, Align, HeapIDMask, Flags uint32
	Handle                        int32
}
type ionFd struct{ Handle, Fd int32 }
type ionCustom struct{ Cmd, Arg uint32 }
type ionMMConfig struct {
	Handle                                           int32
	ModuleID, Security, Coherent, IovaStart, IovaEnd uint32
}
type ionMMData struct {
	MMCmd  uint32
	Config ionMMConfig
	_      [64]byte
}
type ionSysGetPhysP struct {
	Handle       int32
	PhyAddr, Len uint32
}
type ionSysData struct {
	SysCmd uint32
	Phys   ionSysGetPhysP
	_      [128]byte
}
type m4uPort struct {
	PortID                                            int32
	Virtuality, Security, Domain, Distance, Direction uint32
}

func ioc(dir, typ, nr, size uintptr) uintptr { return dir<<30 | size<<16 | typ<<8 | nr }

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(arg))
	if e != 0 {
		return e
	}
	return nil
}

type mmio struct{ mem []byte }

func mapWindow(fd int, base, length int64) (*mmio, error) {
	mem, err := syscall.Mmap(fd, base, int(length), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap %#x: %w", base, err)
	}
	return &mmio{mem: mem}, nil
}

func (m *mmio) rd(off uint32) uint32 {
	return atomic.LoadUint32((*uint32)(unsafe.Pointer(&m.mem[off])))
}
func (m *mmio) wr(off, val uint32) {
	atomic.StoreUint32((*uint32)(unsafe.Pointer(&m.mem[off])), val)
}
func (m *mmio) mask(off, clear, set uint32) { m.wr(off, m.rd(off)&^clear|set) }
func (m *mmio) unmap() {
	if m != nil && m.mem != nil {
		syscall.Munmap(m.mem)
	}
}

// device is the open camera: file descriptors, register windows, the frame buffers.
type device struct {
	isp, sens, ion int
	cam, sen, mipi *mmio
	buf            []byte
	mva            uint32
	mclk           [3]uint32
	sensorOpen     bool

	// Exposure control: what is set on the sensor now, and frames to wait before judging it.
	shutter, gain int
	settle        int
}

func open() (*device, error) {
	d := &device{isp: -1, sens: -1, ion: -1}
	var err error
	if d.isp, err = syscall.Open("/dev/camera-isp", syscall.O_RDWR, 0); err != nil {
		return nil, fmt.Errorf("open camera-isp: %w", err)
	}
	if d.cam, err = mapWindow(d.isp, ispBase, ispLen); err != nil {
		d.close()
		return nil, err
	}
	if d.sen, err = mapWindow(d.isp, seninfBase, seninfLen); err != nil {
		d.close()
		return nil, err
	}
	if d.mipi, err = mapWindow(d.isp, mipiBase, mipiLen); err != nil {
		d.close()
		return nil, err
	}
	if d.sens, err = syscall.Open("/dev/kd_camera_hw", syscall.O_RDWR, 0); err != nil {
		d.close()
		return nil, fmt.Errorf("open kd_camera_hw: %w", err)
	}

	// Sensor master clock: the 48 MHz camtg group, divided by 2 in the SENINF timing generator.
	d.mclk = [3]uint32{1, 1, 0}
	if err := ioctl(d.sens, ioc(3, sensMagic, nrSetMCLK, 12), unsafe.Pointer(&d.mclk[0])); err != nil {
		d.close()
		return nil, fmt.Errorf("SET_MCLK_PLL: %w", err)
	}
	setMCLK1(d.sen, 1)

	// Frame buffers from the ION multimedia heap, mapped for the IMGO port.
	if err := d.allocBuffers(); err != nil {
		d.close()
		return nil, err
	}
	if err := m4uConfig(m4uPortIMGO); err != nil {
		d.close()
		return nil, err
	}

	// Sensor: driver select, power, init table.
	idx := [2]uint32{sensorMain << 16, 0}
	if err := ioctl(d.sens, ioc(3, sensMagic, nrSetDriver, 8), unsafe.Pointer(&idx[0])); err != nil {
		d.close()
		return nil, fmt.Errorf("SET_DRIVER: %w", err)
	}
	cur := uint32(sensorMain)
	ioctl(d.sens, ioc(3, sensMagic, nrSetCur, 4), unsafe.Pointer(&cur))
	if err := ioctl(d.sens, ioc(0, sensMagic, nrOpen, 0), nil); err != nil {
		d.close()
		return nil, fmt.Errorf("sensor open: %w", err)
	}
	d.sensorOpen = true

	d.setupISP()
	setupCSI2(d.sen, d.mipi)

	// Preview mode; the sensor's mode table ends with stream-on.
	var window, config [256]byte
	ctl := [4]uint32{sensorMain, 0, uint32(uintptr(unsafe.Pointer(&window[0]))), uint32(uintptr(unsafe.Pointer(&config[0])))}
	if err := ioctl(d.sens, ioc(3, sensMagic, nrControl, 16), unsafe.Pointer(&ctl[0])); err != nil {
		d.close()
		return nil, fmt.Errorf("sensor control: %w", err)
	}
	// A middling exposure to start from; the loop takes it from there.
	d.shutter, d.gain = 600, 128
	d.setFeature(featShutter, uint64(d.shutter))
	d.setFeature(featGain, uint64(d.gain))
	d.settle = aeDelay
	return d, nil
}

// setFeature is KDIMGSENSORIOC_X_FEATURECONCTROL with one integer parameter. The kernel is 64-bit
// and reads the parameter as an unsigned long, so eight bytes go over.
func (d *device) setFeature(id uint32, v uint64) {
	var para [8]byte
	for i := range para {
		para[i] = byte(v >> (8 * i))
	}
	size := uint32(len(para))
	ctl := [4]uint32{sensorMain, id, uint32(uintptr(unsafe.Pointer(&para[0]))), uint32(uintptr(unsafe.Pointer(&size)))}
	if err := ioctl(d.sens, ioc(3, sensMagic, nrFeature, 16), unsafe.Pointer(&ctl[0])); err != nil {
		slog.Warn("camera: sensor feature", "id", id, "err", err)
	}
}

// Exposure: the sensor has no automatic mode, so this is it. The frame is metered in zones (meter, in
// exposure.go) and the exposure (shutter lines times gain) is moved towards a target, shutter
// first — up to a frame at the sensor's rate — then gain, then longer shutters at the cost of
// frame rate, then the rest of the gain.
const (
	aeTarget   = 290  // mean of the 10-bit frame to aim for: a lit room, no clipping to speak of
	aeDelay    = 3    // frames between changes: a new exposure takes two frames to show
	aeMaxShut  = 4000 // beyond this the frame rate drops under 5 a second
	aeMinGain  = 64   // 1x
	aeMidGain  = 384  // 6x, before trading frame rate
	aeMaxGain  = 992  // the driver's ceiling, 15.5x
	aeDeadband = 6    // no change inside target ± this
)

// The shortest exposure the sensor takes and the lines that fit one of its frames: its own.
var (
	aeMinShut = sensor.minShut
	aeFrame   = sensor.frameLines
)

func (d *device) autoExpose(bayer []byte) {
	if d.settle > 0 {
		d.settle--
		return
	}
	mean := int(meter(bayer))
	if mean >= aeTarget-aeDeadband && mean <= aeTarget+aeDeadband {
		return
	}
	// The change wanted, capped to a factor of two per step so a bright window does not swing it.
	ratio := float64(aeTarget) / float64(max(mean, 1))
	if ratio > 2 {
		ratio = 2
	}

	if ratio < 0.5 {
		ratio = 0.5
	}
	want := float64(d.shutter*d.gain) * ratio
	shutter, gain := d.shutter, d.gain
	switch {
	case want <= float64(aeFrame*aeMinGain):
		shutter, gain = int(want/aeMinGain), aeMinGain
	case want <= float64(aeFrame*aeMidGain):
		shutter, gain = aeFrame, int(want)/aeFrame
	case want <= float64(aeMaxShut*aeMidGain):
		shutter, gain = int(want/aeMidGain), aeMidGain
	default:
		shutter, gain = aeMaxShut, int(want/aeMaxShut)
	}
	shutter = min(max(shutter, aeMinShut), aeMaxShut)
	gain = min(max(gain, aeMinGain), aeMaxGain)
	if shutter == d.shutter && gain == d.gain {
		return
	}
	if shutter != d.shutter {
		d.setFeature(featShutter, uint64(shutter))
	}
	if gain != d.gain {
		d.setFeature(featGain, uint64(gain))
	}
	slog.Debug("camera exposure", "mean", mean, "shutter", shutter, "gain", gain)
	d.shutter, d.gain, d.settle = shutter, gain, aeDelay
}

func (d *device) close() {
	if d.cam != nil {
		d.cam.mask(regTgVfCon, 1, 0) // view finder off
	}
	if d.sensorOpen {
		ioctl(d.sens, ioc(0, sensMagic, nrClose, 0), nil)
	}
	if d.sen != nil && d.mipi != nil {
		teardownCSI2(d.sen, d.mipi)
	}
	if d.sens >= 0 {
		d.mclk[0] = 0
		ioctl(d.sens, ioc(3, sensMagic, nrSetMCLK, 12), unsafe.Pointer(&d.mclk[0]))
		syscall.Close(d.sens)
	}
	if d.buf != nil {
		unix.Munmap(d.buf)
	}
	if d.ion >= 0 {
		syscall.Close(d.ion)
	}
	d.cam.unmap()
	d.sen.unmap()
	d.mipi.unmap()
	if d.isp >= 0 {
		syscall.Close(d.isp)
	}
}

// allocBuffers takes slots frames from ION and resolves the IMGO device address.
func (d *device) allocBuffers() error {
	var err error
	if d.ion, err = syscall.Open("/dev/ion", syscall.O_RDWR, 0); err != nil {
		return fmt.Errorf("open ion: %w", err)
	}
	size := slots * frameBytes
	a := ionAlloc{Len: uint32(size), Align: 4096, HeapIDMask: 1 << ionHeapMultimedia}
	if err := ioctl(d.ion, ioc(3, 'I', 0, unsafe.Sizeof(a)), unsafe.Pointer(&a)); err != nil {
		return fmt.Errorf("ion alloc: %w", err)
	}
	share := ionFd{Handle: a.Handle}
	if err := ioctl(d.ion, ioc(3, 'I', 4, unsafe.Sizeof(share)), unsafe.Pointer(&share)); err != nil {
		return fmt.Errorf("ion share: %w", err)
	}
	d.buf, err = unix.Mmap(int(share.Fd), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	syscall.Close(int(share.Fd))
	if err != nil {
		return fmt.Errorf("mmap ion buffer: %w", err)
	}
	cfg := ionMMData{MMCmd: ionMMConfigBuffer, Config: ionMMConfig{Handle: a.Handle, ModuleID: m4uPortIMGO}}
	c := ionCustom{Cmd: ionCmdMultimedia, Arg: uint32(uintptr(unsafe.Pointer(&cfg)))}
	if err := ioctl(d.ion, ioc(3, 'I', 6, unsafe.Sizeof(c)), unsafe.Pointer(&c)); err != nil {
		return fmt.Errorf("ion config buffer: %w", err)
	}
	phys := ionSysData{SysCmd: ionSysGetPhys, Phys: ionSysGetPhysP{Handle: a.Handle}}
	c = ionCustom{Cmd: ionCmdSystem, Arg: uint32(uintptr(unsafe.Pointer(&phys)))}
	if err := ioctl(d.ion, ioc(3, 'I', 6, unsafe.Sizeof(c)), unsafe.Pointer(&c)); err != nil {
		return fmt.Errorf("ion get phys: %w", err)
	}
	if phys.Phys.PhyAddr == 0 {
		return errors.New("ion gave no device address for the IMGO port")
	}
	d.mva = phys.Phys.PhyAddr
	return nil
}

// m4uConfig puts the IMGO M4U port into translated mode (MTK_M4U_T_CONFIG_PORT on /proc/m4u).
func m4uConfig(port int32) error {
	fd, err := syscall.Open("/proc/m4u", syscall.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open /proc/m4u: %w", err)
	}
	defer syscall.Close(fd)
	p := m4uPort{PortID: port, Virtuality: 1, Distance: 1}
	if err := ioctl(fd, ioc(1, 'g', 11, 4), unsafe.Pointer(&p)); err != nil {
		return fmt.Errorf("m4u config port %d: %w", port, err)
	}
	return nil
}

// setMCLK1 is camera_isp.c's ISP_set_mclk1 + ISP_MCLK1_EN: CMMCLK = camtg / (clkcnt + 1).
func setMCLK1(s *mmio, clkcnt uint32) {
	clkfPol := uint32(0)
	if clkcnt&1 == 0 {
		clkfPol = 1
	}
	clkfEdge := uint32(1)
	if clkcnt > 1 {
		clkfEdge = (clkcnt + 1) >> 1
	}
	s.mask(regTG1PhCnt, 1<<31, 1<<31)
	s.mask(regSeninfTopCtrl, 0xc00, 0x300)
	s.mask(regTG1SenCk, 0x3f|0x3f00|0x3f0000, clkfEdge|clkcnt<<16)
	s.mask(regTG1PhCnt, 0x3|0x4|1<<6|1<<28, 1|clkfPol<<2|1<<28)
	s.mask(regSeninf1Mux, 1<<31, 1<<31)
	s.mask(regSeninf1Ctrl, 1<<31, 1)
	s.mask(regTG1PhCnt, 1<<29, 1<<29)
}

// setupISP programs pass 1: TG -> packer -> IMGO, one byte a pixel, interrupts masked (frame
// sync is the TG's frame counter, polled).
func (d *device) setupISP() {
	cam := d.cam
	ioctl(d.isp, ioc(0, ispMagic, nrIspReset, 0), nil)
	cam.wr(regCtlIntEn, 0)
	cam.wr(regTgVfCon, 0)
	cam.wr(regCtlClkEn, 0x1FFFF)
	cam.wr(regCtlEn1, 1<<0|1<<12) // TG1_EN | PAK_EN: the DMA is fed by the packer
	cam.wr(regCtlEn2, 0)
	cam.wr(regCtlDmaEn, 1<<0)         // IMGO_EN
	cam.wr(regCtlFmtSel, 1<<16|1<<12) // TG1_FMT = RAW10; CAM_OUT_FMT 1 = the packer writes 10-bit packed
	cam.wr(regCtlSel, 0)
	cam.wr(regCtlPixID, 0)               // Bayer, R first
	cam.mask(regCtlMuxSel2, 1<<4, 1<<18) // IMGO_MUX 0 (packer), IMGO_MUX_EN
	cam.wr(regImgoFbc, 0)
	cam.wr(regImgoBase, d.mva)
	cam.wr(regImgoOfst, 0)
	cam.wr(regImgoXsize, uint32(bytesPerLine-1))
	cam.wr(regImgoYsize, uint32(sensorH-1))
	cam.wr(regImgoStride, uint32(bytesPerLine))
	cam.wr(regCtlImgoSize, uint32(sensorW)<<16|uint32(sensorH))
	if cam.rd(regImgoCon) == 0 {
		cam.wr(regImgoCon, 0x80000040)
		cam.wr(regImgoCon2, 0x00200020)
	}
	cam.wr(regTgSenMode, 1<<0|1<<2) // CMOS_EN, SOT_MODE
	cam.wr(regTgVfCon, 1<<12)       // SPDELAY_MODE, continuous
	cam.wr(regTgGrabPxl, uint32(sensorW)<<16)
	cam.wr(regTgGrabLin, uint32(sensorH)<<16)
	cam.wr(regTgPathCfg, 0)
}

// setupCSI2 is SeninfDrvImp's MIPI bring-up for SENINF1/CSI0 feeding TG1: D-PHY analog on, HSRX
// calibration, SENINF1 mux and source, the NCSI2 receiver (1 lane, ECC order 1, 85 ns settle at
// the 364 MHz ISP clock), top mux TG1 <- SENINF1.
func setupCSI2(s, ana *mmio) {
	ana.mask(0x4C, ^uint32(0xFEFBEFBE), 0)
	ana.mask(0x50, ^uint32(0xFEFBEFBE), 0)
	for _, off := range []uint32{0x00, 0x04, 0x08, 0x0C, 0x10} {
		ana.mask(off, 0, 1<<3)
	}
	ana.mask(0x24, 0, 1)
	time.Sleep(30 * time.Microsecond)
	ana.mask(0x20, 0, 3)
	time.Sleep(time.Microsecond)
	for _, off := range []uint32{0x00, 0x04, 0x08, 0x0C, 0x10} {
		ana.mask(off, 0, 1)
	}
	s.wr(regNcsi2HsrxDbg, 0x1F)
	s.mask(regMipiRxCon38, 0, 1)
	s.wr(regMipiRxCon3C, 0x1541)
	s.mask(regMipiRxCon38, 0, 1<<2)
	time.Sleep(500 * time.Microsecond)
	if c44, c48 := s.rd(regMipiRxCon44), s.rd(regMipiRxCon48); c44&0x10001 == 0 || c48&0x101 == 0 {
		slog.Warn("camera: hsrx calibration did not apply", "con44", c44, "con48", c48)
	}
	s.mask(regMipiRxCon38, 1, 0)
	s.wr(regNcsi2HsrxDbg, 0)

	mux := s.rd(regSeninf1Mux)
	mux &^= 0xF<<12 | 1<<8 | 0x3<<28 | 0x3F<<22 | 0x3F<<16 | 1<<10 | 1<<9
	mux |= 1<<31 | 8<<12 | 1<<28 | 0x3B<<22 | 0x3F<<16
	s.wr(regSeninf1Mux, mux)
	s.mask(regSeninf1Ctrl, 0x7<<28|0xF<<12, 1|8<<12)
	s.wr(regNcsi2Dpcm, 0)
	s.wr(regSeninf1Ctrl, s.rd(regSeninf1Ctrl)&0xFFFF0FFF|0x8000)
	s.mask(regNcsi2Ctl, 0, 1<<7)
	settle := uint32(85*364/1000) & 0xFF
	s.wr(regNcsi2LnrdTim, settle<<8)
	s.mask(regNcsi2Ctl, 0, 1<<26|1<<16|1<<4|1)
	s.wr(regNcsi2IntEn, 0)
	s.mask(regSeninf1Mux, 0, 3)
	s.mask(regSeninf1Mux, 3, 0)
	s.mask(regSeninfTopMux, 0xF, 0)
}

// teardownCSI2 is the driver's disable path: receiver lanes off, D-PHY analog off, pads back to
// GPIO mode.
func teardownCSI2(s, ana *mmio) {
	s.mask(regNcsi2Ctl, 0x1F, 0)
	s.mask(regCsi2Ctrl, 0x1F, 0)
	ana.mask(0x24, 1, 0)
	ana.mask(0x20, 1, 0)
	for _, off := range []uint32{0x00, 0x04, 0x08, 0x0C, 0x10} {
		ana.mask(off, 1, 0)
	}
	ana.mask(0x4C, 0, 0x1041041)
	ana.mask(0x50, 0, 0x1041041)
	for _, off := range []uint32{0x00, 0x04, 0x08, 0x0C, 0x10} {
		ana.mask(off, 1<<3, 0)
	}
}

// stream runs the DMA over the slots until stop closes, calling frame with each completed
// frame's Bayer bytes. The TG frame counter (TG_INTER_ST bits 23:16) is the only sync that works
// with interrupts masked; on every tick the DMA is aimed at the next slot, so the one it just
// left is whole and stays untouched for slots-1 more periods.
func (d *device) stream(stop chan struct{}, frame func(bayer []byte)) {
	cam := d.cam
	cur := 0
	cam.wr(regImgoBase, d.mva)
	cam.mask(regTgVfCon, 0, 1) // VFDATA_EN
	defer cam.mask(regTgVfCon, 1, 0)
	last := cam.rd(regTgInterSt) >> 16 & 0xFF
	first := true
	quiet := time.Now()
	for {
		select {
		case <-stop:
			return
		default:
		}
		cnt := cam.rd(regTgInterSt) >> 16 & 0xFF
		if cnt == last {
			if time.Since(quiet) > 2*time.Second {
				slog.Warn("camera: no frames for 2s")
				quiet = time.Now()
			}
			time.Sleep(500 * time.Microsecond)
			continue
		}
		last = cnt
		quiet = time.Now()
		done := cur
		cur = (cur + 1) % slots
		cam.wr(regImgoBase, d.mva+uint32(cur*frameBytes))
		if first {
			first = false // the first tick is the start of the first frame, nothing is complete
			continue
		}
		frame(d.buf[done*frameBytes : (done+1)*frameBytes])
	}
}

// skip: every frame is converted on the Show.
func (d *device) skip() bool { return false }

// ---- conversion ----

// tone is how a frame was levelled: white balance gains and the value that maps to white.
type tone struct {
	gainR, gainB float64
	white        int     // on the 11-bit scale of a summed green pair
	black        int     // on the same scale: what maps to black (blackFor)
	gamma        float64 // output gamma: steeper for a backlit frame (gammaFor)
}

// unpackLine expands one packed line into 10-bit samples, four pixels from every five bytes,
// least significant bits first (the packer's order).
func unpackLine(line []byte, dst []uint16) {
	for i, j := 0, 0; i+5 <= len(line) && j+4 <= len(dst); i, j = i+5, j+4 {
		b0, b1, b2, b3, b4 := uint16(line[i]), uint16(line[i+1]), uint16(line[i+2]), uint16(line[i+3]), uint16(line[i+4])
		dst[j] = b0 | (b1&3)<<8
		dst[j+1] = b1>>2 | (b2&0xF)<<6
		dst[j+2] = b2>>4 | (b3&0x3F)<<4
		dst[j+3] = b3>>6 | b4<<2
	}
}

// convert turns one packed frame into a half-size RGBA picture and the tone it was levelled with.
func convert(raw []byte) (*image.RGBA, tone) {
	t := stats(raw)
	return render(raw, t), t
}

// stats levels one frame: grey-world white balance, the darkest 0.1% at black (within reason), the
// top percentile at white, gamma 1/1.8 or steeper for a backlit frame.
func stats(raw []byte) tone { return sample(raw, statsStep) }

// statsStep is how many cells the measurement moves on by. Levels and white balance come from a
// sixteenth of the frame; sampling every cell measures the same picture for four times the work.
const statsStep = 2

func sample(raw []byte, step int) tone {
	row0 := make([]uint16, sensorW)
	row1 := make([]uint16, sensorW)
	var sumR, sumG, sumB uint32
	var hist, centre [2048]int
	taken, middle := 0, 0
	for y := 0; y < Height; y += step {
		unpackLine(raw[(2*y)*bytesPerLine:(2*y+1)*bytesPerLine], row0)
		unpackLine(raw[(2*y+1)*bytesPerLine:(2*y+2)*bytesPerLine], row1)
		down := y >= Height/4 && y < Height*3/4
		for x := 0; x < Width; x += step {
			r := row0[2*x]
			g := row0[2*x+1] + row1[2*x]
			b := row1[2*x+1]
			if sensor.blueFirst {
				r, b = b, r
			}
			sumR += uint32(r)
			sumG += uint32(g)
			sumB += uint32(b)
			hist[g]++
			taken++
			if down && x >= Width/4 && x < Width*3/4 {
				centre[g]++
				middle++
			}
		}
	}
	n := float64(taken)
	t := tone{gainR: 1, gainB: 1, white: 1, gamma: gammaNormal}
	avgR, avgG, avgB := float64(sumR)*2/n, float64(sumG)/n, float64(sumB)*2/n
	if avgR > 1 && avgB > 1 {
		// Quantised so a steady scene keeps asking for the tables it already has.
		t.gainR = math.Round(avgG/avgR*256) / 256
		t.gainB = math.Round(avgG/avgB*256) / 256
	}
	seen, low := 0, -1
	for v := 0; v < len(hist); v++ {
		seen += hist[v]
		if low < 0 && seen >= taken/1000 {
			low = v
		}
		if seen >= taken*99/100 {
			t.white = v
			break
		}
	}
	median, half := 0, middle/2
	for v, seen := 0, 0; v < len(centre); v++ {
		if seen += centre[v]; seen >= half {
			median = v
			break
		}
	}
	if t.white < 32 {
		t.white = 32
	}
	t.gamma = gammaFor(median, t.white)
	t.black = blackFor(low, t.white)
	return t
}

// render paints the half-size picture: one pixel per Bayer cell, the two greens summed.
func render(raw []byte, t tone) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, Width, Height))
	lut := t.tables()
	row0 := make([]uint16, sensorW)
	row1 := make([]uint16, sensorW)
	for y := 0; y < Height; y++ {
		unpackLine(raw[(2*y)*bytesPerLine:(2*y+1)*bytesPerLine], row0)
		unpackLine(raw[(2*y+1)*bytesPerLine:(2*y+2)*bytesPerLine], row1)
		pix := img.Pix[y*img.Stride:]
		for x := 0; x < Width; x++ {
			r := row0[2*x]
			g := row0[2*x+1] + row1[2*x]
			b := row1[2*x+1]
			if sensor.blueFirst {
				r, b = b, r
			}
			j := x * 4
			pix[j] = lut[0][r<<1]
			pix[j+1] = lut[1][g]
			pix[j+2] = lut[2][b<<1]
			pix[j+3] = 255
		}
	}
	return img
}

// tables are per-channel lookups from an 11-bit level to an 8-bit output: gain, scale to white,
// gamma.
func (t tone) tables() *[3][2048]uint8 {
	lutCache.Lock()
	defer lutCache.Unlock()
	if lutCache.built != nil && lutCache.t == t {
		return lutCache.built
	}
	var lut [3][2048]uint8
	for ch, gain := range []float64{t.gainR, 1, t.gainB} {
		for v := 0; v < 2048; v++ {
			f := float64(v-t.black) * gain / float64(t.white-t.black)
			if f < 0 {
				f = 0
			}
			if f > 1 {
				f = 1
			}
			lut[ch][v] = uint8(math.Pow(f, t.gamma)*255 + 0.5)
		}
	}
	lutCache.t, lutCache.built = t, &lut
	return &lut
}

var lutCache struct {
	sync.Mutex
	t     tone
	built *[3][2048]uint8
}

// Full demosaics the frame at the sensor's own 1600x1200: bilinear, each pixel's missing two
// colours averaged from its neighbours, levelled the way the half-size picture was. It costs a
// few hundred milliseconds on this SoC, so it is for stills, not the stream.
func (f *Frame) Full() *image.RGBA {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.raw == nil {
		return f.rgba
	}
	w, h := sensorW, sensorH
	px := make([]uint16, w*h)
	for y := 0; y < h; y++ {
		unpackLine(f.raw[y*bytesPerLine:(y+1)*bytesPerLine], px[y*w:(y+1)*w])
	}
	at := func(x, y int) uint32 {
		if x < 0 {
			x = 1
		} else if x >= w {
			x = w - 2
		}
		if y < 0 {
			y = 1
		} else if y >= h {
			y = h - 2
		}
		return uint32(px[y*w+x])
	}
	luts := f.level().tables()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var r, g, b uint32
			c := at(x, y)
			switch (y&1)<<1 | x&1 {
			case 0: // red site
				r = c
				g = (at(x-1, y) + at(x+1, y) + at(x, y-1) + at(x, y+1)) / 4
				b = (at(x-1, y-1) + at(x+1, y-1) + at(x-1, y+1) + at(x+1, y+1)) / 4
			case 1: // green on a red row
				g = c
				r = (at(x-1, y) + at(x+1, y)) / 2
				b = (at(x, y-1) + at(x, y+1)) / 2
			case 2: // green on a blue row
				g = c
				b = (at(x-1, y) + at(x+1, y)) / 2
				r = (at(x, y-1) + at(x, y+1)) / 2
			default: // blue site
				b = c
				g = (at(x-1, y) + at(x+1, y) + at(x, y-1) + at(x, y+1)) / 4
				r = (at(x-1, y-1) + at(x+1, y-1) + at(x-1, y+1) + at(x+1, y+1)) / 4
			}
			if sensor.blueFirst {
				r, b = b, r
			}
			// The tables run on the 11-bit scale of a summed green pair; single samples double up.
			j := (y*w + x) * 4
			img.Pix[j] = luts[0][r<<1]
			img.Pix[j+1] = luts[1][g<<1]
			img.Pix[j+2] = luts[2][b<<1]
			img.Pix[j+3] = 255
		}
	}
	return img
}
