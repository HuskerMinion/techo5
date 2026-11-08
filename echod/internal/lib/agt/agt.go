// Package agt implements the Alexa Gadgets Toolkit wire protocol used by the
// Echo Wall Clock: SPP framing with an escaped payload and a 16-bit checksum,
// a minimal protobuf codec, and builders for the directives an Echo sends.
//
// The message shapes follow the behavior of a real Echo captured against the
// clock (see the echo-wall-clock project's PROTOCOL.md): the clock ignores
// anything that does not look exactly like what Amazon's hardware sends, so
// optional fields are omitted rather than sent empty.
//
// Copyright 2026 Rob Lee.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// The protocol follows the echo-wall-clock project and Amazon's Alexa Gadgets
// Toolkit sample code; see NOTICE in this module.
package agt

import (
	"errors"
	"fmt"
)

const (
	// STX, ETX and ESC delimit and escape frames.
	STX byte = 0xF0
	ETX byte = 0xF1
	ESC byte = 0xF2

	cmdByte byte = 0x02
	errByte byte = 0x00

	// DefaultChannel is the RFCOMM channel the clock's data SDP record points at.
	DefaultChannel byte = 4

	// AlertTypeTimer is the Alerts.SetAlert type used for a timer alert.
	AlertTypeTimer = "TIMER"
)

// MaxFrame bounds the reassembly buffer; a frame larger than this is treated as
// line noise and dropped rather than growing without limit.
const MaxFrame = 64 * 1024

func reserved(b byte) bool { return b == STX || b == ETX || b == ESC }

// Framer builds outgoing frames with an incrementing sequence id.
type Framer struct {
	seq byte
}

// NewFramer returns a Framer whose first frame uses first; a real Echo starts at 1.
func NewFramer(first byte) *Framer { return &Framer{seq: first} }

// Seq reports the sequence id the next frame will carry.
func (f *Framer) Seq() byte { return f.seq }

// Frame wraps payload in an SPP frame.
func (f *Framer) Frame(payload []byte) []byte {
	checksum := (int(cmdByte) + int(errByte)) & 0xFFFF
	for _, b := range payload {
		checksum += int(b)
	}
	checksum &= 0xFFFF

	body := make([]byte, 0, len(payload)+8)
	body = append(body, payload...)
	body = append(body, byte(checksum>>8), byte(checksum&0xFF))

	out := make([]byte, 0, len(body)+6)
	out = append(out, STX, cmdByte, errByte, f.seq)
	for _, b := range body {
		if reserved(b) {
			out = append(out, ESC, ESC^b)
			continue
		}
		out = append(out, b)
	}
	out = append(out, ETX)

	// Like Amazon's sample: never use a reserved byte as the sequence id, so it
	// needs no escaping.
	f.seq++
	if f.seq == 0 {
		f.seq = 1
	}
	for reserved(f.seq) {
		f.seq++
	}
	return out
}

// Frame is one decoded SPP frame.
type Frame struct {
	Cmd        byte
	Err        byte
	Seq        byte
	Payload    []byte
	ChecksumOK bool
}

// Deframer reassembles frames from a byte stream; frames may be split across
// reads or coalesced.
type Deframer struct {
	buf     []byte
	inFrame bool
	escaped bool
}

// Feed consumes raw stream bytes and returns every complete frame found.
func (d *Deframer) Feed(data []byte) []Frame {
	var out []Frame
	for _, b := range data {
		switch {
		case b == STX:
			d.buf = d.buf[:0]
			d.inFrame = true
			d.escaped = false
			continue
		case !d.inFrame:
			continue
		case b == ETX:
			if f, ok := d.finish(); ok {
				out = append(out, f)
			}
			d.inFrame = false
			continue
		case d.escaped:
			d.appendByte(ESC ^ b)
			d.escaped = false
		case b == ESC:
			d.escaped = true
		default:
			d.appendByte(b)
		}
	}
	return out
}

func (d *Deframer) appendByte(b byte) {
	if len(d.buf) >= MaxFrame {
		d.inFrame = false
		d.buf = d.buf[:0]
		return
	}
	d.buf = append(d.buf, b)
}

func (d *Deframer) finish() (Frame, bool) {
	// cmd, err, seq, at least one payload byte, two checksum bytes
	if len(d.buf) < 5 {
		return Frame{}, false
	}
	cmd, errb, seq := d.buf[0], d.buf[1], d.buf[2]
	payload := d.buf[3 : len(d.buf)-2]
	found := int(d.buf[len(d.buf)-2])<<8 | int(d.buf[len(d.buf)-1])

	sum := int(cmd) + int(errb)
	for _, b := range payload {
		sum += int(b)
	}
	return Frame{
		Cmd:        cmd,
		Err:        errb,
		Seq:        seq,
		Payload:    append([]byte(nil), payload...),
		ChecksumOK: found == (sum & 0xFFFF),
	}, true
}

// ---------------------------------------------------------------- protobuf

func varint(n uint64) []byte {
	out := make([]byte, 0, 10)
	for {
		b := byte(n & 0x7F)
		n >>= 7
		if n != 0 {
			out = append(out, b|0x80)
			continue
		}
		return append(out, b)
	}
}

func pbBytes(num int, value []byte) []byte {
	out := varint(uint64(num)<<3 | 2)
	out = append(out, varint(uint64(len(value)))...)
	return append(out, value...)
}

func pbString(num int, value string) []byte { return pbBytes(num, []byte(value)) }

func pbVarint(num int, value uint64) []byte {
	out := varint(uint64(num) << 3)
	return append(out, varint(value)...)
}

// Field is one raw protobuf field.
type Field struct {
	Num     int
	Wire    int
	Bytes   []byte
	Varint  uint64
	Present bool
}

// ParseFields decodes a protobuf message into its raw fields.
func ParseFields(data []byte) ([]Field, error) {
	var fields []Field
	i := 0
	for i < len(data) {
		key, n, err := readVarint(data, i)
		if err != nil {
			return nil, err
		}
		i = n
		num, wire := int(key>>3), int(key&7)
		if num == 0 {
			return nil, errors.New("protobuf: field number 0")
		}
		f := Field{Num: num, Wire: wire, Present: true}
		switch wire {
		case 0:
			v, n, err := readVarint(data, i)
			if err != nil {
				return nil, err
			}
			f.Varint, i = v, n
		case 2:
			ln, n, err := readVarint(data, i)
			if err != nil {
				return nil, err
			}
			i = n
			// Compared as the unsigned length it is: converted first, a length near 2^64 comes out
			// negative, passes the check and panics in the slice below.
			if ln > uint64(len(data)-i) {
				return nil, errors.New("protobuf: truncated length-delimited field")
			}
			f.Bytes = data[i : i+int(ln)]
			i += int(ln)
		case 1:
			if i+8 > len(data) {
				return nil, errors.New("protobuf: truncated fixed64")
			}
			f.Bytes, i = data[i:i+8], i+8
		case 5:
			if i+4 > len(data) {
				return nil, errors.New("protobuf: truncated fixed32")
			}
			f.Bytes, i = data[i:i+4], i+4
		default:
			return nil, fmt.Errorf("protobuf: unsupported wire type %d", wire)
		}
		fields = append(fields, f)
	}
	return fields, nil
}

func readVarint(data []byte, i int) (uint64, int, error) {
	var n uint64
	var shift uint
	for {
		if i >= len(data) {
			return 0, i, errors.New("protobuf: truncated varint")
		}
		b := data[i]
		i++
		n |= uint64(b&0x7F) << shift
		if b&0x80 == 0 {
			return n, i, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, i, errors.New("protobuf: varint overflow")
		}
	}
}

// ---------------------------------------------------------------- messages

func header(namespace, name string) []byte {
	h := pbString(1, namespace)
	return append(h, pbString(2, name)...)
}

// directive builds Message{Directive{Header, payload}}; an empty payload is omitted.
func directive(namespace, name string, payload []byte) []byte {
	d := pbBytes(1, header(namespace, name))
	if len(payload) != 0 {
		d = append(d, pbBytes(2, payload)...)
	}
	return pbBytes(1, d)
}

// Discover is byte-for-byte what a real Echo sends on connect: header only.
func Discover() []byte { return directive("Alexa.Discovery", "Discover", nil) }

// State is one entry of a gadget StateListener StateUpdate.
type State struct {
	Name  string
	Value string
}

// StateUpdate reports gadget state, e.g. {"timeinfo": "2026-10-14T10:38:52-04:00"}.
func StateUpdate(states ...State) []byte {
	var payload []byte
	for _, s := range states {
		entry := pbString(1, s.Name)
		entry = append(entry, pbString(2, s.Value)...)
		payload = append(payload, pbBytes(1, entry)...)
	}
	return directive("Alexa.Gadget.StateListener", "StateUpdate", payload)
}

// SetAlert schedules an alert. scheduledTime is the end time, local ISO-8601
// with offset and seconds precision.
func SetAlert(token, alertType, scheduledTime string) []byte {
	payload := pbString(1, token)
	payload = append(payload, pbString(2, alertType)...)
	payload = append(payload, pbString(3, scheduledTime)...)
	return directive("Alerts", "SetAlert", payload)
}

// DeleteAlert removes an alert by token.
func DeleteAlert(token string) []byte {
	return directive("Alerts", "DeleteAlert", pbString(1, token))
}

// ClearIndicator stops the center notification LED.
func ClearIndicator() []byte { return directive("Notifications", "ClearIndicator", nil) }

// SetIndicator pulses the center notification LED.
func SetIndicator() []byte {
	return directive("Notifications", "SetIndicator", pbVarint(1, 1))
}

// TimersActive makes the clock ring: one ~10 s burst of pulses.
func TimersActive() []byte { return StateUpdate(State{"timers", "active"}) }

// TimersCleared tells the clock the ringing timer was stopped.
func TimersCleared() []byte { return StateUpdate(State{"timers", "cleared"}) }

// Message is a decoded gadget-to-Echo message header.
type Message struct {
	Namespace string
	Name      string
	Payload   []byte
}

// DecodeMessage decodes Message{Event{Header, payload}} from the clock into its
// header fields. Unknown or malformed input yields a zero Message. It is what
// comes off the radio, so a panic in it is a malformed message too, never the
// daemon's end.
func DecodeMessage(payload []byte) (decoded Message) {
	defer func() {
		if recover() != nil {
			decoded = Message{}
		}
	}()
	fields, err := ParseFields(payload)
	if err != nil || len(fields) == 0 {
		return Message{}
	}
	inner := fields[0]
	if inner.Wire != 2 {
		return Message{}
	}
	sub, err := ParseFields(inner.Bytes)
	if err != nil {
		return Message{}
	}
	var msg Message
	for _, f := range sub {
		switch {
		case f.Num == 1 && f.Wire == 2:
			hdr, err := ParseFields(f.Bytes)
			if err != nil {
				continue
			}
			for _, h := range hdr {
				if h.Wire != 2 {
					continue
				}
				switch h.Num {
				case 1:
					msg.Namespace = string(h.Bytes)
				case 2:
					msg.Name = string(h.Bytes)
				}
			}
		case f.Num == 2 && f.Wire == 2:
			msg.Payload = f.Bytes
		}
	}
	return msg
}
