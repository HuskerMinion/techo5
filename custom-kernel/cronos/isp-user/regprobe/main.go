// regprobe: read-only snapshots of the MT8163 ISP, imgsys and MMSYS register windows through
// /dev/mem, optionally while it streams the camera itself, to map which offsets are implemented
// (read back non-zero) and which move from frame to frame.
//
//	regprobe [-stream] [-n 8] [-interval 150ms] [-o dump.txt] [-hi] [-save frame.raw]
//	         [-exposure lines] [-gain 64..992]
//
// -exposure and -gain set the sensor's controls first. -hi adds the rest of the camera node's CAM window, 0x15005000-0x15007fff (stage 2's probe for
// the gamma LUT and the blocks beyond the first page). -save writes the last streamed frame.
//
// It never writes a register: /dev/mem is opened O_RDONLY and each page is mapped PROT_READ.
// Offsets that are read-clear (the CAM interrupt status words) or write-only are skipped, see
// skip below. The CAM and imgsys windows are read only while frames are arriving, since the ISP
// power domain and the imgsys clocks are off otherwise and a read there would hang the bus.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

type window struct {
	name string
	base uint64
	len  int
	// needsISP: inside the ISP power domain, readable only while the camera streams.
	needsISP bool
}

var windows = []window{
	{"cam", 0x15004000, 0x1000, true},    // CAM_CTL, DMA, TG, the processing blocks, CDP
	{"img", 0x15000000, 0x1000, true},    // imgsys config: clock gates, misc
	{"mmsys", 0x14000000, 0x1000, false}, // MMSYS_CONFIG: MOUT/SEL routing, clock gates
}

// hiWindows: the rest of the CAM window the camera node claims (0x15004000 + 0x4000).
var hiWindows = []window{
	{"cam5", 0x15005000, 0x1000, true},
	{"cam6", 0x15006000, 0x1000, true},
	{"cam7", 0x15007000, 0x1000, true},
}

// skip lists [from, to) byte ranges never to touch.
var skip = map[string][][2]uint32{
	// camera_isp.c ISP_DumpReg: 0x024-0x038 "may touch ReadClear register"; the B/C/X
	// interrupt status pairs 0x03c-0x048 are the same kind of register; 0x080-0x094 are WO.
	"cam": {{0x020, 0x050}, {0x080, 0x098}},
}

func skipped(w string, off uint32) bool {
	for _, r := range skip[w] {
		if off >= r[0] && off < r[1] {
			return true
		}
	}
	return false
}

type mapped struct {
	window
	mem []byte
}

func (m *mapped) read(off uint32) uint32 {
	return *(*uint32)(unsafe.Pointer(&m.mem[off]))
}

func mapAll(fd int) ([]*mapped, error) {
	var out []*mapped
	for _, w := range windows {
		mem, err := syscall.Mmap(fd, int64(w.base), w.len, syscall.PROT_READ, syscall.MAP_SHARED)
		if err != nil {
			return nil, fmt.Errorf("mmap %s %#x: %w", w.name, w.base, err)
		}
		out = append(out, &mapped{w, mem})
	}
	return out, nil
}

// camera streaming through the mainline cronos-camera node: S_FMT 1600x1200 pRAA, then read().
type v4l2PixFormat struct {
	width, height, pixelformat, field, bytesperline, sizeimage uint32
	colorspace, priv, flags, encoding, quantization, xferFunc  uint32
}
type v4l2Format struct {
	typ   uint32
	union struct {
		_   [0]uintptr
		pix v4l2PixFormat
		_   [200 - unsafe.Sizeof(v4l2PixFormat{})]byte
	}
}

func cameraPath() string {
	paths, _ := filepath.Glob("/sys/class/video4linux/video*/name")
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err == nil && strings.TrimSpace(string(b)) == "cronos-ov02b10" {
			return "/dev/" + filepath.Base(filepath.Dir(p))
		}
	}
	return ""
}

// lastFrame holds a copy of the most recent frame for -save.
var (
	lastMu    sync.Mutex
	lastFrame []byte
)

// Sensor controls set before streaming when non-zero (-exposure, -gain).
var exposure, gain int

func setCtrl(fd int, id uint32, v int32) error {
	c := struct {
		id    uint32
		value int32
	}{id, v}
	req := uintptr(3<<30 | unsafe.Sizeof(c)<<16 | 'V'<<8 | 28) // VIDIOC_S_CTRL
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&c))); e != 0 {
		return e
	}
	return nil
}

func stream(frames chan<- int, stop <-chan struct{}, keep bool) error {
	path := cameraPath()
	if path == "" {
		return fmt.Errorf("no cronos-ov02b10 video node")
	}
	fd, err := syscall.Open(path, syscall.O_RDWR, 0)
	if err != nil {
		return err
	}
	f := v4l2Format{typ: 1}
	f.union.pix.width, f.union.pix.height = 1600, 1200
	f.union.pix.pixelformat = uint32('p') | uint32('R')<<8 | uint32('A')<<16 | uint32('A')<<24
	req := uintptr(3<<30 | unsafe.Sizeof(f)<<16 | 'V'<<8 | 5)
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&f))); e != 0 {
		syscall.Close(fd)
		return fmt.Errorf("S_FMT: %v", e)
	}
	for _, c := range []struct {
		id uint32
		v  int
	}{{0x00980911, exposure}, {0x009e0903, gain}} { // V4L2_CID_EXPOSURE, V4L2_CID_ANALOGUE_GAIN
		if c.v != 0 {
			if err := setCtrl(fd, c.id, int32(c.v)); err != nil {
				syscall.Close(fd)
				return fmt.Errorf("S_CTRL %#x: %v", c.id, err)
			}
		}
	}
	go func() {
		defer syscall.Close(fd)
		buf := make([]byte, f.union.pix.sizeimage)
		for n := 1; ; n++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := syscall.Read(fd, buf); err != nil {
				if err == syscall.EINTR {
					continue
				}
				fmt.Fprintln(os.Stderr, "camera read:", err)
				close(frames)
				return
			}
			if keep {
				lastMu.Lock()
				lastFrame = append(lastFrame[:0], buf...)
				lastMu.Unlock()
			}
			select {
			case frames <- n:
			default:
			}
		}
	}()
	return nil
}

func main() {
	doStream := flag.Bool("stream", false, "stream the camera while reading (echod must not hold it)")
	n := flag.Int("n", 8, "snapshots")
	interval := flag.Duration("interval", 150*time.Millisecond, "time between snapshots")
	outPath := flag.String("o", "", "write the full dump here (default stdout)")
	onlyMMSYS := flag.Bool("mmsys-only", false, "read only the windows outside the ISP domain")
	hi := flag.Bool("hi", false, "also read CAM 0x15005000-0x15007fff")
	savePath := flag.String("save", "", "write the last streamed frame (packed RAW10) here")
	flag.IntVar(&exposure, "exposure", 0, "sensor exposure in lines (0: the driver's)")
	flag.IntVar(&gain, "gain", 0, "sensor analogue gain, 64-992 (0: the driver's)")
	flag.Parse()
	if *hi {
		windows = append(windows, hiWindows...)
	}

	fd, err := syscall.Open("/dev/mem", syscall.O_RDONLY|syscall.O_SYNC, 0)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ms, err := mapAll(fd)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	frames := make(chan int, 1)
	stop := make(chan struct{})
	ispOK := false
	if *doStream {
		if err := stream(frames, stop, *savePath != ""); err != nil {
			fmt.Fprintln(os.Stderr, "stream:", err)
			os.Exit(1)
		}
		select {
		case _, ok := <-frames:
			ispOK = ok
		case <-time.After(5 * time.Second):
		}
		if !ispOK {
			fmt.Fprintln(os.Stderr, "no frame in 5s: not reading the ISP windows")
		}
	}
	if *onlyMMSYS {
		ispOK = false
	}

	// snaps[window][snapshot][word]
	snaps := map[string][][]uint32{}
	stamps := []int{}
	for i := 0; i < *n; i++ {
		frame := 0
		if *doStream && ispOK {
			select {
			case f, ok := <-frames:
				if !ok {
					ispOK = false
				}
				frame = f
			case <-time.After(2 * time.Second):
				ispOK = false
			}
		}
		stamps = append(stamps, frame)
		for _, m := range ms {
			if m.needsISP && !ispOK {
				continue
			}
			words := make([]uint32, m.len/4)
			for o := 0; o < m.len; o += 4 {
				if !skipped(m.name, uint32(o)) {
					words[o/4] = m.read(uint32(o))
				}
			}
			snaps[m.name] = append(snaps[m.name], words)
		}
		time.Sleep(*interval)
	}
	close(stop)
	if *savePath != "" {
		lastMu.Lock()
		frame := append([]byte(nil), lastFrame...)
		lastMu.Unlock()
		if len(frame) == 0 {
			fmt.Fprintln(os.Stderr, "no frame to save")
		} else if err := os.WriteFile(*savePath, frame, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
	}

	out := os.Stdout
	if *outPath != "" {
		if out, err = os.Create(*outPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer out.Close()
	}
	w := bufio.NewWriter(out)
	defer w.Flush()
	fmt.Fprintf(w, "# regprobe %s, snapshots at frames %v\n", time.Now().UTC().Format(time.RFC3339), stamps)
	names := make([]string, 0, len(snaps))
	for k := range snaps {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		var base uint64
		for _, m := range ms {
			if m.name == name {
				base = m.base
			}
		}
		s := snaps[name]
		nz, moving := 0, 0
		fmt.Fprintf(w, "\n## %s %#x (%d snapshots)\n", name, base, len(s))
		for o := 0; o < len(s[0]); o++ {
			off := uint32(o * 4)
			if skipped(name, off) {
				continue
			}
			first, changes := s[0][o], false
			for _, sn := range s[1:] {
				if sn[o] != first {
					changes = true
				}
			}
			if first == 0 && !changes {
				continue
			}
			nz++
			mark := " "
			if changes {
				moving++
				mark = "*"
			}
			fmt.Fprintf(w, "%s %03x %08x", mark, off, first)
			if changes {
				for _, sn := range s[1:] {
					fmt.Fprintf(w, " %08x", sn[o])
				}
			}
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "# %s: %d non-zero words, %d changing\n", name, nz, moving)
	}
}
