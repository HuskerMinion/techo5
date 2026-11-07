// Copyright 2026 Rob Lee.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package agt

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// splitParams separates "key=value" parameters where a value may itself contain
// spaces (the payload hex in the escaping fixtures).
func splitParams(s string) []string {
	var out []string
	for _, tok := range strings.Fields(s) {
		if strings.Contains(tok, "=") || len(out) == 0 {
			out = append(out, tok)
			continue
		}
		out[len(out)-1] += " " + tok
	}
	return out
}

// goldenLine is one line of fixtures/golden-frames.txt, generated from the
// reference implementation (agt.py) and captured from real hardware.
type goldenLine struct {
	name   string
	params map[string]string
	frame  []byte
}

func loadGolden(t *testing.T) []goldenLine {
	t.Helper()
	f, err := os.Open("testdata/golden-frames.txt")
	if err != nil {
		t.Fatalf("open fixtures: %v", err)
	}
	defer f.Close()

	var out []goldenLine
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) != 3 {
			t.Fatalf("malformed fixture line: %q", line)
		}
		gl := goldenLine{name: strings.TrimSpace(parts[0]), params: map[string]string{}}
		for _, field := range splitParams(parts[1]) {
			k, v, ok := strings.Cut(field, "=")
			if !ok {
				t.Fatalf("malformed parameter %q in %q", field, line)
			}
			gl.params[k] = v
		}
		hb := strings.ReplaceAll(strings.TrimSpace(parts[2]), " ", "")
		gl.frame, err = hex.DecodeString(hb)
		if err != nil {
			t.Fatalf("bad hex in fixture %q: %v", gl.name, err)
		}
		out = append(out, gl)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read fixtures: %v", err)
	}
	return out
}

func payloadFromSpec(t *testing.T, spec string) []byte {
	t.Helper()
	var out []byte
	for _, part := range strings.Split(spec, ",") {
		part = strings.ReplaceAll(part, " ", "")
		if part == "" {
			continue
		}
		if b, count, ok := strings.Cut(part, "*"); ok {
			value, err := strconv.ParseUint(b, 16, 8)
			if err != nil {
				t.Fatalf("bad repeat byte %q: %v", b, err)
			}
			n, err := strconv.Atoi(count)
			if err != nil {
				t.Fatalf("bad repeat count %q: %v", count, err)
			}
			for i := 0; i < n; i++ {
				out = append(out, byte(value))
			}
			continue
		}
		b, err := hex.DecodeString(part)
		if err != nil {
			t.Fatalf("bad payload byte %q: %v", part, err)
		}
		out = append(out, b...)
	}
	return out
}

func seqOf(t *testing.T, params map[string]string) byte {
	t.Helper()
	n, err := strconv.Atoi(params["seq"])
	if err != nil {
		t.Fatalf("bad seq %q: %v", params["seq"], err)
	}
	return byte(n)
}

// build reproduces the fixture's payload the way the bridge would build it.
func build(t *testing.T, gl goldenLine) []byte {
	t.Helper()
	switch gl.name {
	case "discover":
		return Discover()
	case "timeinfo":
		return StateUpdate(State{"timeinfo", gl.params["time"]})
	case "clear-indicator":
		return ClearIndicator()
	case "set-alert", "set-alert-derived-token":
		return SetAlert(gl.params["token"], AlertTypeTimer, gl.params["end"])
	case "timers-active":
		return TimersActive()
	case "delete-alert":
		return DeleteAlert(gl.params["token"])
	case "timers-cleared":
		return TimersCleared()
	case "escape-payload", "escape-checksum-low", "escape-checksum-high",
		"seq-before-reserved", "seq-skips-reserved":
		return payloadFromSpec(t, gl.params["payload"])
	default:
		t.Fatalf("fixture %q has no builder", gl.name)
		return nil
	}
}

func TestOutgoingFramesMatchGolden(t *testing.T) {
	for _, gl := range loadGolden(t) {
		if gl.name == "inbound-discover-response" {
			continue
		}
		t.Run(gl.name, func(t *testing.T) {
			got := NewFramer(seqOf(t, gl.params)).Frame(build(t, gl))
			if hex.EncodeToString(got) != hex.EncodeToString(gl.frame) {
				t.Fatalf("frame mismatch\n got %x\nwant %x", got, gl.frame)
			}
		})
	}
}

func TestSeqSkipsReservedAndWraps(t *testing.T) {
	f := NewFramer(239)
	_ = f.Frame([]byte{0x00}) // 239
	// 0xF0/0xF1/0xF2 are reserved, so the next usable id after 0xEF is 0xF3.
	if got := f.Seq(); got != 0xF3 {
		t.Fatalf("after EF got next %#x, want 0xF3", got)
	}
	f = NewFramer(0xF3)
	_ = f.Frame([]byte{0x00})
	if got := f.Seq(); got != 0xF4 {
		t.Fatalf("after F3 got next %d, want F4", got)
	}
	f = NewFramer(0xFF)
	_ = f.Frame([]byte{0x00})
	if got := f.Seq(); got != 1 {
		t.Fatalf("after FF got next %d, want 1 (0 is skipped)", got)
	}
}

func TestDeframerReassemblesGoldenFrames(t *testing.T) {
	lines := loadGolden(t)
	var stream []byte
	for _, gl := range lines {
		stream = append(stream, gl.frame...)
	}

	// Feed one byte at a time: frames must survive arbitrary splitting.
	d := &Deframer{}
	var got []Frame
	for _, b := range stream {
		got = append(got, d.Feed([]byte{b})...)
	}
	if len(got) != len(lines) {
		t.Fatalf("decoded %d frames, want %d", len(got), len(lines))
	}
	for i, f := range got {
		if !f.ChecksumOK {
			t.Fatalf("frame %d (%s): checksum rejected", i, lines[i].name)
		}
	}
}

func TestDeframerRejectsCorruptChecksum(t *testing.T) {
	frame := NewFramer(1).Frame(Discover())
	frame[len(frame)-2] ^= 0xFF // corrupt a checksum byte, keeping the delimiters
	frames := (&Deframer{}).Feed(frame)
	if len(frames) != 1 || frames[0].ChecksumOK {
		t.Fatalf("corrupt frame not flagged: %+v", frames)
	}
}

func TestDecodeDiscoverResponse(t *testing.T) {
	var want goldenLine
	for _, gl := range loadGolden(t) {
		if gl.name == "inbound-discover-response" {
			want = gl
		}
	}
	if want.frame == nil {
		t.Fatal("fixture missing")
	}
	frames := (&Deframer{}).Feed(want.frame)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	msg := DecodeMessage(frames[0].Payload)
	if msg.Namespace != want.params["namespace"] || msg.Name != want.params["name"] {
		t.Fatalf("decoded %q/%q, want %q/%q", msg.Namespace, msg.Name, want.params["namespace"], want.params["name"])
	}
}

func TestDecodeMessageRejectsNoise(t *testing.T) {
	for _, in := range [][]byte{nil, {0x08}, {0xff, 0xff, 0xff}} {
		if msg := DecodeMessage(in); msg.Namespace != "" || msg.Name != "" {
			t.Fatalf("noise %x decoded as %+v", in, msg)
		}
	}
}

func TestTimeinfoRoundTrip(t *testing.T) {
	const stamp = "2026-11-02T21:15:00+00:00"
	frames := (&Deframer{}).Feed(NewFramer(1).Frame(StateUpdate(State{"timeinfo", stamp})))
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	msg := DecodeMessage(frames[0].Payload)
	if msg.Namespace != "Alexa.Gadget.StateListener" || msg.Name != "StateUpdate" {
		t.Fatalf("unexpected message %+v", msg)
	}
	if !strings.Contains(fmt.Sprintf("%q", msg.Payload), stamp) {
		t.Fatalf("payload lost the timestamp: %q", msg.Payload)
	}
}
