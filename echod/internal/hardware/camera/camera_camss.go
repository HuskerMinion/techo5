//go:build !dot && !spot && !panel

package camera

// The Lenovo ThinkSmart View on the msm8953 mainline kernel: a Samsung S5KC505A behind Qualcomm's
// camera subsystem (qcom-camss), plain V4L2 like athena's chain, and opened and streamed by the same
// code (camera_v4l2.go). Three things differ.
//
// The chain's links are not fixed. The sensor feeds msm_csiphy0, and from there the way through
// msm_csid0, msm_ispif0 and msm_vfe0_rdi0 to the video node msm_vfe0_video0 is three links that start
// disabled and are enabled through the media controller before the formats are set.
//
// The device stands the other way up from the sensor's read-out, so the sensor is flipped both ways,
// which the driver does itself. That moves its Bayer cell to green, blue, red, green, an order none
// of the frame's readers know; one column over, the cell starts on blue, as athena's does. So the
// frame taken from each line starts at its second pixel, and is thinkSmartSensor's width, a little
// less than the sensor's 1304.
//
// The frame arrives as MIPI packs 10-bit Bayer: four pixels' high bytes, then a byte of their low
// two bits each, in lines padded to eight bytes. Everything from the frame on reads the Show 5's
// packing, least significant bits first with no padding, so each frame is packed again that way as
// it comes (repackCAMSS), in the same pass as the column is dropped. It is the sensor's binned mode,
// 1304 by 980, a quarter of the full frame, to keep that pass short.

import (
	"errors"
	"fmt"
	"strings"
	"syscall"
	"unsafe"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// thinkSmartSensor is the S5KC505A in its 2x2 binned mode with the first column dropped: 1296 of its
// 1304 columns by 980 lines at 30 frames a second, the first pixel of each cell blue. The shutter runs
// from 4 lines to 2022 at the driver's default blanking (its exposure control, read off a unit
// 2026-11-19). The black level is taken as 64 a pixel, the usual for a 10-bit sensor of this family,
// and has not been measured on a unit yet.
var thinkSmartSensor = sensorSpec{w: 1296, h: 980, outW: 648, outH: 490, minShut: 4, frameLines: 2022,
	maxShut: 2022, blackCap: 144, pedestal: 64, blueFirst: true, v4l2: true}

const (
	camssW, camssH = 1304, 980 // what the sensor hands over
	camssLine      = 1632      // bytes a line as the video node writes it: 1630 padded to eight
	camssSkip      = 1         // columns dropped at the start of a line

	mbusSGBRG10 = 0x300e     // MEDIA_BUS_FMT_SGBRG10_1X10: the cell with both flips on
	pixSGBRG10P = 0x41414770 // 'pGAA'

	cidHFlip = 0x00980914 // V4L2_CID_HFLIP
	cidVFlip = 0x00980915 // V4L2_CID_VFLIP

	mediaEntNext     = 1 << 31 // MEDIA_ENT_ID_FLAG_NEXT
	mediaLnkEnabled  = 1       // MEDIA_LNK_FL_ENABLED
	mediaEntityDesc  = 256     // sizeof(struct media_entity_desc), no pointers
	mediaLinkDesc    = 52      // sizeof(struct media_link_desc), no pointers
	camssMediaDevice = "/dev/media0"
)

// camssBoard is whether this board's camera is behind qcom-camss.
func camssBoard() bool { return layout.ThinkSmart() }

// mioc is an ioctl number of the media controller's ('|').
func mioc(dir, nr, size uintptr) uintptr { return dir<<30 | size<<16 | '|'<<8 | nr }

// camssChain is the nodes of the chain in order, the sensor first and the video node last.
type camssChain struct {
	sensor string
	subdev [4]string // msm_csiphy0, msm_csid0, msm_ispif0, msm_vfe0_rdi0
	video  string
}

var camssSubdevs = [4]string{"msm_csiphy0", "msm_csid0", "msm_ispif0", "msm_vfe0_rdi0"}

func findCAMSS() (camssChain, error) {
	var c camssChain
	c.sensor, _ = v4l2Node(func(s string) bool { return strings.HasPrefix(s, "s5kc505a ") })
	if c.sensor == "" {
		return c, errors.New("no S5KC505A (the kernel's device tree has no camera, or the sensor did not answer)")
	}
	for i, name := range camssSubdevs {
		if c.subdev[i], _ = v4l2Node(func(s string) bool { return s == name }); c.subdev[i] == "" {
			return c, fmt.Errorf("the camera's receive chain is missing %s", name)
		}
	}
	if c.video, _ = v4l2Node(func(s string) bool { return s == "msm_vfe0_video0" }); c.video == "" {
		return c, errors.New("the camera's receive chain is missing its video node")
	}
	return c, nil
}

// mediaEntities is the media device's entities by name.
func mediaEntities(fd int) (map[string]uint32, error) {
	ids := map[string]uint32{}
	var id uint32
	for {
		var b [mediaEntityDesc]byte
		put32(b[:], 0, id|mediaEntNext)
		if err := ioctl(fd, mioc(3, 1, mediaEntityDesc), unsafe.Pointer(&b[0])); err != nil {
			if errors.Is(err, syscall.EINVAL) {
				return ids, nil // past the last one
			}
			return nil, err
		}
		id = le32(b[:], 0)
		name := string(b[4:36])
		if i := strings.IndexByte(name, 0); i >= 0 {
			name = name[:i]
		}
		ids[name] = id
	}
}

// mediaLink enables the link from one entity's pad to another's.
func mediaLink(fd int, from, fromPad, to, toPad uint32) error {
	var b [mediaLinkDesc]byte
	put32(b[:], 0, from)
	put32(b[:], 4, fromPad)
	put32(b[:], 20, to)
	put32(b[:], 24, toPad)
	put32(b[:], 40, mediaLnkEnabled)
	return ioctl(fd, mioc(3, 3, mediaLinkDesc), unsafe.Pointer(&b[0]))
}

// camssLinks enables the three links of the chain: each entity's source pad 1 into the next one's
// sink pad 0. A link that is already on is left as it is.
func camssLinks() error {
	fd, err := syscall.Open(camssMediaDevice, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", camssMediaDevice, err)
	}
	defer syscall.Close(fd)
	ids, err := mediaEntities(fd)
	if err != nil {
		return fmt.Errorf("camera entities: %w", err)
	}
	for i := 0; i+1 < len(camssSubdevs); i++ {
		from, ok1 := ids[camssSubdevs[i]]
		to, ok2 := ids[camssSubdevs[i+1]]
		if !ok1 || !ok2 {
			return fmt.Errorf("camera link %s to %s: no such entity", camssSubdevs[i], camssSubdevs[i+1])
		}
		if err := mediaLink(fd, from, 1, to, 0); err != nil {
			return fmt.Errorf("camera link %s to %s: %w", camssSubdevs[i], camssSubdevs[i+1], err)
		}
	}
	return nil
}

func openCAMSS() (*v4l2Cam, error) {
	chain, err := findCAMSS()
	if err != nil {
		return nil, err
	}
	if err := camssLinks(); err != nil {
		return nil, err
	}
	v := &v4l2Cam{sensor: -1, seninf: -1, camsv: -1, video: -1, lay: v4l2Layout(),
		buf: heapNew[[88]byte](), plane: heapNew[[64]byte](),
		gain:     func(g int) int32 { return int32(min(max(g/2, 32), 511)) }, // 32 is 1x here, 64 on the Show 5
		rawBytes: camssLine * camssH,
		repack:   camssRepacker(),
	}
	open := func(path string) (int, error) {
		fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_CLOEXEC, 0)
		if err != nil {
			v.close()
			return -1, fmt.Errorf("open %s: %w", path, err)
		}
		return fd, nil
	}
	if v.sensor, err = open(chain.sensor); err != nil {
		return nil, err
	}
	// The flips decide the Bayer order every pad is then asked for, so they go first.
	for _, c := range []uint32{cidHFlip, cidVFlip} {
		if err := setCtrl(v.sensor, c, 1); err != nil {
			v.close()
			return nil, fmt.Errorf("camera flip: %w", err)
		}
	}
	w, h := uint32(camssW), uint32(camssH)
	if err := subdevFormat(v.sensor, 0, w, h, mbusSGBRG10); err != nil {
		v.close()
		return nil, fmt.Errorf("camera sensor format: %w", err)
	}
	for i, path := range chain.subdev {
		fd, err := open(path)
		if err != nil {
			return nil, err
		}
		v.more = append(v.more, fd)
		if err := subdevFormat(fd, 0, w, h, mbusSGBRG10); err != nil {
			v.close()
			return nil, fmt.Errorf("camera %s format: %w", camssSubdevs[i], err)
		}
	}
	if v.video, err = open(chain.video); err != nil {
		return nil, err
	}

	f := make([]byte, wordSize+200) // v4l2_format: the union follows the type, aligned to a word
	u := wordSize
	put32(f, 0, v4l2BufCaptureMplane)
	put32(f, u, w)
	put32(f, u+4, h)
	put32(f, u+8, pixSGBRG10P)
	put32(f, u+12, v4l2FieldNone)
	if err := ioctl(v.video, vioc(3, 5, wordSize+200), unsafe.Pointer(&f[0])); err != nil {
		v.close()
		return nil, fmt.Errorf("camera video format: %w", err)
	}
	if pix, bpl := le32(f, u+8), le32(f, u+24); pix != pixSGBRG10P || int(bpl) != camssLine {
		v.close()
		return nil, fmt.Errorf("camera video format: %#x with %d bytes a line, want packed 10-bit at %d", pix, bpl, camssLine)
	}
	if err := v.buffers(); err != nil {
		v.close()
		return nil, err
	}
	return v, nil
}

// camssRepacker makes repackCAMSS's scratch once and returns it bound: one frame is repacked at a
// time, into the same buffer, which the caller copies what it keeps from.
func camssRepacker() func([]byte) []byte {
	out := make([]byte, frameBytes)
	return func(raw []byte) []byte {
		repackCAMSS(raw, out, camssLine, camssSkip, sensorW, sensorH)
		return out
	}
}

// repackCAMSS turns a frame as MIPI packs it (four pixels' high eight bits a byte each, then one byte
// of their low two bits, in lines of line bytes) into the Show 5's packing (ten bits a pixel, least
// significant first, no padding), taking w pixels of each of h lines from pixel skip on. skip is
// less than four and w a multiple of four.
func repackCAMSS(raw, out []byte, line, skip, w, h int) {
	var p [8]uint16
	for y := 0; y < h; y++ {
		in := raw[y*line : (y+1)*line]
		o := out[y*w*10/8:]
		for x := 0; x < w; x += 4 {
			// The four pixels wanted start at skip within a group of the line's, and so end in the next.
			g := in[x/4*5 : x/4*5+10 : x/4*5+10]
			lo, lo2 := g[4], g[9]
			p[0] = uint16(g[0])<<2 | uint16(lo&3)
			p[1] = uint16(g[1])<<2 | uint16(lo>>2&3)
			p[2] = uint16(g[2])<<2 | uint16(lo>>4&3)
			p[3] = uint16(g[3])<<2 | uint16(lo>>6)
			p[4] = uint16(g[5])<<2 | uint16(lo2&3)
			p[5] = uint16(g[6])<<2 | uint16(lo2>>2&3)
			p[6] = uint16(g[7])<<2 | uint16(lo2>>4&3)
			p[7] = uint16(g[8])<<2 | uint16(lo2>>6)
			a, b, c, d := p[skip], p[skip+1], p[skip+2], p[skip+3]
			q := o[x/4*5 : x/4*5+5 : x/4*5+5]
			q[0] = byte(a)
			q[1] = byte(a>>8) | byte(b<<2)
			q[2] = byte(b>>6) | byte(c<<4)
			q[3] = byte(c>>4) | byte(d<<6)
			q[4] = byte(d >> 2)
		}
	}
}
