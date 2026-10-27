package rtpjpeg

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func picture(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), uint8(x ^ y), 255})
		}
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: 70}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// What Go's encoder makes is taken apart, and its packets carry it whole.
func TestAGoJPEGGoesIntoPacketsWhole(t *testing.T) {
	f, err := Parse(picture(t, 640, 480))
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != 1 || f.Width != 640 || f.Height != 480 || len(f.Tables) != 128 || len(f.Scan) == 0 {
		t.Fatalf("parsed type %d %dx%d, %d table bytes, %d scan bytes", f.Type, f.Width, f.Height, len(f.Tables), len(f.Scan))
	}
	seq := uint16(65534) // it wraps
	packets := f.Packets(&seq, 1234, 0xCAFE, 1400)
	if len(packets) < 2 {
		t.Fatalf("%d packets for a whole frame", len(packets))
	}
	var scan []byte
	for i, p := range packets {
		if len(p) > 1400 {
			t.Errorf("packet %d is %d bytes", i, len(p))
		}
		if p[0] != 0x80 || p[1]&0x7F != PayloadType {
			t.Errorf("packet %d header % x", i, p[:2])
		}
		marker := p[1]&0x80 != 0
		if marker != (i == len(packets)-1) {
			t.Errorf("packet %d marker %v", i, marker)
		}
		if got := binary.BigEndian.Uint16(p[2:]); got != uint16(65534+i) {
			t.Errorf("packet %d sequence %d", i, got)
		}
		j := p[12:]
		offset := int(j[1])<<16 | int(j[2])<<8 | int(j[3])
		if offset != len(scan) {
			t.Errorf("packet %d says offset %d, %d come before it", i, offset, len(scan))
		}
		if j[4] != 1 || j[5] != 255 || j[6] != 80 || j[7] != 60 {
			t.Errorf("packet %d jpeg header % x", i, j[:8])
		}
		body := j[8:]
		if i == 0 {
			if binary.BigEndian.Uint16(body[2:]) != 128 || !bytes.Equal(body[4:132], f.Tables) {
				t.Error("the first packet does not carry the tables")
			}
			body = body[132:]
		}
		scan = append(scan, body...)
	}
	if !bytes.Equal(scan, f.Scan) {
		t.Error("the packets do not carry the scan whole")
	}
}

func TestWhatRTPJPEGCannotCarry(t *testing.T) {
	gray := image.NewGray(image.Rect(0, 0, 64, 64))
	var g bytes.Buffer
	_ = jpeg.Encode(&g, gray, nil)
	if _, err := Parse(g.Bytes()); err == nil {
		t.Error("a grayscale JPEG was taken")
	}
	if _, err := Parse([]byte("not a picture")); err == nil {
		t.Error("something that is not a JPEG was taken")
	}
	if _, err := Parse(picture(t, 2048, 16)); err != errTooBig {
		t.Errorf("a picture too wide: %v", err)
	}
}

// segment is a JPEG marker segment: the marker, its length and its body.
func segment(marker byte, body ...byte) []byte {
	return append([]byte{0xFF, marker, byte((len(body) + 2) >> 8), byte(len(body) + 2)}, body...)
}

// encoderJPEG is a JPEG laid out as the MT8183's JPEG encoder writes one from YUYV (hardware/jpegenc): a
// JFIF header, the two quantization tables in segments of their own, a 4:2:2 baseline frame whose two
// chroma components share a table, four Huffman tables, then the scan. The scan here is made up: Parse
// does not decode it, only carries it.
func encoderJPEG(w, h int, scan []byte) []byte {
	luma, chroma := make([]byte, 64), make([]byte, 64)
	for i := range 64 {
		luma[i], chroma[i] = byte(i+1), byte(100+i)
	}
	b := []byte{0xFF, 0xD8}
	b = append(b, segment(0xE0, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0)...)
	b = append(b, segment(0xDB, append([]byte{0x00}, luma...)...)...)
	b = append(b, segment(0xDB, append([]byte{0x01}, chroma...)...)...)
	b = append(b, segment(0xC0, 8, byte(h>>8), byte(h), byte(w>>8), byte(w), 3, 1, 0x21, 0, 2, 0x11, 1, 3, 0x11, 1)...)
	for _, class := range []byte{0x00, 0x10, 0x01, 0x11} {
		b = append(b, segment(0xC4, append([]byte{class}, make([]byte, 16)...)...)...)
	}
	b = append(b, segment(0xDA, 3, 1, 0x00, 2, 0x11, 3, 0x11, 0, 63, 0)...)
	b = append(b, scan...)
	return append(b, 0xFF, 0xD9)
}

// The encoder's 4:2:2 JPEG is RTP/JPEG type 0, its tables luma then chroma, from separate segments.
func TestTheJPEGEncodersLayout(t *testing.T) {
	scan := bytes.Repeat([]byte{0x12, 0xFF, 0x00, 0x34}, 1000)
	f, err := Parse(encoderJPEG(1056, 784, scan))
	if err != nil {
		t.Fatal(err)
	}
	if f.Type != 0 || f.Width != 1056 || f.Height != 784 {
		t.Fatalf("type %d %dx%d, want type 0 1056x784", f.Type, f.Width, f.Height)
	}
	if len(f.Tables) != 128 || f.Tables[0] != 1 || f.Tables[63] != 64 || f.Tables[64] != 100 || f.Tables[127] != 163 {
		t.Errorf("tables % x", f.Tables)
	}
	if !bytes.Equal(f.Scan, scan) {
		t.Error("the scan is not carried from after its header to before the end marker")
	}
	seq := uint16(0)
	for i, p := range f.Packets(&seq, 0, 1, 1400) {
		if j := p[12:]; j[0] != 0 || j[4] != 0 || j[5] != 255 || j[6] != 132 || j[7] != 98 {
			t.Fatalf("packet %d jpeg header % x, want type 0, Q 255, 132x98 blocks", i, j[:8])
		}
	}
}

// RFC 2435's main header and the quantization table header, field by field, and the fragments: the
// tables in the first packet of every frame only, each packet as full as the MTU lets it be, the
// offsets running on without a gap, the marker on the last packet alone, one timestamp for the frame.
func TestPacketsFieldByField(t *testing.T) {
	scan := make([]byte, 5000)
	for i := range scan {
		scan[i] = byte(i)
	}
	f, err := Parse(encoderJPEG(640, 480, scan))
	if err != nil {
		t.Fatal(err)
	}
	const mtu = 1000
	seq := uint16(7)
	for frame := range 2 {
		ts := uint32(90000 * frame)
		packets := f.Packets(&seq, ts, 0x01020304, mtu)
		// 20 bytes of headers a packet, and 132 more of tables in the first.
		if want := 1 + (len(scan)-(mtu-20-132)+mtu-20-1)/(mtu-20); len(packets) != want {
			t.Fatalf("%d packets, want %d", len(packets), want)
		}
		offset := 0
		for i, p := range packets {
			last := i == len(packets)-1
			if !last && len(p) != mtu {
				t.Errorf("packet %d is %d bytes, not the MTU", i, len(p))
			}
			if p[0] != 0x80 || p[1] != PayloadType|map[bool]byte{true: 0x80}[last] {
				t.Errorf("packet %d RTP header % x", i, p[:2])
			}
			if got := binary.BigEndian.Uint16(p[2:]); got != uint16(7+frame*len(packets)+i) {
				t.Errorf("packet %d sequence %d", i, got)
			}
			if binary.BigEndian.Uint32(p[4:]) != ts || binary.BigEndian.Uint32(p[8:]) != 0x01020304 {
				t.Errorf("packet %d timestamp or SSRC % x", i, p[4:12])
			}
			j := p[12:]
			if got := int(j[1])<<16 | int(j[2])<<8 | int(j[3]); got != offset {
				t.Errorf("packet %d fragment offset %d, want %d", i, got, offset)
			}
			body := j[8:]
			if i == 0 {
				// MBZ, precision (0: 8-bit tables), and the length of both tables.
				if body[0] != 0 || body[1] != 0 || binary.BigEndian.Uint16(body[2:]) != 128 || !bytes.Equal(body[4:132], f.Tables) {
					t.Errorf("quantization table header % x", body[:4])
				}
				body = body[132:]
			}
			if !bytes.Equal(body, scan[offset:offset+len(body)]) {
				t.Errorf("packet %d carries the wrong part of the scan", i)
			}
			offset += len(body)
		}
		if offset != len(scan) {
			t.Errorf("frame %d: %d of %d scan bytes sent", frame, offset, len(scan))
		}
	}
}

// A scan that fills its packets exactly ends on a full packet, with no empty one after it; an MTU with
// no room for the headers and tables gives no packets at all.
func TestPacketEdges(t *testing.T) {
	const mtu = 400
	first := mtu - 20 - 132
	f, err := Parse(encoderJPEG(64, 64, make([]byte, first+2*(mtu-20))))
	if err != nil {
		t.Fatal(err)
	}
	seq := uint16(0)
	packets := f.Packets(&seq, 0, 0, mtu)
	if len(packets) != 3 || len(packets[2]) != mtu || packets[2][1]&0x80 == 0 {
		t.Errorf("%d packets, the last %d bytes", len(packets), len(packets[len(packets)-1]))
	}
	if got := f.Packets(&seq, 0, 0, 20+132); got != nil {
		t.Errorf("%d packets with no room for the scan", len(got))
	}
}

// What the encoder never writes and RTP/JPEG's types 0 and 1 cannot say is refused, not sent wrong:
// restart markers (types 64 and up), progressive frames, 12-bit tables, and chroma subsampled
// otherwise.
func TestWhatTheEncoderMustNotWrite(t *testing.T) {
	good := encoderJPEG(64, 64, []byte{1, 2, 3})
	sof := bytes.Index(good, []byte{0xFF, 0xC0})
	dqt := bytes.Index(good, []byte{0xFF, 0xDB})
	for name, edit := range map[string]func([]byte) []byte{
		"restart interval": func(b []byte) []byte {
			return append(append(append([]byte(nil), b[:sof]...), segment(0xDD, 0, 4)...), b[sof:]...)
		},
		"progressive":  func(b []byte) []byte { b[sof+1] = 0xC2; return b },
		"12-bit table": func(b []byte) []byte { b[dqt+4] = 0x10; return b },
		"4:1:1":        func(b []byte) []byte { b[sof+11] = 0x41; return b },
	} {
		if _, err := Parse(edit(append([]byte(nil), good...))); err == nil {
			t.Errorf("%s: taken", name)
		}
	}
	// A restart interval of zero is no restart markers, and is taken.
	zero := append(append(append([]byte(nil), good[:sof]...), segment(0xDD, 0, 0)...), good[sof:]...)
	if _, err := Parse(zero); err != nil {
		t.Errorf("a zero restart interval: %v", err)
	}
}
