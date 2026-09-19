//go:build spot

// Package camera is the Echo Spot's front camera, driven without Android.
//
// The GC0312 is a VGA sensor on a parallel bus, not MIPI: eight data lines on the CSI0 pads (CMDAT9..2
// in the device tree's default pin state), HSYNC, VSYNC and a pixel clock. The board powers it as the
// sub sensor (pin set 1 in camera_hw/rook). Its frames come in through SENINF4 (parallel source,
// data on pads 9..2), the SENINF1 mux fed from it, TG1 and the packer, one byte a pixel, into the
// IMGO DMA. The settings that differ from the Show's MIPI path were found with cmd/spotcam: the pad
// input enables on all three CSI analog blocks, SENINF_TOP_CTRL with only SENINF1_PCLK_EN (the
// pixel clock select doubles every sample), and both sync polarities at 0.
//
// Frames are 640x480 RGGB at about 30 a second. Every other one is demosaiced (bilinear) to RGBA with
// grey-world white balance and levels; the sensor's automatic exposure is off in its init table, so
// the loop here sets shutter and gain.
package camera

import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"math"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Width and Height are the frames handed out: the sensor's own size.
const Width, Height = 640, 480

const (
	sensorW, sensorH = Width, Height
	frameBytes       = sensorW * sensorH // one byte a pixel
	slots            = 3

	// convertEvery: frames are demosaiced one in this many (about 15 a second). warmup: the first
	// frames after a start are dropped; the first DMA pass can be empty.
	convertEvery = 2
	warmup       = 3

	sensMagic   = 'i'
	nrOpen      = 0
	nrControl   = 20
	nrClose     = 25
	nrSetDriver = 35
	nrSetMCLK   = 60
	nrSetCur    = 70
	nrFeature   = 15
	// socketSub is DUAL_CAMERA_SUB_SENSOR: only that socket runs the GC0312's power sequence on rook.
	socketSub = 2

	featShutter = 3004 // lines, 6..4095
	featGain    = 3006 // 1/64

	ispMagic   = 'k'
	nrIspReset = 0

	ispBase, ispLen       = 0x15000000, 0x10000
	seninfBase, seninfLen = 0x15008000, 0x4000
	mipiBase, mipiLen     = 0x10217000, 0x3000

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

	regSeninfTopCtrl = 0x0000
	regSeninfTopMux  = 0x0008
	regSeninf1Ctrl   = 0x0100
	regSeninf1Mux    = 0x0120
	regTG1PhCnt      = 0x0200
	regTG1SenCk      = 0x0204
	regSeninf4Ctrl   = 0x0D00
	regSeninf4Mux    = 0x0D20

	// padInputs are the GPI input enables of the CSI analog blocks, set for a parallel sensor.
	padInputs = 0x1041041

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
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(arg)); e != 0 {
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

func (m *mmio) rd(off uint32) uint32        { return *(*uint32)(unsafe.Pointer(&m.mem[off])) }
func (m *mmio) wr(off, val uint32)          { *(*uint32)(unsafe.Pointer(&m.mem[off])) = val }
func (m *mmio) mask(off, clear, set uint32) { m.wr(off, m.rd(off)&^clear|set) }
func (m *mmio) unmap() {
	if m != nil && m.mem != nil {
		syscall.Munmap(m.mem)
	}
}

type device struct {
	isp, sens, ion int
	cam, sen, mipi *mmio
	buf            []byte
	mva            uint32
	mclk           [3]uint32
	sensorOpen     bool

	shutter, gain int
	settle        int
	n             int // frames seen, for convertEvery
}

func open() (*device, error) {
	d := &device{isp: -1, sens: -1, ion: -1}
	fail := func(err error) (*device, error) {
		d.close()
		return nil, err
	}
	var err error
	if d.isp, err = syscall.Open("/dev/camera-isp", syscall.O_RDWR, 0); err != nil {
		return nil, fmt.Errorf("open camera-isp: %w", err)
	}
	if d.cam, err = mapWindow(d.isp, ispBase, ispLen); err != nil {
		return fail(err)
	}
	if d.sen, err = mapWindow(d.isp, seninfBase, seninfLen); err != nil {
		return fail(err)
	}
	if d.mipi, err = mapWindow(d.isp, mipiBase, mipiLen); err != nil {
		return fail(err)
	}
	if d.sens, err = syscall.Open("/dev/kd_camera_hw", syscall.O_RDWR, 0); err != nil {
		return fail(fmt.Errorf("open kd_camera_hw: %w", err))
	}

	// Sensor master clock: the 48 MHz camtg group divided by 2 (24 MHz, what the driver asks for).
	d.mclk = [3]uint32{1, 1, 0}
	if err := ioctl(d.sens, ioc(3, sensMagic, nrSetMCLK, 12), unsafe.Pointer(&d.mclk[0])); err != nil {
		return fail(fmt.Errorf("SET_MCLK_PLL: %w", err))
	}
	setMCLK1(d.sen, 1)

	if err := d.allocBuffers(); err != nil {
		return fail(err)
	}
	if err := m4uConfig(m4uPortIMGO); err != nil {
		return fail(err)
	}

	idx := [2]uint32{socketSub << 16, 0}
	if err := ioctl(d.sens, ioc(3, sensMagic, nrSetDriver, 8), unsafe.Pointer(&idx[0])); err != nil {
		return fail(fmt.Errorf("SET_DRIVER: %w", err))
	}
	cur := uint32(socketSub)
	ioctl(d.sens, ioc(3, sensMagic, nrSetCur, 4), unsafe.Pointer(&cur))
	if err := ioctl(d.sens, ioc(0, sensMagic, nrOpen, 0), nil); err != nil {
		return fail(fmt.Errorf("sensor open: %w", err))
	}
	d.sensorOpen = true

	d.setupISP()
	setupParallel(d.sen, d.mipi)

	var window, config [256]byte
	ctl := [4]uint32{socketSub, 0, uint32(uintptr(unsafe.Pointer(&window[0]))), uint32(uintptr(unsafe.Pointer(&config[0])))}
	if err := ioctl(d.sens, ioc(3, sensMagic, nrControl, 16), unsafe.Pointer(&ctl[0])); err != nil {
		return fail(fmt.Errorf("sensor control: %w", err))
	}
	d.shutter, d.gain = 400, 96
	d.setFeature(featShutter, uint64(d.shutter))
	d.setFeature(featGain, uint64(d.gain))
	d.settle = aeDelay
	return d, nil
}

// setFeature is KDIMGSENSORIOC_X_FEATURECONCTROL with one integer parameter (eight bytes: the kernel
// is 64-bit).
func (d *device) setFeature(id uint32, v uint64) {
	var para [8]byte
	for i := range para {
		para[i] = byte(v >> (8 * i))
	}
	size := uint32(len(para))
	ctl := [4]uint32{socketSub, id, uint32(uintptr(unsafe.Pointer(&para[0]))), uint32(uintptr(unsafe.Pointer(&size)))}
	if err := ioctl(d.sens, ioc(3, sensMagic, nrFeature, 16), unsafe.Pointer(&ctl[0])); err != nil {
		slog.Warn("camera: sensor feature", "id", id, "err", err)
	}
}

func (d *device) close() {
	if d.cam != nil {
		d.cam.mask(regTgVfCon, 1, 0)
	}
	if d.sensorOpen {
		ioctl(d.sens, ioc(0, sensMagic, nrClose, 0), nil)
	}
	if d.mipi != nil {
		for _, base := range []uint32{0x0000, 0x1000, 0x2000} {
			d.mipi.mask(base+0x4C, padInputs, 0)
			d.mipi.mask(base+0x50, padInputs, 0)
		}
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

func (d *device) allocBuffers() error {
	var err error
	if d.ion, err = syscall.Open("/dev/ion", syscall.O_RDWR, 0); err != nil {
		return fmt.Errorf("open ion: %w", err)
	}
	size := (slots*frameBytes + 4095) &^ 4095
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

// setupISP programs pass 1 for 8-bit output: TG1 -> packer -> IMGO, interrupts masked.
func (d *device) setupISP() {
	cam := d.cam
	ioctl(d.isp, ioc(0, ispMagic, nrIspReset, 0), nil)
	cam.wr(regCtlIntEn, 0)
	cam.wr(regTgVfCon, 0)
	cam.wr(regCtlClkEn, 0x1FFFF)
	cam.wr(regCtlEn1, 1<<0|1<<12) // TG1_EN | PAK_EN
	cam.wr(regCtlEn2, 0)
	cam.wr(regCtlDmaEn, 1<<0) // IMGO_EN
	cam.wr(regCtlFmtSel, 1<<16)
	cam.wr(regCtlSel, 0)
	cam.wr(regCtlPixID, 0) // R first
	cam.mask(regCtlMuxSel2, 1<<4, 1<<18)
	cam.wr(regImgoFbc, 0)
	cam.wr(regImgoBase, d.mva)
	cam.wr(regImgoOfst, 0)
	cam.wr(regImgoXsize, sensorW-1)
	cam.wr(regImgoYsize, sensorH-1)
	cam.wr(regImgoStride, sensorW)
	cam.wr(regCtlImgoSize, uint32(sensorW)<<16|uint32(sensorH))
	if cam.rd(regImgoCon) == 0 {
		cam.wr(regImgoCon, 0x80000040)
		cam.wr(regImgoCon2, 0x00200020)
	}
	cam.wr(regTgSenMode, 1<<0|1<<2)
	cam.wr(regTgVfCon, 1<<12)
	cam.wr(regTgGrabPxl, uint32(sensorW)<<16)
	cam.wr(regTgGrabLin, uint32(sensorH)<<16)
	cam.wr(regTgPathCfg, 0)
}

// setupParallel is SeninfDrvImp's parallel path (setSeninf4Ctrl, setSeninf4Parallel, the mux, the top
// mux) with the values the Spot needs.
func setupParallel(s, ana *mmio) {
	for _, base := range []uint32{0x0000, 0x1000, 0x2000} {
		ana.mask(base+0x4C, 0, padInputs)
		ana.mask(base+0x50, 0, padInputs)
	}
	// SENINF4: enabled, source 3 (parallel), PAD2CAM_DATA_SEL 4 (eight bits on data 9..2).
	s.mask(regSeninf4Ctrl, 0x7<<28|0xF<<12, 1|3<<12|4<<28)
	m4 := s.rd(regSeninf4Mux)
	s.wr(regSeninf4Mux, m4|3)
	s.wr(regSeninf4Mux, m4&^3)
	// Mux 1: enabled, parallel source, FIFO flush 0x1B / push 0x1F, full write on, sync polarities 0.
	mux := s.rd(regSeninf1Mux)
	mux &^= 0xF<<12 | 1<<8 | 0x3<<28 | 0x3F<<22 | 0x3F<<16 | 1<<10 | 1<<9 | 1<<7
	mux |= 1<<31 | 3<<12 | 1<<28 | 0x1B<<22 | 0x1F<<16
	s.wr(regSeninf1Mux, mux)
	s.mask(regSeninf1Mux, 0, 3)
	s.mask(regSeninf1Mux, 3, 0)
	s.mask(regSeninfTopMux, 0xF, 3) // mux 1 <- SENINF4
	// The pixel clock: SENINF1_PCLK_EN only (bit 10); PCLK_SEL doubles every sample.
	s.mask(regSeninfTopCtrl, 0xF00, 1<<10)
}

// stream is the Show's: the DMA runs over the slots, aimed at the next one on every TG frame tick.
func (d *device) stream(stop chan struct{}, frame func(bayer []byte)) {
	cam := d.cam
	cur := 0
	cam.wr(regImgoBase, d.mva)
	cam.mask(regTgVfCon, 0, 1)
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
			time.Sleep(time.Millisecond)
			continue
		}
		last = cnt
		quiet = time.Now()
		done := cur
		cur = (cur + 1) % slots
		cam.wr(regImgoBase, d.mva+uint32(cur*frameBytes))
		if first {
			first = false
			continue
		}
		frame(d.buf[done*frameBytes : (done+1)*frameBytes])
	}
}

// skip reports whether this frame is left unconverted (convertEvery); exposure still sees it.
func (d *device) skip() bool {
	d.n++
	return d.n <= warmup || d.n%convertEvery != 0
}

// Exposure: shutter in lines (504 is one frame at 30 a second; longer ones slow the frame rate) and
// gain in 1/64, moved together towards a mean of the raw frame.
const (
	aeTarget   = 58 // mean of the 8-bit raw frame
	aeDeadband = 4
	aeDelay    = 2
	aeMinShut  = 6
	aeFrame    = 500
	aeMaxShut  = 1500 // 10 frames a second
	aeMinGain  = 64
	aeMidGain  = 256
	aeMaxGain  = 1024
)

func meter(raw []byte) int {
	sum, n := 0, 0
	for y := 8; y < sensorH-8; y += 6 {
		row := raw[y*sensorW : (y+1)*sensorW]
		for x := 8; x < sensorW-8; x += 5 {
			sum += int(row[x])
			n++
		}
	}
	return sum / max(n, 1)
}

func (d *device) autoExpose(raw []byte) {
	if d.settle > 0 {
		d.settle--
		return
	}
	mean := meter(raw)
	if mean >= aeTarget-aeDeadband && mean <= aeTarget+aeDeadband {
		return
	}
	ratio := math.Min(math.Max(float64(aeTarget)/float64(max(mean, 1)), 0.5), 2)
	want := float64(d.shutter*d.gain) * ratio
	shutter, gain := d.shutter, d.gain
	switch {
	case want <= float64(aeFrame*aeMinGain):
		shutter, gain = int(want/aeMinGain), aeMinGain
	case want <= float64(aeFrame*aeMidGain):
		shutter, gain = aeFrame, int(want/aeFrame)
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

// ---- conversion ----

// tone is how a frame was levelled.
type tone struct {
	gainR, gainB float64
	black, white int // 8-bit raw levels mapped to black and white
}

// convert demosaics one RGGB frame to RGBA at full size.
func convert(raw []byte) (*image.RGBA, tone) {
	t := levels(raw)
	return demosaic(raw, t), t
}

// levels measures grey-world white balance on the 2x2 cells, and the black and white points from the
// green histogram (0.2 % and 99.5 %).
func levels(raw []byte) tone {
	var sumR, sumG, sumB uint64
	var hist [256]int
	n := 0
	for y := 0; y+1 < sensorH; y += 2 {
		r0 := raw[y*sensorW : (y+1)*sensorW]
		r1 := raw[(y+1)*sensorW : (y+2)*sensorW]
		for x := 0; x+1 < sensorW; x += 2 {
			sumR += uint64(r0[x])
			g := (uint32(r0[x+1]) + uint32(r1[x])) / 2
			sumG += uint64(g)
			sumB += uint64(r1[x+1])
			hist[g]++
			n++
		}
	}
	t := tone{gainR: 1, gainB: 1, black: 0, white: 255}
	if sumR > 0 && sumB > 0 {
		t.gainR = math.Min(float64(sumG)/float64(sumR), 3)
		t.gainB = math.Min(float64(sumG)/float64(sumB), 3)
	}
	seen, low := 0, -1
	for v := 0; v < 256; v++ {
		seen += hist[v]
		if low < 0 && seen >= n/500 {
			low = v
		}
		if seen >= n*995/1000 {
			t.white = v
			break
		}
	}
	t.black = min(max(low, 0), 16)
	if t.white < t.black+24 {
		t.white = t.black + 24
	}
	return t
}

// lut maps a raw level (times 4, so a channel gain can carry fractions) to 8-bit output.
func (t tone) lut(gain float64) *[1024]uint8 {
	var l [1024]uint8
	span := float64(t.white - t.black)
	for v := 0; v < 1024; v++ {
		f := (float64(v)/4*gain - float64(t.black)) / span
		f = math.Min(math.Max(f, 0), 1)
		l[v] = uint8(math.Pow(f, 1/2.2)*255 + 0.5)
	}
	return &l
}

// demosaic is bilinear on RGGB: each pixel's missing colours are averaged from its neighbours.
// Values carry two extra bits (times 4) into the lookups.
func demosaic(raw []byte, t tone) *image.RGBA {
	w, h := sensorW, sensorH
	lr, lg, lb := t.lut(t.gainR), t.lut(1), t.lut(t.gainB)
	img := image.NewRGBA(image.Rect(0, 0, w, h))
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
		return uint32(raw[y*w+x])
	}
	for y := 0; y < h; y++ {
		inner := y > 0 && y < h-1
		for x := 0; x < w; x++ {
			var r, g, b uint32
			i := y*w + x
			if inner && x > 0 && x < w-1 {
				c := uint32(raw[i])
				up, dn, lf, rt := uint32(raw[i-w]), uint32(raw[i+w]), uint32(raw[i-1]), uint32(raw[i+1])
				switch (y&1)<<1 | x&1 {
				case 0:
					r, g, b = c*4, up+dn+lf+rt, uint32(raw[i-w-1])+uint32(raw[i-w+1])+uint32(raw[i+w-1])+uint32(raw[i+w+1])
				case 1:
					g, r, b = c*4, (lf+rt)*2, (up+dn)*2
				case 2:
					g, b, r = c*4, (lf+rt)*2, (up+dn)*2
				default:
					b, g, r = c*4, up+dn+lf+rt, uint32(raw[i-w-1])+uint32(raw[i-w+1])+uint32(raw[i+w-1])+uint32(raw[i+w+1])
				}
			} else {
				c := at(x, y)
				switch (y&1)<<1 | x&1 {
				case 0:
					r, g, b = c*4, at(x-1, y)+at(x+1, y)+at(x, y-1)+at(x, y+1), at(x-1, y-1)+at(x+1, y-1)+at(x-1, y+1)+at(x+1, y+1)
				case 1:
					g, r, b = c*4, (at(x-1, y)+at(x+1, y))*2, (at(x, y-1)+at(x, y+1))*2
				case 2:
					g, b, r = c*4, (at(x-1, y)+at(x+1, y))*2, (at(x, y-1)+at(x, y+1))*2
				default:
					b, g, r = c*4, at(x-1, y)+at(x+1, y)+at(x, y-1)+at(x, y+1), at(x-1, y-1)+at(x+1, y-1)+at(x-1, y+1)+at(x+1, y+1)
				}
			}
			j := i * 4
			img.Pix[j] = lr[min(r, 1023)]
			img.Pix[j+1] = lg[min(g, 1023)]
			img.Pix[j+2] = lb[min(b, 1023)]
			img.Pix[j+3] = 255
		}
	}
	return img
}

// Full is the frame at full size: the same picture as RGBA, which is already the sensor's size.
func (f *Frame) Full() *image.RGBA {
	if f.RGBA == nil && f.raw != nil {
		f.RGBA = demosaic(f.raw, f.tone)
	}
	return f.RGBA
}

func Available() bool { return availableVendor() }
