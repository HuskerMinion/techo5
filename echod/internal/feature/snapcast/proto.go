package snapcast

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// The Snapcast binary protocol (snapcast doc/binary_protocol.md): a 26-byte base message, then the
// typed message it announces. Everything is little-endian, and every time is a {sec, usec} pair, kept
// here as microseconds.
const (
	msgCodecHeader    = 1
	msgWireChunk      = 2
	msgServerSettings = 3
	msgTime           = 4
	msgHello          = 5
	msgClientInfo     = 7
	msgError          = 8

	baseSize = 26

	// maxMessage bounds what a message may claim to be. A codec header or a chunk is a few kilobytes; a
	// size past this is a stream out of step or a peer that is not a Snapcast server, and allocating
	// what it claims would be the daemon's memory spent on its word.
	maxMessage = 1 << 20
)

// base is the base message. sent is the sender's clock, received the receiver's.
type base struct {
	typ      uint16
	id       uint16
	refersTo uint16
	sent     int64
	received int64
	size     uint32
}

func micros(sec, usec int32) int64 { return int64(sec)*1e6 + int64(usec) }

func split(us int64) (sec, usec int32) {
	s := us / 1e6
	u := us % 1e6
	if u < 0 {
		s--
		u += 1e6
	}
	return int32(s), int32(u)
}

// readMessage reads one message, stamping its arrival with now (microseconds, the client's clock).
func readMessage(r io.Reader, now func() int64) (base, []byte, error) {
	var b [baseSize]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return base{}, nil, err
	}
	at := now()

	m := base{
		typ:      binary.LittleEndian.Uint16(b[0:]),
		id:       binary.LittleEndian.Uint16(b[2:]),
		refersTo: binary.LittleEndian.Uint16(b[4:]),
		sent:     micros(int32(binary.LittleEndian.Uint32(b[6:])), int32(binary.LittleEndian.Uint32(b[10:]))),
		received: at,
		size:     binary.LittleEndian.Uint32(b[22:]),
	}
	if m.size > maxMessage {
		return base{}, nil, fmt.Errorf("snapcast: a %d-byte message (type %d) is not believable", m.size, m.typ)
	}

	payload := make([]byte, m.size)
	if _, err := io.ReadFull(r, payload); err != nil {
		return base{}, nil, err
	}
	return m, payload, nil
}

// encode is one message ready to write, sent stamped at sent.
func encode(typ, id uint16, sent int64, payload []byte) []byte {
	b := make([]byte, baseSize+len(payload))
	binary.LittleEndian.PutUint16(b[0:], typ)
	binary.LittleEndian.PutUint16(b[2:], id)
	sec, usec := split(sent)
	binary.LittleEndian.PutUint32(b[6:], uint32(sec))
	binary.LittleEndian.PutUint32(b[10:], uint32(usec))
	binary.LittleEndian.PutUint32(b[22:], uint32(len(payload)))
	copy(b[baseSize:], payload)
	return b
}

var errShort = errors.New("snapcast: message shorter than it says")

// sized reads a uint32 length and that many bytes.
func sized(p []byte) (field, rest []byte, err error) {
	if len(p) < 4 {
		return nil, nil, errShort
	}
	n := binary.LittleEndian.Uint32(p)
	if uint64(n) > uint64(len(p)-4) {
		return nil, nil, errShort
	}
	return p[4 : 4+n], p[4+n:], nil
}

// sizedPayload is a message that is one length-prefixed field: Hello, ServerSettings, ClientInfo.
func sizedPayload(s []byte) []byte {
	b := make([]byte, 4+len(s))
	binary.LittleEndian.PutUint32(b, uint32(len(s)))
	copy(b[4:], s)
	return b
}

func parseCodecHeader(p []byte) (codec string, header []byte, err error) {
	name, rest, err := sized(p)
	if err != nil {
		return "", nil, err
	}
	header, _, err = sized(rest)
	if err != nil {
		return "", nil, err
	}
	return string(name), header, nil
}

// parseWireChunk is a chunk's timestamp (server microseconds, when its first sample was recorded) and
// its encoded audio.
func parseWireChunk(p []byte) (at int64, audio []byte, err error) {
	if len(p) < 8 {
		return 0, nil, errShort
	}
	at = micros(int32(binary.LittleEndian.Uint32(p)), int32(binary.LittleEndian.Uint32(p[4:])))
	audio, _, err = sized(p[8:])
	return at, audio, err
}

// parseTime is the latency a Time reply carries: the server's receipt of the request less its sent.
func parseTime(p []byte) (int64, error) {
	if len(p) < 8 {
		return 0, errShort
	}
	return micros(int32(binary.LittleEndian.Uint32(p)), int32(binary.LittleEndian.Uint32(p[4:]))), nil
}

func timePayload() []byte { return make([]byte, 8) }

func parseError(p []byte) string {
	if len(p) < 4 {
		return "malformed error"
	}
	code := binary.LittleEndian.Uint32(p)
	msg, rest, err := sized(p[4:])
	if err != nil {
		return fmt.Sprintf("error %d", code)
	}
	detail, _, _ := sized(rest)
	return fmt.Sprintf("error %d: %s %s", code, msg, detail)
}

// format is what a codec header says the stream is.
type format struct {
	codec    string
	rate     int
	channels int
	bits     int
}

// formatOf reads the stream's format out of the codec header. FLAC's lives in STREAMINFO, PCM's in the
// WAVE header, Opus's in Snapcast's own eight bytes after "OPUS".
func formatOf(codec string, h []byte) (format, error) {
	f := format{codec: codec}
	switch codec {
	case "flac":
		// "fLaC", then a metadata block header (4 bytes) and STREAMINFO: rate is 20 bits at byte 10 of
		// it, channels-1 three bits, bits-per-sample-1 five bits.
		if len(h) < 8+18 || string(h[:4]) != "fLaC" {
			return f, fmt.Errorf("snapcast: not a flac header")
		}
		si := h[8:]
		f.rate = int(si[10])<<12 | int(si[11])<<4 | int(si[12])>>4
		f.channels = int(si[12]>>1&0x7) + 1
		f.bits = int(si[12]&1)<<4 | int(si[13]>>4) + 1
	case "pcm":
		// RIFF....WAVEfmt  then the fmt chunk: format, channels, rate, byte rate, align, bits.
		if len(h) < 36 || string(h[:4]) != "RIFF" || string(h[8:12]) != "WAVE" {
			return f, fmt.Errorf("snapcast: not a wave header")
		}
		f.channels = int(binary.LittleEndian.Uint16(h[22:]))
		f.rate = int(binary.LittleEndian.Uint32(h[24:]))
		f.bits = int(binary.LittleEndian.Uint16(h[34:]))
	case "opus":
		if len(h) < 12 || binary.LittleEndian.Uint32(h) != 0x4F505553 {
			return f, fmt.Errorf("snapcast: not an opus header")
		}
		f.rate = int(binary.LittleEndian.Uint32(h[4:]))
		f.bits = int(binary.LittleEndian.Uint16(h[8:]))
		f.channels = int(binary.LittleEndian.Uint16(h[10:]))
	default:
		return f, fmt.Errorf("snapcast: no decoder for %q (flac, pcm or opus)", codec)
	}
	return f, nil
}
