//go:build linux

// Command spotcamcheck is a camera check for an Echo Spot whose camera gives no frames. It runs the
// daemon's own sequence (hardware/camera/camera_spot.go) for the GC0312 on its parallel bus, then
// reports what moved: the receiver's registers before and after, the frame size the receiver
// measured, and whether the TG frame counter ticks, then the same with the pixel clock edge and the
// sync polarities swapped. A frame that arrives is saved as a gray PNG: a picture of the room.
//
//	spotcamcheck [-out /tmp/spotcam-check]
//
// The daemon must not be using the camera while it runs; the check refuses to start if another
// process holds the camera's devices. It writes the report to stdout and <out>.txt as it goes, and
// ignores Ctrl-C and a dropped SSH session for the 15 s it runs, so the camera is always powered off.
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

const (
	w, h       = 640, 480
	frameBytes = w * h

	sensMagic   = 'i'
	nrOpen      = 0
	nrControl   = 20
	nrClose     = 25
	nrSetDriver = 35
	nrSetMCLK   = 60
	nrSetCur    = 70
	nrFeature   = 15
	socketSub   = 2

	featShutter = 3004
	featGain    = 3006

	ispMagic   = 'k'
	nrIspReset = 0

	ispBase, ispLen       = 0x15000000, 0x10000
	seninfBase, seninfLen = 0x15008000, 0x4000
	mipiBase, mipiLen     = 0x10217000, 0x3000

	padInputs = 0x1041041

	ionHeapMultimedia = 10
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

// heapNew and ptr32: the compat ioctls carry pointers as uint32 fields, so what they point at has to
// live on the heap (see hardware/camera/camera_common.go).
func heapNew[T any]() *T {
	p := new(T)
	escape(unsafe.Pointer(p))
	return p
}

var escape = func(unsafe.Pointer) {}

func ptr32[T any](p *T) uint32 { return uint32(uintptr(unsafe.Pointer(p))) }

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

// variant is one way of setting up the receiver; the first is the daemon's.
type variant struct {
	name          string
	pclkInv       uint32 // TG1_PH_CNT PADCLK_INV
	hpol, vpol    uint32 // SENINF1_MUX sync polarities
	topCtrl       uint32 // SENINF_TOP_CTRL bits 8..11
	shutter, gain int
}

var variants = []variant{
	{name: "daemon settings", topCtrl: 1 << 10, shutter: 400, gain: 96},
	{name: "pixel clock inverted", pclkInv: 1, topCtrl: 1 << 10, shutter: 400, gain: 96},
	{name: "HSYNC inverted", hpol: 1, topCtrl: 1 << 10, shutter: 400, gain: 96},
	{name: "VSYNC inverted", vpol: 1, topCtrl: 1 << 10, shutter: 400, gain: 96},
	{name: "both syncs inverted", hpol: 1, vpol: 1, topCtrl: 1 << 10, shutter: 400, gain: 96},
	{name: "pixel clock select on", topCtrl: 1<<10 | 1<<8, shutter: 400, gain: 96},
}

type checker struct {
	out            io.Writer
	isp, sens, ion int
	cam, sen, mipi *mmio
	buf            []byte
	mva            uint32
	prefix         string
}

func main() {
	prefix := flag.String("out", "/tmp/spotcam-check", "file prefix for the report and any frames")
	flag.Parse()

	// Stopped half way, the sensor would stay powered and the DMA running into a freed buffer. A
	// dropped SSH session is a hang-up, or a broken pipe on the next line written.
	signal.Ignore(syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT, syscall.SIGPIPE)

	out := both{}
	f, ferr := os.OpenFile(*prefix+".txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if ferr != nil {
		fmt.Println("the report will not be saved:", ferr)
	} else {
		out.file = f
	}
	err := run(out, *prefix)
	if err != nil {
		fmt.Fprintln(out, "stopped:", err)
	}
	if f != nil {
		fmt.Fprintln(out, "\nreport saved to", *prefix+".txt")
		f.Close()
	}
	fmt.Println("Any .png saved next to it is a picture of the room: look before sharing it.")
	if err != nil {
		os.Exit(1)
	}
}

// both writes to the terminal and the report. A terminal that went away does not stop the report.
type both struct{ file *os.File }

func (b both) Write(p []byte) (int, error) {
	os.Stdout.Write(p)
	if b.file != nil {
		return b.file.Write(p)
	}
	return len(p), nil
}

func run(out io.Writer, prefix string) error {
	fmt.Fprintln(out, "spotcamcheck", time.Now().UTC().Format(time.RFC3339))
	if v, err := os.ReadFile("/proc/version"); err == nil {
		fmt.Fprint(out, "kernel: ", string(v))
	}
	if holders := camHolders(); len(holders) > 0 {
		return fmt.Errorf("the camera is in use by %s; close camera views and turn presence off, then run again",
			strings.Join(holders, ", "))
	}
	c := &checker{out: out, isp: -1, sens: -1, ion: -1, prefix: prefix}
	defer c.close()
	if err := c.open(); err != nil {
		return err
	}
	c.dump("at rest, before any setup")
	for i, v := range variants {
		if err := c.try(i, v); err != nil {
			return err
		}
	}
	return nil
}

// camHolders lists the processes other than this one with the camera's devices open.
func camHolders() []string {
	var who []string
	self := fmt.Sprint(os.Getpid())
	fds, _ := filepath.Glob("/proc/[0-9]*/fd/*")
	seen := map[string]bool{}
	for _, fd := range fds {
		pid := strings.Split(fd, "/")[2]
		if pid == self || seen[pid] {
			continue
		}
		if t, err := os.Readlink(fd); err == nil && (t == "/dev/camera-isp" || t == "/dev/kd_camera_hw") {
			seen[pid] = true
			comm, _ := os.ReadFile("/proc/" + pid + "/comm")
			who = append(who, strings.TrimSpace(string(comm))+" ("+pid+")")
		}
	}
	return who
}

func (c *checker) open() error {
	var err error
	if c.isp, err = syscall.Open("/dev/camera-isp", syscall.O_RDWR|syscall.O_CLOEXEC, 0); err != nil {
		return fmt.Errorf("open camera-isp: %w", err)
	}
	if c.cam, err = mapWindow(c.isp, ispBase, ispLen); err != nil {
		return err
	}
	if c.sen, err = mapWindow(c.isp, seninfBase, seninfLen); err != nil {
		return err
	}
	if c.mipi, err = mapWindow(c.isp, mipiBase, mipiLen); err != nil {
		return err
	}
	if c.sens, err = syscall.Open("/dev/kd_camera_hw", syscall.O_RDWR|syscall.O_CLOEXEC, 0); err != nil {
		return fmt.Errorf("open kd_camera_hw: %w", err)
	}
	return c.allocBuffers()
}

func (c *checker) close() {
	if c.sens >= 0 {
		syscall.Close(c.sens)
	}
	if c.buf != nil {
		unix.Munmap(c.buf)
	}
	if c.ion >= 0 {
		syscall.Close(c.ion)
	}
	if c.isp >= 0 {
		syscall.Close(c.isp)
	}
}

func (c *checker) allocBuffers() error {
	var err error
	if c.ion, err = syscall.Open("/dev/ion", syscall.O_RDWR|syscall.O_CLOEXEC, 0); err != nil {
		return fmt.Errorf("open ion: %w", err)
	}
	size := (frameBytes + 4095) &^ 4095
	a := ionAlloc{Len: uint32(size), Align: 4096, HeapIDMask: 1 << ionHeapMultimedia}
	if err := ioctl(c.ion, ioc(3, 'I', 0, unsafe.Sizeof(a)), unsafe.Pointer(&a)); err != nil {
		return fmt.Errorf("ion alloc: %w", err)
	}
	share := ionFd{Handle: a.Handle}
	if err := ioctl(c.ion, ioc(3, 'I', 4, unsafe.Sizeof(share)), unsafe.Pointer(&share)); err != nil {
		return fmt.Errorf("ion share: %w", err)
	}
	c.buf, err = unix.Mmap(int(share.Fd), 0, size, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	syscall.Close(int(share.Fd))
	if err != nil {
		return fmt.Errorf("mmap ion buffer: %w", err)
	}
	cfg := heapNew[ionMMData]()
	*cfg = ionMMData{Config: ionMMConfig{Handle: a.Handle, ModuleID: m4uPortIMGO}}
	cc := ionCustom{Cmd: 1, Arg: ptr32(cfg)}
	err = ioctl(c.ion, ioc(3, 'I', 6, unsafe.Sizeof(cc)), unsafe.Pointer(&cc))
	runtime.KeepAlive(cfg)
	if err != nil {
		return fmt.Errorf("ion config buffer: %w", err)
	}
	phys := heapNew[ionSysData]()
	*phys = ionSysData{SysCmd: 1, Phys: ionSysGetPhysP{Handle: a.Handle}}
	cc = ionCustom{Cmd: 0, Arg: ptr32(phys)}
	err = ioctl(c.ion, ioc(3, 'I', 6, unsafe.Sizeof(cc)), unsafe.Pointer(&cc))
	runtime.KeepAlive(phys)
	if err != nil {
		return fmt.Errorf("ion get phys: %w", err)
	}
	if phys.Phys.PhyAddr == 0 {
		return fmt.Errorf("ion gave no device address for the IMGO port")
	}
	c.mva = phys.Phys.PhyAddr
	fd, err := syscall.Open("/proc/m4u", syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open /proc/m4u: %w", err)
	}
	defer syscall.Close(fd)
	p := m4uPort{PortID: m4uPortIMGO, Virtuality: 1, Distance: 1}
	if err := ioctl(fd, ioc(1, 'g', 11, 4), unsafe.Pointer(&p)); err != nil {
		return fmt.Errorf("m4u config port: %w", err)
	}
	return nil
}

// feature is KDIMGSENSORIOC_X_FEATURECONCTROL with an eight-byte parameter, returned as the driver
// left it.
func (c *checker) feature(id uint32, v uint64) (uint64, error) {
	para, size := heapNew[[8]byte](), heapNew[uint32]()
	for i := range para {
		para[i] = byte(v >> (8 * i))
	}
	*size = uint32(len(para))
	ctl := [4]uint32{socketSub, id, ptr32(para), ptr32(size)}
	err := ioctl(c.sens, ioc(3, sensMagic, nrFeature, 16), unsafe.Pointer(&ctl[0]))
	runtime.KeepAlive(para)
	runtime.KeepAlive(size)
	var r uint64
	for i := range para {
		r |= uint64(para[i]) << (8 * i)
	}
	return r, err
}

// powerOn starts the master clock (the PLL, then the TG1 divider the sensor's MCLK pin runs from:
// without it the sensor answers its id read with 0) and opens the sensor on the sub socket. A
// failure stops the clock again.
func (c *checker) powerOn(padInv uint32) (err error) {
	mclk := heapNew[[3]uint32]()
	*mclk = [3]uint32{1, 1, 0}
	if err := ioctl(c.sens, ioc(3, sensMagic, nrSetMCLK, 12), unsafe.Pointer(&mclk[0])); err != nil {
		return fmt.Errorf("SET_MCLK_PLL: %w", err)
	}
	defer func() {
		if err != nil {
			mclk[0] = 0
			ioctl(c.sens, ioc(3, sensMagic, nrSetMCLK, 12), unsafe.Pointer(&mclk[0]))
		}
	}()
	c.setMCLK1(padInv)
	idx := heapNew[[2]uint32]()
	*idx = [2]uint32{socketSub << 16, 0}
	if err := ioctl(c.sens, ioc(3, sensMagic, nrSetDriver, 8), unsafe.Pointer(&idx[0])); err != nil {
		return fmt.Errorf("SET_DRIVER: %w", err)
	}
	cur := heapNew[uint32]()
	*cur = socketSub
	ioctl(c.sens, ioc(3, sensMagic, nrSetCur, 4), unsafe.Pointer(cur))
	if err := ioctl(c.sens, ioc(0, sensMagic, nrOpen, 0), nil); err != nil {
		return fmt.Errorf("sensor open: %w", err)
	}
	return nil
}

func (c *checker) powerOff() {
	c.cam.mask(0x4414, 1, 0)
	ioctl(c.sens, ioc(0, sensMagic, nrClose, 0), nil)
	for _, base := range []uint32{0x0000, 0x1000, 0x2000} {
		c.mipi.mask(base+0x4C, padInputs, 0)
		c.mipi.mask(base+0x50, padInputs, 0)
	}
	mclk := heapNew[[3]uint32]()
	ioctl(c.sens, ioc(3, sensMagic, nrSetMCLK, 12), unsafe.Pointer(&mclk[0]))
}

func (c *checker) setMCLK1(padInv uint32) {
	const clkcnt = 1
	s := c.sen
	s.mask(0x0200, 1<<31, 1<<31)
	s.mask(0x0000, 0xc00, 0x300)
	s.mask(0x0204, 0x3f|0x3f00|0x3f0000, 1|clkcnt<<16)
	s.mask(0x0200, 0x3|0x4|1<<6|1<<28, 1|0<<2|padInv<<6|1<<28)
	s.mask(0x0120, 1<<31, 1<<31)
	s.mask(0x0100, 1<<31, 1)
	s.mask(0x0200, 1<<29, 1<<29)
}

func (c *checker) setupISP() {
	cam := c.cam
	ioctl(c.isp, ioc(0, ispMagic, nrIspReset, 0), nil)
	cam.wr(0x4020, 0)
	cam.wr(0x4414, 0)
	cam.wr(0x4150, 0x1FFFF)
	cam.wr(0x4004, 1<<0|1<<12)
	cam.wr(0x4008, 0)
	cam.wr(0x400C, 1)
	cam.wr(0x4010, 1<<16)
	cam.wr(0x4018, 0)
	cam.wr(0x401C, 0)
	cam.mask(0x4078, 1<<4, 1<<18)
	cam.wr(0x40F4, 0)
	cam.wr(0x4300, c.mva)
	cam.wr(0x4304, 0)
	cam.wr(0x4308, w-1)
	cam.wr(0x430C, h-1)
	cam.wr(0x4310, w)
	cam.wr(0x414C, w<<16|h)
	if cam.rd(0x4314) == 0 {
		cam.wr(0x4314, 0x80000040)
		cam.wr(0x4318, 0x00200020)
	}
	cam.wr(0x4410, 1<<0|1<<2)
	cam.wr(0x4414, 1<<12)
	cam.wr(0x4418, w<<16)
	cam.wr(0x441C, h<<16)
	cam.wr(0x4420, 0)
}

func (c *checker) setupParallel(v variant) {
	s := c.sen
	for _, base := range []uint32{0x0000, 0x1000, 0x2000} {
		c.mipi.mask(base+0x4C, 0, padInputs)
		c.mipi.mask(base+0x50, 0, padInputs)
	}
	s.mask(0x0D00, 0x7<<28|0xF<<12, 1|3<<12|4<<28)
	m4 := s.rd(0x0D20)
	s.wr(0x0D20, m4|3)
	s.wr(0x0D20, m4&^3)
	mux := s.rd(0x0120)
	mux &^= 0xF<<12 | 1<<8 | 0x3<<28 | 0x3F<<22 | 0x3F<<16 | 1<<10 | 1<<9 | 1<<7
	mux |= 1<<31 | 3<<12 | 1<<28 | 0x1B<<22 | 0x1F<<16 | v.hpol<<10 | v.vpol<<9
	s.wr(0x0120, mux)
	s.mask(0x0120, 0, 3)
	s.mask(0x0120, 3, 0)
	s.mask(0x0008, 0xF, 3)
	s.mask(0x0000, 0xF00, v.topCtrl)
}

// try powers the sensor, sets the receiver up one way, and watches for 2 s.
func (c *checker) try(i int, v variant) error {
	fmt.Fprintf(c.out, "\n=== %d: %s ===\n", i+1, v.name)
	if err := c.powerOn(v.pclkInv); err != nil {
		return err
	}
	defer c.powerOff()
	c.setupISP()
	c.setupParallel(v)
	window, config := heapNew[[256]byte](), heapNew[[256]byte]()
	ctl := heapNew[[4]uint32]()
	*ctl = [4]uint32{socketSub, 0, ptr32(window), ptr32(config)}
	err := ioctl(c.sens, ioc(3, sensMagic, nrControl, 16), unsafe.Pointer(&ctl[0]))
	runtime.KeepAlive(window)
	runtime.KeepAlive(config)
	if err != nil {
		return fmt.Errorf("sensor control: %w", err)
	}
	c.feature(featShutter, uint64(v.shutter))
	c.feature(featGain, uint64(v.gain))
	clear(c.buf)
	before := c.snap()
	c.cam.wr(0x4300, c.mva)
	c.cam.mask(0x4414, 0, 1)
	last := c.cam.rd(0x444C) >> 16 & 0xFF
	ticks := 0
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cnt := c.cam.rd(0x444C) >> 16 & 0xFF; cnt != last {
			last = cnt
			ticks++
		}
		time.Sleep(time.Millisecond)
	}
	after := c.snap()
	c.cam.mask(0x4414, 1, 0)
	var sum, nz int
	for _, b := range c.buf[:frameBytes] {
		sum += int(b)
		if b != 0 {
			nz++
		}
	}
	fmt.Fprintf(c.out, "TG frame ticks in 2 s: %d (a working Spot: about 31); buffer mean %d, nonzero bytes %d of %d\n",
		ticks, sum/frameBytes, nz, frameBytes)
	// SENINF1's line counter: pixels a line in the high half, lines a frame in the low (a working
	// Spot: 640 x 480). Zero means no sync or pixel clock reached the receiver.
	m := c.sen.rd(0x0134)
	fmt.Fprintf(c.out, "receiver measured %d pixels x %d lines\n", m>>16, m&0xffff)
	c.changed(before, after)
	if i == 0 {
		c.dump("set up with the daemon's settings, after 2 s")
	}
	if nz > 0 {
		name := fmt.Sprintf("%s-%d.png", c.prefix, i+1)
		if err := savePNG(name, c.buf[:frameBytes]); err == nil {
			fmt.Fprintln(c.out, "frame saved:", name)
		}
	}
	return nil
}

type region struct {
	name     string
	m        func(*checker) *mmio
	from, to uint32
}

var regions = []region{
	{"seninf top", func(c *checker) *mmio { return c.sen }, 0x0000, 0x0010},
	{"seninf1", func(c *checker) *mmio { return c.sen }, 0x0100, 0x0150},
	{"tg1 clock", func(c *checker) *mmio { return c.sen }, 0x0200, 0x0210},
	{"seninf4", func(c *checker) *mmio { return c.sen }, 0x0D00, 0x0D50},
	{"cam ctl", func(c *checker) *mmio { return c.cam }, 0x4000, 0x4030},
	{"cam tg", func(c *checker) *mmio { return c.cam }, 0x4410, 0x4460},
	{"imgo", func(c *checker) *mmio { return c.cam }, 0x4300, 0x4320},
	{"csi0 pads", func(c *checker) *mmio { return c.mipi }, 0x0040, 0x0060},
	{"csi1 pads", func(c *checker) *mmio { return c.mipi }, 0x1040, 0x1060},
	{"csi2 pads", func(c *checker) *mmio { return c.mipi }, 0x2040, 0x2060},
}

func (c *checker) snap() map[string]uint32 {
	s := map[string]uint32{}
	for _, r := range regions {
		m := r.m(c)
		for off := r.from; off < r.to; off += 4 {
			s[fmt.Sprintf("%s %#05x", r.name, off)] = m.rd(off)
		}
	}
	return s
}

func (c *checker) dump(title string) {
	fmt.Fprintf(c.out, "--- registers, %s ---\n", title)
	for _, r := range regions {
		m := r.m(c)
		fmt.Fprintf(c.out, "%-10s", r.name)
		for off := r.from; off < r.to; off += 4 {
			fmt.Fprintf(c.out, " %04x:%08x", off, m.rd(off))
		}
		fmt.Fprintln(c.out)
	}
}

// changed lists the registers that moved while the receiver ran: counters and status bits.
func (c *checker) changed(before, after map[string]uint32) {
	n := 0
	for _, r := range regions {
		for off := r.from; off < r.to; off += 4 {
			k := fmt.Sprintf("%s %#05x", r.name, off)
			if before[k] != after[k] {
				fmt.Fprintf(c.out, "moved: %s %08x -> %08x\n", k, before[k], after[k])
				n++
			}
		}
	}
	if n == 0 {
		fmt.Fprintln(c.out, "moved: nothing")
	}
}

func savePNG(name string, raw []byte) error {
	img := image.NewGray(image.Rect(0, 0, w, h))
	copy(img.Pix, raw)
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
