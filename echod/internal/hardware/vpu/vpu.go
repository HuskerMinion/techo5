//go:build linux

// Package vpu runs the 2nd gen Show 8's VPU (the MT8183's Cadence Vision P6, core 0) through
// /dev/techo5-vpu (custom-kernel/athena: patches/vpu; research/vpu-vision-p6.md): it boots the
// unit's own VPU firmware, loads one of the unit's own algorithms and runs jobs on images in the
// driver's data area. Nothing of Amazon's is shipped: the firmware and the algorithms are read
// from the unit's cam_vpu partitions. Two algorithms are driven: apunn, the NN runtime (apunn.go,
// net.go: person detection), and swnr, the camera's noise reduction (swnr.go: the stream in dim
// light). The device takes one opener, so echod's users share one open device (Acquire), each
// loading its algorithm again when the other's ran last, and each keeping to one end of the data area.
//
// The structures passed to the kernel are declared as Go structs laid out as the C ones are
// (techo5_vpu_uapi.h: fixed-size fields only), so the request numbers, which carry their sizes, are
// right on arm and arm64 alike.
package vpu

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Path is the driver's device.
const Path = "/dev/techo5-vpu"

// MaxBufs is how many images one job takes.
const MaxBufs = 4

type blob struct {
	data     uint64
	size     uint32
	reserved uint32
}

// Buf is one image of a job, in the data area (Device.Data): Offset a multiple of 64.
type Buf struct {
	Port, Format uint8
	_            [2]uint8
	Width        uint32 // in bytes, as the algorithms count it
	Height       uint32
	Stride       uint32
	Length       uint32
	Offset       uint32
}

type syncReq struct {
	offset, length, flags, reserved uint32
}

type d2d struct {
	nbufs       uint32
	settingsLen uint32
	bufs        [MaxBufs]Buf
	settings    [64]byte
	timeoutMs   uint32
	stage       uint32
}

func ioc(dir, nr, size uintptr) uintptr { return dir<<30 | size<<16 | 'v'<<8 | nr }

var (
	iocBoot = ioc(1, 1, unsafe.Sizeof(blob{}))
	iocLoad = ioc(1, 2, unsafe.Sizeof(blob{}))
	iocD2D  = ioc(3, 3, unsafe.Sizeof(d2d{}))
	iocSync = ioc(1, 4, unsafe.Sizeof(syncReq{}))
)

// The data area's cached view (the device's mmap at cachedOff), and the cache maintenance it needs
// around a job (Sync): the VPU does not see the cores' caches.
const (
	cachedOff = 0x10000000
	// ForDevice writes the cores' writes to a range back to memory, before a job reads it.
	ForDevice = 1
	// ForCPU drops the cores' copies of a range, after a job wrote it and before they read it.
	ForCPU = 2
)

// FormatY8 is the firmware's format for 8-bit samples (and bytes that are not pixels).
const FormatY8 = 1

// Available is whether the board's kernel has the driver.
func Available() bool {
	_, err := os.Stat(Path)
	return err == nil
}

// Device is the open VPU.
type Device struct {
	mu   sync.Mutex // one request at a time
	fd   int
	data []byte
	// cached is the same area mapped cached (nil on a kernel without it): quick for the cores to
	// read, but a range written or read around a job wants Sync.
	cached []byte

	// The shared device (Acquire): its holders, the algorithms they registered (Register), which one
	// is on the core, and whether the core is booted. A holder runs its jobs between Lock and Unlock,
	// which loads its algorithm again if another holder's ran since.
	job    sync.Mutex
	refs   int
	elfs   map[string][]byte
	loaded string
	booted bool
	// low and high are the data area's parts the holders keep between jobs: from the bottom up to
	// low (apunn's nets), from high to the top (swnr's pictures). They must not meet.
	low, high uint32
}

// Open opens the VPU and maps its data area, for this caller alone: one opener at a time (EBUSY
// otherwise), and Acquire's holders count as one. The core is powered from Boot until Close.
func Open() (*Device, error) {
	fd, err := unix.Open(Path, unix.O_RDWR|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	d := &Device{fd: fd}
	// the area is the region's end, from 0x7eb00000: 5 MiB
	if d.data, err = unix.Mmap(fd, 0, 5<<20, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("vpu: the data area: %w", err)
	}
	if c, err := unix.Mmap(fd, cachedOff, 5<<20, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED); err == nil {
		d.cached = c
	}
	d.high = uint32(len(d.data))
	return d, nil
}

var (
	sharedMu sync.Mutex
	shared   *Device
)

// Acquire is a hold on the process's one open VPU, shared by everything in echod that uses it (the
// device takes one opener): opened and booted (the unit's cam_vpu1) by the first holder, closed, and
// so powered down, when the last one lets go (Close). Each holder registers its algorithm and keeps to
// its part of the data area.
func Acquire() (*Device, error) {
	sharedMu.Lock()
	defer sharedMu.Unlock()
	if shared != nil {
		shared.refs++
		return shared, nil
	}
	d, err := Open()
	if err != nil {
		return nil, err
	}
	d.refs, d.elfs = 1, map[string][]byte{}
	d.job.Lock()
	err = d.boot()
	d.job.Unlock()
	if err != nil {
		d.close()
		return nil, err
	}
	shared = d
	return d, nil
}

// boot boots the core from the unit's firmware, with d.job held.
func (d *Device) boot() error {
	img, err := ReadFirmware()
	if err != nil {
		return err
	}
	if err := d.Boot(img); err != nil {
		return err
	}
	d.booted = true
	return nil
}

// Register gives the shared device an algorithm by name, for Lock to load when it is wanted.
func (d *Device) Register(name string, elf []byte) {
	d.job.Lock()
	defer d.job.Unlock()
	if d.elfs == nil {
		d.elfs = map[string][]byte{}
	}
	d.elfs[name] = elf
}

// Lock takes the core for a run of jobs of the named algorithm (registered), loading it if another
// ran since, and booting the core again if a job failed since (the driver powers it down then).
// Unlock gives it back.
func (d *Device) Lock(name string) error {
	d.job.Lock()
	if err := d.use(name); err != nil {
		d.job.Unlock()
		return err
	}
	return nil
}

// Unlock ends a run of jobs Lock began.
func (d *Device) Unlock() { d.job.Unlock() }

// use loads the named algorithm if it is registered and not on the core, with d.job held. An
// algorithm not registered is the caller's to have loaded (Load).
func (d *Device) use(name string) error {
	elf, ok := d.elfs[name]
	if !ok || d.loaded == name && d.booted {
		return nil
	}
	if !d.booted {
		if err := d.boot(); err != nil {
			return err
		}
	}
	if err := d.Load(elf); err != nil {
		return err
	}
	d.loaded = name
	return nil
}

// Close lets the device go: a holder's hold (Acquire), or the device itself.
func (d *Device) Close() {
	sharedMu.Lock()
	if d.refs > 0 {
		d.refs--
		if d.refs > 0 {
			sharedMu.Unlock()
			return
		}
		shared = nil
	}
	sharedMu.Unlock()
	d.close()
}

func (d *Device) close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.data != nil {
		unix.Munmap(d.data)
		d.data = nil
	}
	if d.cached != nil {
		unix.Munmap(d.cached)
		d.cached = nil
	}
	unix.Close(d.fd)
}

// reserve marks [from, to) of the data area as kept by a holder between jobs: low for a part from the
// bottom, high for one to the top. Refused if it meets the other end's.
func (d *Device) reserve(to uint32, fromTop bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if fromTop {
		if to < d.low {
			return fmt.Errorf("vpu: the data area's top from %#x meets its bottom's %#x", to, d.low)
		}
		d.high = min(d.high, to)
		return nil
	}
	if to > d.high {
		return fmt.Errorf("vpu: the data area's bottom to %#x meets its top's %#x", to, d.high)
	}
	d.low = max(d.low, to)
	return nil
}

// Data is the area the jobs' images live in, as Buf.Offset counts it: uncached for the cores (slow
// to read, nothing to keep in step).
func (d *Device) Data() []byte { return d.data }

// Cached is the same area cached for the cores, or nil where the kernel has no such view: a range the
// cores write wants Sync(ForDevice) before a job reads it, and one a job writes Sync(ForCPU) before
// the cores read it. A range is best used through one view only.
func (d *Device) Cached() []byte { return d.cached }

// Sync does the cache maintenance on [off, off+n) of the cached view that flags (ForDevice, ForCPU)
// ask for; nothing without that view.
func (d *Device) Sync(off, n uint32, flags uint32) error {
	if d.cached == nil {
		return nil
	}
	s := syncReq{offset: off, length: n, flags: flags}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ioctl(iocSync, unsafe.Pointer(&s)); err != nil {
		return fmt.Errorf("vpu: sync: %w", err)
	}
	return nil
}

func (d *Device) ioctl(req uintptr, p unsafe.Pointer) error {
	for {
		_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(d.fd), req, uintptr(p))
		if e == syscall.EINTR {
			continue
		}
		if e != 0 {
			return e
		}
		return nil
	}
}

// Boot boots the core from a firmware image (Firmware).
func (d *Device) Boot(image []byte) error {
	b := blob{data: uint64(uintptr(unsafe.Pointer(&image[0]))), size: uint32(len(image))}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.loaded = ""
	if err := d.ioctl(iocBoot, unsafe.Pointer(&b)); err != nil {
		d.booted = false
		return fmt.Errorf("vpu: boot: %w", err)
	}
	d.booted = true
	return nil
}

// Load loads an algorithm's ELF (Algorithm). Only what its program headers load is sent: the loader
// reads nothing else, and the driver takes 1 MiB (apunn's ELF is 1.3 MiB, 0.97 MiB of it loaded).
func (d *Device) Load(elf []byte) error {
	elf = loaded(elf)
	b := blob{data: uint64(uintptr(unsafe.Pointer(&elf[0]))), size: uint32(len(elf))}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.loaded = ""
	if err := d.ioctl(iocLoad, unsafe.Pointer(&b)); err != nil {
		d.booted = false
		return fmt.Errorf("vpu: load: %w", err)
	}
	return nil
}

// Run runs a job of the loaded algorithm on bufs with its settings, and returns the firmware's
// stage when it ended (0x1400: done).
func (d *Device) Run(bufs []Buf, settings []byte, timeoutMs int) (uint32, error) {
	if len(bufs) == 0 || len(bufs) > MaxBufs || len(settings) > 64 {
		return 0, errors.New("vpu: a job of the wrong shape")
	}
	j := d2d{nbufs: uint32(len(bufs)), settingsLen: uint32(len(settings)), timeoutMs: uint32(timeoutMs)}
	copy(j.bufs[:], bufs)
	copy(j.settings[:], settings)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ioctl(iocD2D, unsafe.Pointer(&j)); err != nil {
		// a timeout or an exception: the driver has powered the core down, and it boots again
		d.booted = false
		return j.stage, fmt.Errorf("vpu: job: %w (stage %#x)", err, j.stage)
	}
	return j.stage, nil
}

// loaded cuts an ELF32 after the last byte a program header loads.
func loaded(elf []byte) []byte {
	le := binary.LittleEndian
	if len(elf) < 52 || string(elf[1:4]) != "ELF" || elf[4] != 1 {
		return elf
	}
	phoff, phentsize, phnum := le.Uint32(elf[28:]), int(le.Uint16(elf[42:])), int(le.Uint16(elf[44:]))
	end := uint32(int(phoff) + phentsize*phnum)
	for i := 0; i < phnum; i++ {
		h := int(phoff) + i*phentsize
		if h+20 > len(elf) {
			return elf
		}
		if le.Uint32(elf[h:]) == 1 { // PT_LOAD
			end = max(end, le.Uint32(elf[h+4:])+le.Uint32(elf[h+16:]))
		}
	}
	if int(end) < len(elf) {
		return elf[:end]
	}
	return elf
}

// The unit's partitions, by name (Fire OS's GPT names them)
var blockClass = "/sys/class/block"

func partition(name string) (string, error) {
	paths, _ := filepath.Glob(filepath.Join(blockClass, "*", "uevent"))
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		for _, l := range strings.Split(string(b), "\n") {
			if l == "PARTNAME="+name {
				return "/dev/" + filepath.Base(filepath.Dir(p)), nil
			}
		}
	}
	return "", fmt.Errorf("vpu: no %s partition", name)
}

// ReadPartition reads a cam_vpu partition of the unit.
func ReadPartition(name string) ([]byte, error) {
	dev, err := partition(name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(dev)
}

// The vpu_image_header, after the partition's 0x200-byte MTK header (research/vpu-vision-p6.md §1)
const (
	imageHdr      = 0x200
	segCountOff   = 0x30
	algoCountOff  = 0x41c
	core0         = 0x61
	firmwareBase  = 0x7da00000
	firmwareSize  = 0x400000
	iramBlobOff   = 0x300000
	iramDst       = 0x7ff00000
	iramHeaderLen = 0x100
)

// Firmware lays core 0's segments of a cam_vpu1 partition out as the driver boots them: each at its
// address in the 4 MiB from 0x7da00000, and the segments the core's own memories take (DMEM, IMEM)
// as the IRAM blob SET_DBG loads, at 0x7dd00000: {count; {offset, dst, size}[20]}, then the data.
func Firmware(part []byte) ([]byte, error) {
	le := binary.LittleEndian
	if len(part) < imageHdr+algoCountOff {
		return nil, errors.New("vpu: not a cam_vpu partition")
	}
	n := int(le.Uint32(part[imageHdr+segCountOff:]))
	if n > 50 {
		return nil, errors.New("vpu: not a cam_vpu partition")
	}
	img := make([]byte, firmwareSize)
	type seg struct {
		dst  uint32
		data []byte
	}
	var iram []seg
	for i := 0; i < n; i++ {
		e := part[imageHdr+segCountOff+4+i*20:]
		core, off, dst, length := le.Uint32(e), le.Uint32(e[4:]), le.Uint32(e[8:]), le.Uint32(e[12:])
		if core != core0 {
			continue
		}
		if int(imageHdr+off+length) > len(part) {
			return nil, fmt.Errorf("vpu: segment %d runs past the partition", i)
		}
		data := part[imageHdr+off : imageHdr+off+length]
		if dst >= iramDst {
			iram = append(iram, seg{dst, data})
			continue
		}
		if dst < firmwareBase || dst+length > firmwareBase+iramBlobOff {
			return nil, fmt.Errorf("vpu: segment %d at %#x is not core 0's", i, dst)
		}
		copy(img[dst-firmwareBase:], data)
	}
	if len(iram) > 20 {
		return nil, errors.New("vpu: too many IRAM segments")
	}
	blob := img[iramBlobOff:]
	le.PutUint32(blob, uint32(len(iram)))
	at := iramHeaderLen
	for k, s := range iram {
		if iramBlobOff+at+len(s.data) > firmwareSize {
			return nil, errors.New("vpu: the IRAM segments do not fit")
		}
		le.PutUint32(blob[4+k*12:], uint32(at))
		le.PutUint32(blob[8+k*12:], s.dst)
		le.PutUint32(blob[12+k*12:], uint32(len(s.data)))
		copy(blob[at:], s.data)
		at += (len(s.data) + 15) &^ 15
	}
	return img, nil
}

// Algorithm is the named algorithm's ELF for core 0, as a cam_vpu2 or cam_vpu3 partition stores it.
func Algorithm(part []byte, name string) ([]byte, error) {
	off, length, err := algorithmAt(part, name)
	if err != nil {
		return nil, err
	}
	if int(off+length) > len(part) {
		return nil, fmt.Errorf("vpu: %s runs past the partition", name)
	}
	return part[off : off+length], nil
}

// algorithmAt finds the named algorithm for core 0 in a partition's table (its first tableLen bytes
// are enough): where its ELF is, from the partition's start.
func algorithmAt(part []byte, name string) (off, length uint32, err error) {
	le := binary.LittleEndian
	if len(part) < imageHdr+algoCountOff+4 {
		return 0, 0, errors.New("vpu: not a cam_vpu partition")
	}
	n := int(le.Uint32(part[imageHdr+algoCountOff:]))
	if n > 50 || len(part) < imageHdr+algoCountOff+4+n*44 {
		return 0, 0, errors.New("vpu: not a cam_vpu partition")
	}
	for i := 0; i < n; i++ {
		e := part[imageHdr+algoCountOff+4+i*44:] // {core, offset, length, name[32]}
		core, o, l := le.Uint32(e), le.Uint32(e[4:]), le.Uint32(e[8:])
		nm, _, _ := strings.Cut(string(e[12:44]), "\x00")
		if core != core0 || nm != name {
			continue
		}
		if l > 64<<20 || o > 1<<30 {
			return 0, 0, fmt.Errorf("vpu: %s of the wrong size", name)
		}
		return imageHdr + o, l, nil
	}
	return 0, 0, fmt.Errorf("vpu: no %s for core 0", name)
}

// tableLen is as much of a cam_vpu partition as its tables take: the segments' and the algorithms'
// (50 of each at most).
const tableLen = imageHdr + algoCountOff + 4 + 50*44

// readAt reads n bytes of the named partition from off.
func readAt(name string, off int64, n int) ([]byte, error) {
	dev, err := partition(name)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(dev)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b := make([]byte, n)
	if _, err := f.ReadAt(b, off); err != nil {
		return nil, fmt.Errorf("vpu: %s: %w", name, err)
	}
	return b, nil
}

// ReadFirmware is Firmware of the unit's cam_vpu1, reading only as much of the partition as core 0's
// segments take (a few MiB of its 15).
func ReadFirmware() ([]byte, error) {
	le := binary.LittleEndian
	hdr, err := readAt("cam_vpu1", 0, tableLen)
	if err != nil {
		return nil, err
	}
	n := int(le.Uint32(hdr[imageHdr+segCountOff:]))
	if n > 50 {
		return nil, errors.New("vpu: not a cam_vpu partition")
	}
	end := uint64(len(hdr))
	for i := 0; i < n; i++ {
		e := hdr[imageHdr+segCountOff+4+i*20:]
		if le.Uint32(e) == core0 {
			end = max(end, imageHdr+uint64(le.Uint32(e[4:]))+uint64(le.Uint32(e[12:])))
		}
	}
	if end > 64<<20 {
		return nil, errors.New("vpu: not a cam_vpu partition")
	}
	part, err := readAt("cam_vpu1", 0, int(end))
	if err != nil {
		return nil, err
	}
	return Firmware(part)
}

// ReadAlgorithm is Algorithm of the named partition, reading only its table and the ELF.
func ReadAlgorithm(part, name string) ([]byte, error) {
	hdr, err := readAt(part, 0, tableLen)
	if err != nil {
		return nil, err
	}
	off, length, err := algorithmAt(hdr, name)
	if err != nil {
		return nil, err
	}
	return readAt(part, int64(off), int(length))
}
