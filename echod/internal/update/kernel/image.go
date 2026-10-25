package kernel

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// An athena boot image is an Android boot image (header version 1) as tools/linux/athena/kernel's
// pack.py makes it: the kernel with the device tree after it, and a small ramdisk of firmware. The
// device tree holds the amp tuning slot (tools/linux/athena/kernel/dsp_slot.py). The released image
// carries the slot empty; the installer fills it with the unit's own tuning and re-signs the header.
// The code here is the installer's (tools/install-athena.py, BootImage, fill_slot, read_slot), so a
// kernel this writes is the one the installer would have.

const (
	slotSize = 8192
	slotHead = 44
	slotMax  = slotSize - slotHead
	headerID = 576 // the 32-byte id in the header: SHA-1 over the parts, zero padded
)

var slotMagic = []byte("T5DSP" + "CF1")

// knownTables are the amp tunings the installer accepts, by the sha256 of the table (install-athena.py,
// KNOWN_AMP_TABLES). A slot holding anything else is not written into a new kernel.
var knownTables = map[string]string{
	"8c14a75c68e77d071cfc512f2fbeb002351eb2f26b0ee945bd94e87b10c45c49": "Fire OS 7080, table A",
	"5f3f4ddbcb081b6ac49a361c529ef99fcb010cee0100ae37088d831601ba9248": "Fire OS 7080, table B",
}

type image struct {
	data                              []byte
	kernelSize, ramdiskSize, secondSz uint32
	page, version                     uint32
	kernelAt, ramdiskAt, secondAt     int
	dtbo, dtb                         []byte
	extent                            int // where the image ends, page aligned: the bytes LK reads
}

func pad(n, page int) int { return (n + page - 1) / page * page }

// parseImage reads an Android boot image header, version 0 to 2.
func parseImage(data []byte) (*image, error) {
	if len(data) < 1652 || !bytes.Equal(data[:8], []byte("ANDROID!")) {
		return nil, errors.New("not an Android boot image")
	}
	u := func(at int) uint32 { return binary.LittleEndian.Uint32(data[at:]) }
	im := &image{data: data, kernelSize: u(8), ramdiskSize: u(16), secondSz: u(24), page: u(36), version: u(40)}
	switch im.page {
	case 2048, 4096, 8192, 16384:
	default:
		return nil, fmt.Errorf("page size %d", im.page)
	}
	if im.version > 2 {
		return nil, fmt.Errorf("header version %d", im.version)
	}
	// Sizes and offsets are the image's word, checked against its length in 64 bits before any of
	// them is used: on the device an int is 32 bits, and a u32 near 4 GiB would turn negative.
	n := int64(len(data))
	p := int64(im.page)
	pad64 := func(v int64) int64 { return (v + p - 1) / p * p }
	kernelAt := p
	ramdiskAt := kernelAt + pad64(int64(im.kernelSize))
	secondAt := ramdiskAt + pad64(int64(im.ramdiskSize))
	end := secondAt + int64(im.secondSz)
	if end > n {
		return nil, fmt.Errorf("%d bytes, shorter than its header says (%d)", n, end)
	}
	extent := secondAt + pad64(int64(im.secondSz))
	var dtbo, dtb []byte
	if im.version >= 1 {
		size := int64(u(1632))
		off := binary.LittleEndian.Uint64(data[1636:])
		if size > 0 {
			if off > uint64(n) || size > n-int64(off) {
				return nil, errors.New("ends inside its recovery dtbo")
			}
			dtbo = data[int64(off) : int64(off)+size]
			extent = max(extent, pad64(int64(off)+size))
		}
	}
	if im.version >= 2 {
		size := int64(u(1648))
		at := secondAt + pad64(int64(im.secondSz)) + pad64(int64(len(dtbo)))
		if at > n || size > n-at {
			return nil, errors.New("ends inside its dtb")
		}
		dtb = data[at : at+size]
		extent = max(extent, pad64(at+size))
	}
	im.kernelAt, im.ramdiskAt, im.secondAt = int(kernelAt), int(ramdiskAt), int(secondAt)
	im.dtbo, im.dtb = dtbo, dtb
	im.extent = int(min(extent, n))
	return im, nil
}

func (im *image) part(at int, size uint32) []byte { return im.data[at : at+int(size)] }

// computedID is the header's id as mkbootimg (and pack.py) make it.
func (im *image) computedID() []byte {
	h := sha1.New()
	parts := [][]byte{im.part(im.kernelAt, im.kernelSize), im.part(im.ramdiskAt, im.ramdiskSize), im.part(im.secondAt, im.secondSz)}
	if im.version >= 1 {
		parts = append(parts, im.dtbo)
	}
	if im.version >= 2 {
		parts = append(parts, im.dtb)
	}
	var n [4]byte
	for _, b := range parts {
		h.Write(b)
		binary.LittleEndian.PutUint32(n[:], uint32(len(b)))
		h.Write(n[:])
	}
	id := make([]byte, 32)
	copy(id, h.Sum(nil))
	return id
}

func (im *image) idOK() bool { return bytes.Equal(im.data[headerID:headerID+32], im.computedID()) }

// slotAt is where the tuning slot is: the magic exactly once, the whole slot inside the parts LK loads.
func (im *image) slotAt() (int, error) {
	if n := bytes.Count(im.data[:im.extent], slotMagic); n != 1 {
		return 0, fmt.Errorf("%d amp tuning slots, not one", n)
	}
	at := bytes.Index(im.data, slotMagic)
	if at < im.kernelAt || at+slotSize > im.secondAt+int(im.secondSz) {
		return 0, errors.New("the amp tuning slot is not inside the kernel, ramdisk or second stage")
	}
	return at, nil
}

// table is the tuning in the slot, checked against the slot's own sha256; nil when the slot is empty.
func (im *image) table() ([]byte, error) {
	at, err := im.slotAt()
	if err != nil {
		return nil, err
	}
	n := int(binary.LittleEndian.Uint32(im.data[at+8:]))
	if n == 0 {
		return nil, nil
	}
	if n > slotMax || n%2 != 0 {
		return nil, fmt.Errorf("amp tuning slot length %d", n)
	}
	t := im.data[at+slotHead : at+slotHead+n]
	if sum := sha256.Sum256(t); !bytes.Equal(sum[:], im.data[at+12:at+slotHead]) {
		return nil, errors.New("the amp tuning in the slot does not match its own SHA-256")
	}
	return t, nil
}

// withTable is a copy of the image's first extent bytes with table in its slot (nil: emptied) and the
// header id recomputed; the same size.
func (im *image) withTable(table []byte) ([]byte, error) {
	if len(table) > slotMax || len(table)%2 != 0 {
		return nil, fmt.Errorf("amp tuning of %d bytes", len(table))
	}
	at, err := im.slotAt()
	if err != nil {
		return nil, err
	}
	out := bytes.Clone(im.data[:im.extent])
	clear(out[at+8 : at+slotSize])
	if len(table) > 0 {
		binary.LittleEndian.PutUint32(out[at+8:], uint32(len(table)))
		sum := sha256.Sum256(table)
		copy(out[at+12:], sum[:])
		copy(out[at+slotHead:], table)
	}
	o, err := parseImage(out)
	if err != nil {
		return nil, err
	}
	copy(out[headerID:], o.computedID())
	return out, nil
}

// checkShipped: the athena kernel a slot carries, as released: a valid image, its id right, its slot
// empty.
func checkShipped(data []byte) (*image, error) {
	im, err := parseImage(data)
	if err != nil {
		return nil, err
	}
	if !im.idOK() {
		return nil, errors.New("its header id does not match its contents")
	}
	at, err := im.slotAt()
	if err != nil {
		return nil, err
	}
	// Empty as the installer means it (check_techo5_boot): nothing after the magic, not even a length
	// of zero with something left behind it, which would never compare equal to a filled slot emptied
	// and so be written again after every boot.
	for _, b := range im.data[at+len(slotMagic) : at+slotSize] {
		if b != 0 {
			return nil, errors.New("its amp tuning slot is not empty")
		}
	}
	if im.extent != len(data) {
		return nil, fmt.Errorf("%d bytes after the image's end", len(data)-im.extent)
	}
	return im, nil
}

// A Skylight boot image is header version 2 as tools/linux/skylight/kernel's pack-sky.py makes it:
// the kernel, an empty ramdisk, a Rockchip resource image and the device tree, nothing of the unit's
// own. Its command line tells TECHO5's kernel (the store as the root) from the installer image, which
// boots the same kernel for one install (rdinit=/init techo5.installer=1).

// cmdline is the header's command line.
func (im *image) cmdline() []string {
	c := im.data[64 : 64+512]
	if i := bytes.IndexByte(c, 0); i >= 0 {
		c = c[:i]
	}
	return strings.Fields(string(c))
}

// skylightBoot is whether im is TECHO5's Skylight kernel: header version 2 with a device tree, and
// the command line build.sh gives the boot image, not the installer's.
func skylightBoot(im *image) bool {
	if im.version != 2 || len(im.dtb) == 0 {
		return false
	}
	root := false
	for _, w := range im.cmdline() {
		switch {
		case w == "root=PARTLABEL=techo5-store":
			root = true
		case strings.HasPrefix(w, "rdinit=") || strings.HasPrefix(w, "techo5.installer"):
			return false
		}
	}
	return root
}

// checkShipped is checkShipped for the board: athena's (the function), or the Skylight's: a valid
// image, its id right, TECHO5's Skylight kernel, and nothing after its end.
func (b Board) checkShipped(data []byte) (*image, error) {
	if b != Skylight {
		return checkShipped(data)
	}
	im, err := parseImage(data)
	if err != nil {
		return nil, err
	}
	if !im.idOK() {
		return nil, errors.New("its header id does not match its contents")
	}
	if !skylightBoot(im) {
		return nil, errors.New("it is not TECHO5's Skylight kernel (pack-sky.py, root=PARTLABEL=techo5-store)")
	}
	if im.extent != len(data) {
		return nil, fmt.Errorf("%d bytes after the image's end", len(data)-im.extent)
	}
	return im, nil
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
