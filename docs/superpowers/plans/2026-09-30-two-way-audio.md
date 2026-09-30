# Two-way Audio (Talk Through a Camera) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Talk control on the camera page sends the device's microphones to that camera's own speaker through go2rtc's backchannel, while the camera's picture and sound stay up.

**Architecture:** The device serves its processed microphone frames, halved to 8 kHz and G.711-encoded, as a live WAV at a one-time address on its existing web port (8181). It then asks go2rtc to play that address on the camera: `POST /api/streams?dst=<stream>&src=<address>`. go2rtc's native `http:` source reads the WAV with no ffmpeg. Talking ends when the device closes its response. Whether a camera can be talked to is asked of go2rtc when the view comes up (`GET /api/streams?src=<stream>&microphone`), and the control is drawn only where it can.

**Tech Stack:** Go 1.26 (`echod` module, `github.com/HuskerMinion/techo5/echod`), `net/http`, `httptest`; go2rtc v1.9.14 HTTP API; no new dependencies.

**Spec:** [docs/two-way-audio-plan.md](../../two-way-audio-plan.md) — read it first; this plan argues from it.

## Global Constraints

- Work on a new branch off `main` (e.g. `two-way-audio`), not on `two-way-audio-plan`, which is based on an old main.
- All commands run from `echod/`. Every task ends green on all three builds: `go test ./...`, `go test -tags dot ./...`, `go test -tags spot ./...` (`.github/workflows/build.yml`). `go vet ./...` clean.
- No new modules in `go.mod`.
- go2rtc: the served WAV must be exactly the camera's backchannel codec (PCMA/8000 or PCMU/8000). go2rtc does not transcode on the `http:` path.
- go2rtc: **never** call `DELETE /api/streams`, because it deletes the stream from go2rtc's config. Talking ends by closing the response.
- The `src` query value must be URL-encoded (`url.Values.Encode()`), and must contain no whitespace (go2rtc's `Validate` rejects it).
- Switch `Security.Talk` (JSON `talk_back`) is off on a new device. Talking needs it on, and turning it off ends a talk in progress.
- go2rtc password: written on the setup page, never shown, never logged, never in the diagnostics bundle.
- Talk limits: `talkMax = 2 * time.Minute`, `talkHold = 15 * time.Second`, probe answers kept `10 * time.Minute`, refusal note shown `5 * time.Second`.
- Comments and log lines follow the codebase's voice: plain sentences about what the device does and why. Match the surrounding files.
- Commit messages follow the repo's style (`area: what a person sees`), with no attribution lines.

## Review Focus

1. **go2rtc cannot reach the device** (VLAN, or a Docker bridge without host networking). go2rtc's POST fails. Expected: the view says "Can't talk: …" with go2rtc's own words, Talking() is false, and nothing is left serving. Pinned in Task 4 (`TestARefusalIsSaidOnTheView`).
2. **The view is tapped away during the one-second gap between two watcher polls.** Expected: the talk ends and the view is not resurrected by `holdView`. Pinned in Task 4 (`TestATappedAwayViewIsNotHeldOpen`).
3. **Somebody other than go2rtc fetches the talk address**, or fetches it a second time. Expected: 404, and no second copy of the microphones. Pinned in Task 4 (`TestTheAddressServesOnceAndOnlyItsOwnToken`).
4. **Talk tapped on a camera whose probe is stale or failed** (go2rtc down since). Expected: no talk starts and no panic. The control is not drawn, and a stray tap is a no-op. Pinned in Task 4 (`TestNoTalkWithoutABackchannel`).
5. **The microphones are muted mid-talk.** Expected: the talk ends rather than sending silence. Pinned in Task 4 (`TestTalkEnds/muted`); the two-minute cap in `TestATalkEndsAtItsLongest`.

---

## File Structure

| File | Responsibility |
|---|---|
| `echod/internal/lib/g711/g711.go` (new) | A-law and μ-law encoding, the 16→8 kHz halving filter, the streaming WAV header |
| `echod/internal/lib/g711/g711_test.go` (new) | Reference values, round-trip error, filter response, header bytes |
| `echod/internal/lib/go2rtc/go2rtc.go` (new) | HTTP client: address normalising, stream list, backchannel probe, play, own address |
| `echod/internal/lib/go2rtc/go2rtc_test.go` (new) | Against an `httptest` go2rtc |
| `echod/internal/config/home.go` | `Home.Go2rtc`, `Home.TalkStreams`, writers |
| `echod/internal/config/security.go` | `Security.Talk`, writer |
| `echod/internal/config/talk_test.go` (new) | Writers |
| `echod/internal/feature/home/talk.go` (new) | Stream match, probe, session, WAV handler, start, watcher |
| `echod/internal/feature/home/talk_setup.go` (new) | `SetGo2rtc`, `SetTalkStream` for the setup page |
| `echod/internal/feature/home/talk_test.go` (new) | End-to-end with a fake go2rtc that fetches the address |
| `echod/internal/feature/home/home.go` | Fields on `Feature`; register `/talk/` at build |
| `echod/internal/feature/home/camera.go` | Probe on a fresh view |
| `echod/internal/feature/display/render.go`, `render_camera.go`, `display.go`, `render_show_test.go` | Show: Talk control, note, tap |
| `echod/internal/feature/display/render_spot.go`, `camera_spot.go`, `display_spot.go`, `render_spot_test.go` | Spot: Talk bar |
| `echod/internal/feature/security/security.go` | `talk_back` switch |
| `echod/internal/feature/display/sheet.go`, `sheet_spot.go`, `sheet_test.go` | Privacy & Security row |
| `echod/internal/feature/setup/talk.go` (new), `page.go` | Connections form for go2rtc and stream names; Privacy tab line |
| `echod/internal/feature/diag/bundle.go` | Summary line |
| `echod/internal/feature/detect/detect.go` | Wake word ignored while talking |
| `docs/two-way-audio-plan.md`, `docs/actions.md` or `docs/setup.md` | Status, numbers, user docs |

---

### Task 1: G.711 and the halving filter (`lib/g711`)

**Files:**
- Create: `echod/internal/lib/g711/g711.go`
- Test: `echod/internal/lib/g711/g711_test.go`

**Interfaces:**
- Produces:
  - `type Law byte` with `const ALaw Law = 6; MuLaw Law = 7` (their WAV format tags)
  - `func EncodeALaw(s int16) byte`, `func EncodeMuLaw(s int16) byte`
  - `func Header(l Law) []byte`, a 46-byte WAV header, mono, 8000 Hz, sizes `0xFFFFFFFF`
  - `type Encoder struct{...}`, `func NewEncoder(l Law) *Encoder`, `func (e *Encoder) Encode(in []int16) []byte` (16 kHz in, one byte per two samples out, filter state kept across calls)

- [ ] **Step 1: Write the failing tests**

```go
package g711

import (
	"bytes"
	"math"
	"testing"
)

// The reference decoders (Sun's g711.c, the one everybody's is copied from), here only to measure the
// encoders against.
func decodeALaw(a byte) int {
	a ^= 0x55
	t := int(a&0x0f) << 4
	seg := int(a&0x70) >> 4
	switch seg {
	case 0:
		t += 8
	case 1:
		t += 0x108
	default:
		t += 0x108
		t <<= seg - 1
	}
	if a&0x80 != 0 {
		return t
	}
	return -t
}

func decodeMuLaw(u byte) int {
	u = ^u
	t := (int(u&0x0f) << 3) + 0x84
	t <<= int(u&0x70) >> 4
	if u&0x80 != 0 {
		return 0x84 - t
	}
	return t - 0x84
}

// Values every G.711 implementation agrees on: silence, the loudest either way, and one in a middle segment.
func TestReferenceValues(t *testing.T) {
	for _, c := range []struct {
		s    int16
		a, u byte
	}{
		{0, 0xD5, 0xFF},
		{32767, 0xAA, 0x80},
		{-32768, 0x2A, 0x00},
		{256, 0xC5, 0xE7},
		{-256, 0x45, 0x67},
	} {
		if got := EncodeALaw(c.s); got != c.a {
			t.Errorf("A-law(%d) = %#x, want %#x", c.s, got, c.a)
		}
		if got := EncodeMuLaw(c.s); got != c.u {
			t.Errorf("μ-law(%d) = %#x, want %#x", c.s, got, c.u)
		}
	}
}

// Every sample comes back within G.711's own step: a sixteenth of its size, or 16 near silence.
func TestRoundTripIsWithinAStep(t *testing.T) {
	for s := -32768; s <= 32767; s += 7 {
		allowed := math.Max(16, math.Abs(float64(s))/16)
		if e := math.Abs(float64(decodeALaw(EncodeALaw(int16(s))) - s)); e > allowed {
			t.Fatalf("A-law %d came back %v off", s, e)
		}
		if e := math.Abs(float64(decodeMuLaw(EncodeMuLaw(int16(s))) - s)); e > allowed {
			t.Fatalf("μ-law %d came back %v off", s, e)
		}
	}
}

// The header is byte for byte the one go2rtc writes for the same codec (pkg/wav/wav.go Header), which is
// the one its reader is sure to take.
func TestHeaderIsGo2rtcs(t *testing.T) {
	want := []byte("RIFF\xff\xff\xff\xffWAVEfmt \x12\x00\x00\x00\x06\x00\x01\x00\x40\x1f\x00\x00\x40\x1f\x00\x00\x01\x00\x08\x00\x00\x00data\xff\xff\xff\xff")
	if got := Header(ALaw); !bytes.Equal(got, want) {
		t.Fatalf("A-law header\n got % x\nwant % x", got, want)
	}
	if got := Header(MuLaw); got[20] != 7 || len(got) != len(want) {
		t.Fatalf("μ-law header has format %d, length %d", got[20], len(got))
	}
}

func tone(hz float64, n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(8000 * math.Sin(2*math.Pi*hz*float64(i)/16000))
	}
	return out
}

func rms(s []int16) float64 {
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(s)))
}

// Speech keeps its level through the halving, and what would fold back into it as a whistle does not get
// through: 7 kHz has no place at 8 kHz.
func TestHalvingKeepsSpeechAndDropsWhatWouldAlias(t *testing.T) {
	for _, c := range []struct {
		hz       float64
		min, max float64
	}{{1000, 0.9, 1.1}, {7000, 0, 0.05}} {
		e := NewEncoder(ALaw)
		in := tone(c.hz, 16000)
		var out []int16
		for i := 0; i < len(in); i += 320 { // the microphones' 20 ms frames
			out = e.halve(in[i:i+320], out)
		}
		if len(out) != 8000 {
			t.Fatalf("%v Hz: %d samples out of 16000, want 8000", c.hz, len(out))
		}
		ratio := rms(out[100:]) / rms(in[200:])
		if ratio < c.min || ratio > c.max {
			t.Errorf("%v Hz came through at %.3f of its level, want %.2f–%.2f", c.hz, ratio, c.min, c.max)
		}
	}
}

// A frame of 320 is 160 bytes, and an odd frame does not lose or double a sample at the join.
func TestEncodeCountsAcrossFrames(t *testing.T) {
	e := NewEncoder(MuLaw)
	if n := len(e.Encode(make([]int16, 320))); n != 160 {
		t.Fatalf("a 20 ms frame is %d bytes, want 160", n)
	}
	e = NewEncoder(MuLaw)
	n := len(e.Encode(make([]int16, 3))) + len(e.Encode(make([]int16, 3)))
	if n != 3 {
		t.Fatalf("six samples in two frames of three made %d, want 3", n)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/lib/g711/`
Expected: FAIL to build: `undefined: EncodeALaw` (and the rest).

- [ ] **Step 3: Write the implementation**

```go
// Package g711 is what a camera's speaker takes: G.711 A-law or μ-law at 8 kHz, made from the
// microphones' 16 kHz frames, in a WAV whose length is not known when it starts.
//
// It is here so that go2rtc need not convert anything. Its http: source reads a WAV itself but plays
// it only if it is already in the camera's own codec, and nearly every camera with a speaker takes
// one of these two.
package g711

import (
	"encoding/binary"
	"math"
)

// Law is which of the two, by its WAV format tag.
type Law byte

const (
	ALaw  Law = 6
	MuLaw Law = 7
)

// Rate is what both are sent at.
const Rate = 8000

var (
	aEnd = [8]int{0x1F, 0x3F, 0x7F, 0xFF, 0x1FF, 0x3FF, 0x7FF, 0xFFF}
	uEnd = [8]int{0x3F, 0x7F, 0xFF, 0x1FF, 0x3FF, 0x7FF, 0xFFF, 0x1FFF}
)

func segment(v int, ends *[8]int) int {
	for i, e := range ends {
		if v <= e {
			return i
		}
	}
	return 8
}

// EncodeALaw is Sun's linear2alaw, on 16-bit samples.
func EncodeALaw(s int16) byte {
	v := int(s) >> 3
	mask := byte(0xD5)
	if v < 0 {
		mask = 0x55
		v = -v - 1
	}
	seg := segment(v, &aEnd)
	if seg >= 8 {
		return 0x7F ^ mask
	}
	a := byte(seg << 4)
	if seg < 2 {
		a |= byte(v>>1) & 0x0F
	} else {
		a |= byte(v>>seg) & 0x0F
	}
	return a ^ mask
}

// EncodeMuLaw is Sun's linear2ulaw, on 16-bit samples.
func EncodeMuLaw(s int16) byte {
	const bias, clip = 0x84 >> 2, 8159
	v := int(s) >> 2
	mask := byte(0xFF)
	if v < 0 {
		v = -v
		mask = 0x7F
	}
	if v > clip {
		v = clip
	}
	v += bias
	seg := segment(v, &uEnd)
	if seg >= 8 {
		return 0x7F ^ mask
	}
	return (byte(seg<<4) | byte(v>>(seg+1))&0x0F) ^ mask
}

// Header is a mono WAV at Rate in l, with both sizes 0xFFFFFFFF: a stream that runs until it is closed.
// It is go2rtc's own header for these codecs, byte for byte.
func Header(l Law) []byte {
	b := make([]byte, 0, 46)
	b = append(b, "RIFF\xff\xff\xff\xffWAVEfmt "...)
	b = binary.LittleEndian.AppendUint32(b, 18)
	b = binary.LittleEndian.AppendUint16(b, uint16(l))
	b = binary.LittleEndian.AppendUint16(b, 1) // mono
	b = binary.LittleEndian.AppendUint32(b, Rate)
	b = binary.LittleEndian.AppendUint32(b, Rate) // a byte a sample
	b = binary.LittleEndian.AppendUint16(b, 1)
	b = binary.LittleEndian.AppendUint16(b, 8)
	b = binary.LittleEndian.AppendUint16(b, 0) // no extra format bytes
	return append(b, "data\xff\xff\xff\xff"...)
}

// taps is the halving filter's length: a windowed sinc cut at 4 kHz, long enough that 6 kHz and above
// are gone before they can fold back into speech.
const taps = 31

var kernel = func() [taps]float64 {
	var k [taps]float64
	var sum float64
	mid := float64(taps-1) / 2
	for n := range k {
		x := float64(n) - mid
		sinc := 1.0
		if x != 0 {
			sinc = math.Sin(math.Pi*x/2) / (math.Pi * x / 2)
		}
		k[n] = sinc * (0.54 - 0.46*math.Cos(2*math.Pi*float64(n)/float64(taps-1)))
		sum += k[n]
	}
	for n := range k {
		k[n] /= sum
	}
	return k
}()

// Encoder turns 16 kHz frames into G.711 at 8 kHz. It keeps the filter's last samples between frames,
// so one Encoder is one stream.
type Encoder struct {
	law  Law
	hist [taps]float64
	pos  int
	odd  bool
}

func NewEncoder(l Law) *Encoder { return &Encoder{law: l} }

// Encode is one frame in, half as many bytes out.
func (e *Encoder) Encode(in []int16) []byte {
	half := e.halve(in, make([]int16, 0, len(in)/2+1))
	out := make([]byte, len(half))
	for i, s := range half {
		if e.law == MuLaw {
			out[i] = EncodeMuLaw(s)
		} else {
			out[i] = EncodeALaw(s)
		}
	}
	return out
}

// halve filters and keeps every other sample, appending to out.
func (e *Encoder) halve(in []int16, out []int16) []int16 {
	for _, s := range in {
		e.hist[e.pos] = float64(s)
		e.pos = (e.pos + 1) % taps
		e.odd = !e.odd
		if !e.odd {
			continue
		}
		var acc float64
		for k := range taps {
			acc += kernel[k] * e.hist[(e.pos+k)%taps]
		}
		out = append(out, int16(math.Max(-32768, math.Min(32767, math.Round(acc)))))
	}
	return out
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/lib/g711/ -v`
Expected: PASS, all five tests. If `TestReferenceValues` fails on a μ-law line, check the value against the decoder instead of changing the encoder: `decodeMuLaw(want)` has to be the nearest level to `s`.

- [ ] **Step 5: Commit**

```bash
git add echod/internal/lib/g711
git commit -m "g711: A-law and μ-law at 8 kHz from the microphones' 16 kHz, for a camera's speaker"
```

---

### Task 2: A go2rtc client (`lib/go2rtc`)

**Files:**
- Create: `echod/internal/lib/go2rtc/go2rtc.go`
- Test: `echod/internal/lib/go2rtc/go2rtc_test.go`

**Interfaces:**
- Produces:
  - `type Client struct { Base, User, Pass string; HTTP *http.Client }`
  - `type Codec struct { Name string; Rate int }`
  - `var ErrNoStream`, `var ErrNoBackchannel`
  - `func Normalize(addr string) (string, error)`
  - `func (c *Client) Streams(ctx context.Context) ([]string, error)`
  - `func (c *Client) Backchannel(ctx context.Context, stream string) ([]Codec, error)`
  - `func (c *Client) Play(ctx context.Context, stream, src string) error`
  - `func (c *Client) LocalAddr(ctx context.Context) (netip.Addr, error)`

- [ ] **Step 1: Write the failing tests**

```go
package go2rtc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"testing"
)

func TestNormalize(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"192.168.1.5", "http://192.168.1.5:1984"},
		{"192.168.1.5:1985", "http://192.168.1.5:1985"},
		{"http://go2rtc.lan:1984/", "http://go2rtc.lan:1984"},
		{"https://go2rtc.lan", "https://go2rtc.lan"},
		{" nvr.lan ", "http://nvr.lan:1984"},
	} {
		if got, err := Normalize(c.in); err != nil || got != c.want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	for _, bad := range []string{"", "ftp://x", "http://x/api", "http://"} {
		if _, err := Normalize(bad); err == nil {
			t.Errorf("Normalize(%q) took it", bad)
		}
	}
}

// fake is go2rtc as far as its API goes: a stream list, a probe that says what each stream's camera
// takes, and a play that records what it was asked.
func fake(t *testing.T, user, pass string) (*Client, *[]string) {
	t.Helper()
	var played []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user != "" {
			if u, p, ok := r.BasicAuth(); !ok || u != user || p != pass {
				http.Error(w, "", http.StatusUnauthorized)
				return
			}
		}
		q := r.URL.Query()
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/streams" && q.Get("src") == "":
			w.Write([]byte(`{"front_door":{"producers":[]},"deck":{"producers":[]}}`))
		case r.Method == "GET" && r.URL.Path == "/api/streams" && q.Has("microphone"):
			switch q.Get("src") {
			case "front_door":
				w.Write([]byte(`{"producers":[{"medias":["video, recvonly, H264","audio, recvonly, PCMA/8000","audio, sendonly, PCMA/8000, PCMU/8000"]}],"consumers":[]}`))
			case "deck":
				w.Write([]byte(`{"producers":[{"medias":["video, recvonly, H264","audio, recvonly, AAC/16000"]}],"consumers":[]}`))
			default:
				http.Error(w, "", http.StatusNotFound)
			}
		case r.Method == "POST" && r.URL.Path == "/api/streams":
			if q.Get("dst") == "nope" {
				http.Error(w, "can't find consumer", http.StatusInternalServerError)
				return
			}
			played = append(played, q.Get("dst")+" <- "+q.Get("src"))
			w.Write([]byte(`{}`))
		default:
			http.Error(w, "", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return &Client{Base: srv.URL, User: user, Pass: pass}, &played
}

func TestStreams(t *testing.T) {
	c, _ := fake(t, "", "")
	got, err := c.Streams(context.Background())
	if err != nil || !slices.Equal(got, []string{"deck", "front_door"}) {
		t.Fatalf("Streams = %v, %v", got, err)
	}
}

func TestBackchannel(t *testing.T) {
	c, _ := fake(t, "", "")
	got, err := c.Backchannel(context.Background(), "front_door")
	if err != nil || !slices.Equal(got, []Codec{{"PCMA", 8000}, {"PCMU", 8000}}) {
		t.Fatalf("front_door: %v, %v", got, err)
	}
	if _, err := c.Backchannel(context.Background(), "deck"); !errors.Is(err, ErrNoBackchannel) {
		t.Fatalf("a camera with no speaker: %v, want ErrNoBackchannel", err)
	}
	if _, err := c.Backchannel(context.Background(), "garage"); !errors.Is(err, ErrNoStream) {
		t.Fatalf("a stream go2rtc does not have: %v, want ErrNoStream", err)
	}
}

// The source arrives whole, # and all: go2rtc reads its options from after the #, and a # left raw in the
// query would be taken for the URL's own fragment and never sent.
func TestPlaySendsTheSourceWhole(t *testing.T) {
	c, played := fake(t, "", "")
	src := "http://192.168.1.20:8181/talk/abc.wav#backchannel=1"
	if err := c.Play(context.Background(), "front_door", src); err != nil {
		t.Fatal(err)
	}
	if want := "front_door <- " + src; len(*played) != 1 || (*played)[0] != want {
		t.Fatalf("played %v, want %q", *played, want)
	}
	err := c.Play(context.Background(), "nope", src)
	if err == nil || !contains(err.Error(), "can't find consumer") {
		t.Fatalf("a refusal came back as %v; it should carry go2rtc's own words", err)
	}
}

func TestBasicAuth(t *testing.T) {
	c, _ := fake(t, "admin", "s3cret")
	if _, err := c.Streams(context.Background()); err != nil {
		t.Fatalf("with the account: %v", err)
	}
	c.Pass = "wrong"
	if _, err := c.Streams(context.Background()); err == nil {
		t.Fatal("a wrong password was let in")
	}
}

func TestLocalAddr(t *testing.T) {
	c, _ := fake(t, "", "")
	got, err := c.LocalAddr(context.Background())
	if err != nil || got != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("LocalAddr = %v, %v; want 127.0.0.1", got, err)
	}
}

func contains(s, sub string) bool { return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0) }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

(Use `strings.Contains` in place of the two helpers if the package's tests already import `strings`. They are spelled out here only so the test file compiles on its own.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/lib/go2rtc/`
Expected: FAIL to build: `undefined: Normalize`, `undefined: Client`.

- [ ] **Step 3: Write the implementation**

```go
// Package go2rtc is as much of go2rtc's HTTP API as talking to a camera needs: which streams it has,
// whether a stream's camera has a speaker and what it takes, and playing a source on it.
//
// go2rtc's API has no login unless its api: section sets one, and then it is basic auth. Nothing here
// logs the password, and the errors carry go2rtc's own words and not the request.
package go2rtc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

var (
	ErrNoStream      = errors.New("go2rtc has no such stream")
	ErrNoBackchannel = errors.New("the camera takes no audio")
)

// DefaultPort is go2rtc's API port, which is what an address without one means.
const DefaultPort = "1984"

type Client struct {
	Base string // http://host:1984, as Normalize makes it
	User string
	Pass string
	HTTP *http.Client // nil for one with no overall timeout; every call takes a context
}

// Codec is one codec a camera's speaker takes, as go2rtc names it: PCMA, PCMU, OPUS and so on.
type Codec struct {
	Name string
	Rate int
}

// Normalize makes what somebody typed an address: http:// when there is no scheme, go2rtc's port when
// there is no port and no https, and nothing after it.
func Normalize(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", errors.New("no address")
	}
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	u, err := url.Parse(addr)
	if err != nil {
		return "", err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%s is not an http address", u.Scheme)
	}
	if u.Hostname() == "" {
		return "", errors.New("no host in the address")
	}
	if strings.Trim(u.Path, "/") != "" || u.RawQuery != "" {
		return "", errors.New("the address is go2rtc's own, without a path")
	}
	host := u.Host
	if u.Port() == "" && u.Scheme == "http" {
		host = net.JoinHostPort(u.Hostname(), DefaultPort)
	}
	return u.Scheme + "://" + host, nil
}

func (c *Client) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) do(ctx context.Context, method string, q url.Values) ([]byte, error) {
	u := c.Base + "/api/streams"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	if c.User != "" {
		req.SetBasicAuth(c.User, c.Pass)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNoStream
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, errors.New("go2rtc turned the user and password down")
	case resp.StatusCode/100 != 2:
		said, _, _ := strings.Cut(strings.TrimSpace(string(body)), "\n")
		return nil, fmt.Errorf("go2rtc: %s", said)
	}
	return body, nil
}

// Streams is every stream go2rtc has, by name, sorted.
func (c *Client) Streams(ctx context.Context) ([]string, error) {
	body, err := c.do(ctx, http.MethodGet, nil)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, fmt.Errorf("go2rtc's stream list: %w", err)
	}
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	slices.Sort(names)
	return names, nil
}

// Backchannel asks go2rtc to connect to a stream's camera the way a talking client would, and says what
// its speaker takes. go2rtc lists a camera's media as "audio, sendonly, PCMA/8000, PCMU/8000", and sendonly
// audio is the direction it sends in: the camera's speaker.
func (c *Client) Backchannel(ctx context.Context, stream string) ([]Codec, error) {
	body, err := c.do(ctx, http.MethodGet, url.Values{"src": {stream}, "microphone": {""}})
	if err != nil {
		return nil, err
	}
	var info struct {
		Producers []struct {
			Medias []string `json:"medias"`
		} `json:"producers"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, fmt.Errorf("go2rtc's stream: %w", err)
	}
	var out []Codec
	for _, p := range info.Producers {
		for _, m := range p.Medias {
			parts := strings.Split(m, ", ")
			if len(parts) < 3 || parts[0] != "audio" || parts[1] != "sendonly" {
				continue
			}
			for _, cs := range parts[2:] {
				name, rest, _ := strings.Cut(cs, "/")
				rateS, _, _ := strings.Cut(rest, "/")
				rate, _ := strconv.Atoi(rateS)
				out = append(out, Codec{Name: strings.ToUpper(name), Rate: rate})
			}
		}
	}
	if len(out) == 0 {
		return nil, ErrNoBackchannel
	}
	return out, nil
}

// Play has go2rtc play src on stream's camera. go2rtc fetches src before it answers, and stops when src
// ends. This is the only way to stop it that leaves go2rtc's config alone.
func (c *Client) Play(ctx context.Context, stream, src string) error {
	_, err := c.do(ctx, http.MethodPost, url.Values{"dst": {stream}, "src": {src}})
	return err
}

// LocalAddr is the address this device reaches go2rtc from, which is the one go2rtc can reach it on.
func (c *Client) LocalAddr(ctx context.Context) (netip.Addr, error) {
	u, err := url.Parse(c.Base)
	if err != nil {
		return netip.Addr{}, err
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[u.Scheme]
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), port))
	if err != nil {
		return netip.Addr{}, err
	}
	defer conn.Close()
	ap, err := netip.ParseAddrPort(conn.LocalAddr().String())
	if err != nil {
		return netip.Addr{}, err
	}
	return ap.Addr().Unmap(), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/lib/go2rtc/ -v`
Expected: PASS, all six tests.

- [ ] **Step 5: Commit**

```bash
git add echod/internal/lib/go2rtc
git commit -m "go2rtc: a client for its stream list, a camera's speaker, and playing to it"
```

---

### Task 3: Settings (`config`)

**Files:**
- Modify: `echod/internal/config/home.go` (the `Home` struct near `Reolink Reolink`, and beside `type Reolink` / `func (w HomeWriter) Reolink`)
- Modify: `echod/internal/config/security.go`
- Test: `echod/internal/config/talk_test.go`

**Interfaces:**
- Produces:
  - `config.Home.Go2rtc config.Go2rtc` (`Base, User, Pass string`)
  - `config.Home.TalkStreams map[string]string` (camera entity → go2rtc stream name)
  - `config.Security.Talk bool` (JSON `talk_back`)
  - `config.Set().Home().Go2rtc(g Go2rtc) error`
  - `config.Set().Home().TalkStream(entity, stream string) error` (empty stream removes it)
  - `config.Set().Security().Talk(v bool) error`

- [ ] **Step 1: Write the failing test**

```go
package config

import (
	"path/filepath"
	"testing"
)

func TestTalkSettings(t *testing.T) {
	Use(filepath.Join(t.TempDir(), "state.json"))

	if Get().Security.Talk {
		t.Fatal("talking through cameras is on for a device nobody has set")
	}
	if err := Set().Security().Talk(true); err != nil || !Get().Security.Talk {
		t.Fatalf("the switch did not stay on: %v", err)
	}

	g := Go2rtc{Base: "http://192.168.1.5:1984", User: "admin", Pass: "s3cret"}
	if err := Set().Home().Go2rtc(g); err != nil || Get().Home.Go2rtc != g {
		t.Fatalf("go2rtc = %+v, %v", Get().Home.Go2rtc, err)
	}

	if err := Set().Home().TalkStream("camera.front", "front_door_talk"); err != nil {
		t.Fatal(err)
	}
	before := Get().Home.TalkStreams
	if err := Set().Home().TalkStream("camera.porch", "porch_talk"); err != nil {
		t.Fatal(err)
	}
	if _, leaked := before["camera.porch"]; leaked {
		t.Fatal("a copy read before a change saw the change: the map is shared, not copied")
	}
	if err := Set().Home().TalkStream("camera.front", ""); err != nil {
		t.Fatal(err)
	}
	if got := Get().Home.TalkStreams; len(got) != 1 || got["camera.porch"] != "porch_talk" {
		t.Fatalf("after clearing one: %v", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -run TestTalkSettings`
Expected: FAIL to build: `undefined: Go2rtc`, `Set().Security().Talk undefined`.

- [ ] **Step 3: Write the implementation**

In `config/security.go`, add the field and the writer:

```go
type Security struct {
	SSH    bool `json:"ssh"`
	Camera bool `json:"camera_web"`
	Screen bool `json:"screen_web"`

	// Talk lets the camera page's Talk control send the microphones to a camera's speaker through go2rtc
	// (feature/home/talk.go). Off until somebody turns it on: it is a microphone on the network.
	Talk bool `json:"talk_back"`
}

func (w SecurityWriter) Talk(v bool) error {
	return w.st.Update(func(c *Config) { c.Security.Talk = v })
}
```

In `config/home.go`, add to `Home` right after the `Reolink Reolink` field:

```go
	// Go2rtc is the go2rtc server the camera page talks to cameras through (feature/home/talk.go).
	Go2rtc Go2rtc `json:"go2rtc"`

	// TalkStreams names a camera's go2rtc stream, by camera entity, where matching by name does not find it.
	TalkStreams map[string]string `json:"talk_streams,omitempty"`
```

and next to `type Reolink`:

```go
// Go2rtc is where go2rtc's API answers (Base, as go2rtc.Normalize makes it) and the account its api:
// section asks for, if it asks for one. The password is a secret, never shown again once saved.
type Go2rtc struct {
	Base string `json:"base,omitempty"`
	User string `json:"user,omitempty"`
	Pass string `json:"pass,omitempty"`
}

func (w HomeWriter) Go2rtc(g Go2rtc) error {
	return w.st.Update(func(c *Config) { c.Home.Go2rtc = g })
}

// TalkStream names entity's go2rtc stream, or forgets the name when stream is empty. The map is copied
// rather than changed in place, because Get hands the same map to every reader.
func (w HomeWriter) TalkStream(entity, stream string) error {
	return w.st.Update(func(c *Config) {
		m := maps.Clone(c.Home.TalkStreams)
		if stream == "" {
			delete(m, entity)
		} else {
			if m == nil {
				m = map[string]string{}
			}
			m[entity] = stream
		}
		c.Home.TalkStreams = m
	})
}
```

Add `"maps"` to `home.go`'s imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/`
Expected: PASS, including `TestTalkSettings` and the existing unknown-field test (`unknown_test.go`). New fields must not break a file an older build wrote.

- [ ] **Step 5: Commit**

```bash
git add echod/internal/config
git commit -m "config: go2rtc's address, a camera's stream name, and the talk switch"
```

---

### Task 4: The talk itself (`feature/home/talk.go`)

**Files:**
- Create: `echod/internal/feature/home/talk.go`
- Create: `echod/internal/feature/home/talk_test.go`
- Modify: `echod/internal/feature/home/home.go` (fields on `Feature` after `cameraSoundSw`; registration beside `shared.buildCameraSoundSwitch()` at about line 228)
- Modify: `echod/internal/feature/home/camera.go` (`showCamera`, the second `if fresh {`, after the lock is released, at about line 131)

**Interfaces:**
- Consumes: `g711.Law`, `g711.ALaw`, `g711.MuLaw`, `g711.Header`, `g711.NewEncoder` (Task 1); `go2rtc.Client`, `go2rtc.Codec` (Task 2); `config.Home.Go2rtc`, `config.Home.TalkStreams`, `config.Security.Talk` (Task 3); `web.Handle`, `web.Wake`, `web.Port`; `mic.Get().Listen`; `mute.Get().Muted()`; `f.cameraViewUp(entity)` (camera_sound.go).
- Produces (used by Tasks 5–7):
  - `func (f *Feature) TalkOffered() bool`: the view up now can be talked to
  - `func (f *Feature) Talking() bool`
  - `func (f *Feature) TalkNote() string`: a refusal to show, or ""
  - `func (f *Feature) ToggleTalk()`: start, or end; call it with `go`, because it blocks for up to ~10 s on go2rtc
  - `func (f *Feature) forgetTalkProbes()`
  - `func talkStreamFor(entity, name string, streams []string, named map[string]string) string`

- [ ] **Step 1: Write the failing tests**

```go
package home

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/g711"
	"github.com/HuskerMinion/techo5/echod/internal/lib/go2rtc"
)

func TestTalkStreamFor(t *testing.T) {
	streams := []string{"back_door", "Front_Door", "porch", "garage_talk"}
	named := map[string]string{"camera.garage": "garage_talk", "camera.shed": "shed_on_demand"}
	for _, c := range []struct{ entity, name, want string }{
		{"camera.back_door", "Back door", "back_door"},  // the entity id is the stream (Frigate's naming)
		{"recorder:2", "Porch", "porch"},                  // not from Home Assistant: the name made a name
		{"camera.x", "Front Door", "Front_Door"},          // case aside
		{"camera.garage", "Garage", "garage_talk"},        // set on the setup page
		{"camera.shed", "Shed", "shed_on_demand"},         // set, and not listed yet: still used
		{"camera.attic", "Attic", ""},                     // nothing
		{LocalCamera, "This device", ""},                  // the device's own camera has no stream
	} {
		if got := talkStreamFor(c.entity, c.name, streams, named); got != c.want {
			t.Errorf("talkStreamFor(%q, %q) = %q, want %q", c.entity, c.name, got, c.want)
		}
	}
}

// fakeGo2rtc is go2rtc as the talk sees it. Its play fetches the address it is given from the device the
// test serves, the way go2rtc's http: source does before it answers, and reads it until it ends.
type fakeGo2rtc struct {
	device  string // the test server's URL, which stands in for http://<device>:8181
	streams []string
	codecs  map[string][]go2rtc.Codec
	playErr error

	mu     sync.Mutex
	played []string
	got    []byte
	body   io.ReadCloser
	ended  chan struct{}
}

func (g *fakeGo2rtc) Streams(context.Context) ([]string, error) { return g.streams, nil }

func (g *fakeGo2rtc) Backchannel(_ context.Context, s string) ([]go2rtc.Codec, error) {
	if cs, ok := g.codecs[s]; ok {
		return cs, nil
	}
	return nil, go2rtc.ErrNoBackchannel
}

func (g *fakeGo2rtc) LocalAddr(context.Context) (netip.Addr, error) {
	return netip.MustParseAddr("192.168.1.20"), nil
}

func (g *fakeGo2rtc) Play(_ context.Context, stream, src string) error {
	if g.playErr != nil {
		return g.playErr
	}
	u, err := url.Parse(src)
	if err != nil {
		return err
	}
	resp, err := http.Get(g.device + u.Path)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return fmt.Errorf("the source answered %d", resp.StatusCode)
	}
	g.mu.Lock()
	g.played = append(g.played, stream+" <- "+src)
	g.body = resp.Body
	g.ended = make(chan struct{})
	g.mu.Unlock()
	go func() {
		buf := make([]byte, 512)
		for {
			n, err := resp.Body.Read(buf)
			g.mu.Lock()
			g.got = append(g.got, buf[:n]...)
			g.mu.Unlock()
			if err != nil {
				close(g.ended)
				return
			}
		}
	}()
	return nil
}

func (g *fakeGo2rtc) received() []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]byte(nil), g.got...)
}

// talkRig is a device with a camera up, the switch on, and go2rtc answering for "front_door" in A-law.
type talkRig struct {
	f      *Feature
	g      *fakeGo2rtc
	env    talkEnv
	frames chan []int16
	muted  bool
	mu     sync.Mutex
}

func newTalkRig(t *testing.T) *talkRig {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Security().Talk(true); err != nil {
		t.Fatal(err)
	}
	r := &talkRig{f: &Feature{}, frames: make(chan []int16, 8)}
	r.g = &fakeGo2rtc{
		streams: []string{"front_door", "deck"},
		codecs:  map[string][]go2rtc.Codec{"front_door": {{Name: "PCMA", Rate: 8000}}, "deck": {{Name: "OPUS", Rate: 48000}}},
	}
	r.env = talkEnv{
		backend:   func() (talkBackend, bool) { return r.g, true },
		portReady: func(context.Context) error { return nil },
		frames:    func() (<-chan []int16, func()) { return r.frames, func() {} },
		muted:     func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.muted },
		poll:      10 * time.Millisecond,
		max:       talkMax,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.f.serveTalk(w, req, r.env)
	}))
	t.Cleanup(srv.Close)
	r.g.device = srv.URL
	r.f.cam = CameraView{Entity: "camera.front_door", Name: "Front door", Until: time.Now().Add(time.Minute)}
	r.f.probeTalk("camera.front_door", "Front door", r.env)
	return r
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestTalkIsOfferedOnlyWhereTheCameraHasASpeaker(t *testing.T) {
	r := newTalkRig(t)
	if !r.f.TalkOffered() {
		t.Fatal("a camera whose speaker takes A-law was not offered Talk")
	}
	r.f.cam = CameraView{Entity: "camera.deck", Name: "Deck", Until: time.Now().Add(time.Minute)}
	r.f.probeTalk("camera.deck", "Deck", r.env)
	if r.f.TalkOffered() {
		t.Fatal("a camera whose speaker takes only Opus was offered Talk, which go2rtc would refuse")
	}
	r.f.cam = CameraView{Entity: "camera.front_door", Name: "Front door", Until: time.Now().Add(time.Minute)}
	if err := config.Set().Security().Talk(false); err != nil {
		t.Fatal(err)
	}
	if r.f.TalkOffered() {
		t.Fatal("Talk was offered with its switch off")
	}
}

func TestTalkingSendsTheMicrophonesAsG711(t *testing.T) {
	r := newTalkRig(t)
	r.f.startTalk(r.env)
	if !r.f.Talking() {
		t.Fatalf("not talking; note %q", r.f.TalkNote())
	}
	if p := r.g.played; len(p) != 1 || !strings.HasPrefix(p[0], "front_door <- http://192.168.1.20:8181/talk/") {
		t.Fatalf("go2rtc was asked %v", p)
	}
	r.frames <- make([]int16, 320)
	want := len(g711.Header(g711.ALaw)) + 160
	eventually(t, "a frame of the microphones did not reach go2rtc", func() bool { return len(r.g.received()) >= want })
	if got := r.g.received(); string(got[:46]) != string(g711.Header(g711.ALaw)) || got[46] != 0xD5 {
		t.Fatalf("go2rtc got % x…, want the A-law header and A-law silence", got[:47])
	}
	r.f.ToggleTalk()
	eventually(t, "the stream did not end when Talk was tapped again", func() bool {
		select {
		case <-r.g.ended:
			return true
		default:
			return false
		}
	})
	if r.f.Talking() {
		t.Fatal("still talking after the tap that ends it")
	}
}

func TestTheAddressServesOnceAndOnlyItsOwnToken(t *testing.T) {
	r := newTalkRig(t)
	r.f.startTalk(r.env)
	u, _ := url.Parse(strings.SplitN(r.g.played[0], " <- ", 2)[1])
	for _, path := range []string{u.Path, "/talk/0123456789abcdef0123456789abcdef.wav", "/talk/.wav", "/talk/"} {
		resp, err := http.Get(r.g.device + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s answered %d: a second listener, or a guess, was given the microphones", path, resp.StatusCode)
		}
	}
}

func TestTalkEnds(t *testing.T) {
	for _, c := range []struct {
		name string
		do   func(r *talkRig)
	}{
		{"view closed", func(r *talkRig) { r.f.HideCamera() }},
		{"another camera", func(r *talkRig) {
			r.f.mu.Lock()
			r.f.cam = CameraView{Entity: "camera.deck", Until: time.Now().Add(time.Minute)}
			r.f.mu.Unlock()
		}},
		{"switched off", func(r *talkRig) { config.Set().Security().Talk(false) }},
		{"muted", func(r *talkRig) { r.mu.Lock(); r.muted = true; r.mu.Unlock() }},
		{"go2rtc hung up", func(r *talkRig) {
			r.g.mu.Lock()
			r.g.body.Close()
			r.g.mu.Unlock()
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := newTalkRig(t)
			r.f.startTalk(r.env)
			if !r.f.Talking() {
				t.Fatal("did not start")
			}
			c.do(r)
			if c.name == "go2rtc hung up" {
				r.frames <- make([]int16, 320) // a write is what finds a closed connection soonest
			}
			eventually(t, "still talking", func() bool { return !r.f.Talking() })
		})
	}
}

// The longest a talk lasts is the environment's, so a test need not wait two minutes, and nothing writes a
// running talk's start time under the watcher reading it.
func TestATalkEndsAtItsLongest(t *testing.T) {
	r := newTalkRig(t)
	r.env.max = 50 * time.Millisecond
	r.f.startTalk(r.env)
	if !r.f.Talking() {
		t.Fatal("did not start")
	}
	eventually(t, "a talk went on past its longest", func() bool { return !r.f.Talking() })
}

func TestTalkingHoldsTheViewOpen(t *testing.T) {
	r := newTalkRig(t)
	r.f.cam.Until = time.Now().Add(time.Second)
	r.f.startTalk(r.env)
	eventually(t, "a view about to time out was not held while somebody talked", func() bool {
		r.f.mu.Lock()
		defer r.f.mu.Unlock()
		return time.Until(r.f.cam.Until) > talkHold/2
	})
}

func TestATappedAwayViewIsNotHeldOpen(t *testing.T) {
	r := newTalkRig(t)
	r.f.startTalk(r.env)
	r.f.HideCamera()
	eventually(t, "still talking", func() bool { return !r.f.Talking() })
	if _, up := r.f.Camera(); up {
		t.Fatal("a view tapped away was brought back by the talk holding it")
	}
}

func TestARefusalIsSaidOnTheView(t *testing.T) {
	r := newTalkRig(t)
	r.g.playErr = errors.New("go2rtc: can't find consumer")
	r.f.startTalk(r.env)
	if r.f.Talking() {
		t.Fatal("talking after go2rtc refused")
	}
	if n := r.f.TalkNote(); !strings.Contains(n, "can't find consumer") {
		t.Fatalf("the note is %q; it should carry go2rtc's words", n)
	}
}

func TestNoTalkWithoutABackchannel(t *testing.T) {
	r := newTalkRig(t)
	r.f.cam = CameraView{Entity: "camera.attic", Until: time.Now().Add(time.Minute)} // never probed
	r.f.startTalk(r.env)
	if r.f.Talking() || len(r.g.played) != 0 {
		t.Fatal("a camera nobody found a speaker on was talked to")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/feature/home/ -run 'Talk'`
Expected: FAIL to build: `undefined: talkStreamFor`, `talkEnv`, `serveTalk`, and the rest.

- [ ] **Step 3: Add the fields to `Feature`** (`home.go`, after `cameraSoundSw *esphome.Switch`)

```go
	// talk is the camera being talked to, nil when nobody is; talkCan is what each camera was found
	// able to take, by entity; talkNote is a refusal to show on the view until talkNoteUntil. See talk.go.
	talk          *talkSession
	talkCan       map[string]talkProbe
	talkNote      string
	talkNoteUntil time.Time
```

- [ ] **Step 4: Write `talk.go`**

```go
package home

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/lib/g711"
	"github.com/HuskerMinion/techo5/echod/internal/lib/go2rtc"
)

// Talking back through a camera: the microphones to the camera's own speaker, through go2rtc. The device
// serves them as a live G.711 WAV at a one-time address on its web port and asks go2rtc to play that on
// the camera, and ends it by ending the WAV. See docs/two-way-audio-plan.md.

const (
	// talkMax is the longest a talk lasts: long enough for any conversation at a door, and short enough
	// that a Talk left on does not leave a room on a camera's speaker all afternoon.
	talkMax = 2 * time.Minute

	// talkHold is how far ahead a view is kept while somebody talks, so it does not time out mid-sentence.
	talkHold = 15 * time.Second

	// talkProbeKeep is how long what a camera takes is remembered: asking connects to the camera.
	talkProbeKeep = 10 * time.Minute

	// talkNoteFor is how long a refusal stays on the view.
	talkNoteFor = 5 * time.Second
)

// talkPoll is how often a talk checks whether it should still be going. A variable only so tests can wait
// less.
var talkPoll = time.Second

// talkBackend is go2rtc, as the talk uses it.
type talkBackend interface {
	Streams(ctx context.Context) ([]string, error)
	Backchannel(ctx context.Context, stream string) ([]go2rtc.Codec, error)
	Play(ctx context.Context, stream, src string) error
	LocalAddr(ctx context.Context) (netip.Addr, error)
}

// talkEnv is what a talk reaches outside this feature for, as a test can replace it.
type talkEnv struct {
	backend   func() (talkBackend, bool)
	portReady func(context.Context) error
	frames    func() (<-chan []int16, func())
	muted     func() bool
	poll      time.Duration
	max       time.Duration // the longest a talk lasts, talkMax but for tests
}

func talkHere() talkEnv {
	return talkEnv{
		backend: func() (talkBackend, bool) {
			g := config.Get().Home.Go2rtc
			if g.Base == "" {
				return nil, false
			}
			return &go2rtc.Client{Base: g.Base, User: g.User, Pass: g.Pass}, true
		},
		portReady: webPortReady,
		frames:    func() (<-chan []int16, func()) { return mic.Get().Listen("talk") },
		muted: func() bool {
			m, _ := mute.Get().Muted()
			return m
		},
		poll: talkPoll,
		max:  talkMax,
	}
}

// webPortReady opens the web port if nothing else had it open, and waits until it answers: go2rtc fetches
// the address as soon as it is asked to.
func webPortReady(ctx context.Context) error {
	web.Wake()
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(web.Port))
	for {
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("the device's web port did not open")
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// talkProbe is what go2rtc said about one camera: its stream, the law its speaker takes, and if it cannot
// be talked to, why.
type talkProbe struct {
	stream string
	law    g711.Law
	ok     bool
	why    string
	at     time.Time
}

// streamName is a camera's name as go2rtc streams are usually named: "Front Door" is front_door.
func streamName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r == ' ', r == '-':
			b.WriteByte('_')
		}
	}
	return b.String()
}

// talkStreamFor is entity's go2rtc stream: the one named for it on the setup page, whether or not go2rtc
// lists it yet, or the one whose name matches the entity's id or the camera's name, exactly and then in
// any case. Nothing in a camera entity says which stream it is, so this is a match and not a lookup.
func talkStreamFor(entity, name string, streams []string, named map[string]string) string {
	if entity == LocalCamera {
		return ""
	}
	if s := named[entity]; s != "" {
		return s
	}
	var want []string
	if id, ok := strings.CutPrefix(entity, "camera."); ok {
		want = append(want, id)
	}
	if n := streamName(name); n != "" {
		want = append(want, n)
	}
	for _, w := range want {
		for _, s := range streams {
			if s == w {
				return s
			}
		}
	}
	for _, w := range want {
		for _, s := range streams {
			if strings.EqualFold(s, w) {
				return s
			}
		}
	}
	return ""
}

// talkLaw is the law to send in, from what the camera's speaker takes: A-law first, since it is what most
// cameras list first, and only at 8 kHz, since go2rtc will not resample.
func talkLaw(cs []go2rtc.Codec) (g711.Law, bool) {
	for _, want := range []struct {
		name string
		law  g711.Law
	}{{"PCMA", g711.ALaw}, {"PCMU", g711.MuLaw}} {
		for _, c := range cs {
			if c.Name == want.name && c.Rate == g711.Rate {
				return want.law, true
			}
		}
	}
	return 0, false
}

// probeTalk asks go2rtc whether a camera just put up can be talked to, once in talkProbeKeep. It runs
// while the first picture loads, so the Talk control is there by the time anybody looks for it.
func (f *Feature) probeTalk(entity, name string, env talkEnv) {
	if entity == LocalCamera || !config.Get().Security.Talk {
		return
	}
	f.mu.Lock()
	p, seen := f.talkCan[entity]
	f.mu.Unlock()
	if seen && time.Since(p.at) < talkProbeKeep {
		return
	}
	b, ok := env.backend()
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	p = talkProbe{at: time.Now()}
	if streams, err := b.Streams(ctx); err != nil {
		p.why = err.Error()
	} else if p.stream = talkStreamFor(entity, name, streams, config.Get().Home.TalkStreams); p.stream == "" {
		p.why = "go2rtc has no stream by this camera's name"
	} else if cs, err := b.Backchannel(ctx, p.stream); err != nil {
		p.why = err.Error()
	} else if p.law, p.ok = talkLaw(cs); !p.ok {
		p.why = fmt.Sprintf("its speaker takes %v, not G.711 at 8 kHz", cs)
	}
	f.mu.Lock()
	if f.talkCan == nil {
		f.talkCan = map[string]talkProbe{}
	}
	f.talkCan[entity] = p
	f.mu.Unlock()
	slog.Info("talk: camera asked about", "entity", entity, "stream", p.stream, "can", p.ok, "why", p.why)
	f.Changed.Emit(struct{}{})
}

// forgetTalkProbes has every camera asked about again, for when go2rtc or a stream name changes.
func (f *Feature) forgetTalkProbes() {
	f.mu.Lock()
	f.talkCan = nil
	f.mu.Unlock()
}

// TalkOffered is whether the view up now can be talked to, which is when the Talk control is drawn.
func (f *Feature) TalkOffered() bool {
	if !config.Get().Security.Talk {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cam.Entity == "" || time.Now().After(f.cam.Until) {
		return false
	}
	return f.talkCan[f.cam.Entity].ok
}

func (f *Feature) Talking() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.talk != nil
}

// TalkNote is why a talk just failed, for the view, or "".
func (f *Feature) TalkNote() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if time.Now().Before(f.talkNoteUntil) {
		return f.talkNote
	}
	return ""
}

func (f *Feature) noteTalk(s string) {
	f.mu.Lock()
	f.talkNote, f.talkNoteUntil = s, time.Now().Add(talkNoteFor)
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})
}

// talkSession is one talk: the camera, its stream and law, the one-time token its address carries, and
// whether go2rtc has come for it.
type talkSession struct {
	entity, stream string
	law            g711.Law
	token          string
	began          time.Time
	served         atomic.Bool
	done           chan struct{}
	once           sync.Once
}

func (s *talkSession) over() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func newTalkToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ToggleTalk starts talking to the camera on the view, or stops. It waits on go2rtc, so the screen calls
// it with go.
func (f *Feature) ToggleTalk() {
	f.mu.Lock()
	s := f.talk
	f.mu.Unlock()
	if s != nil {
		f.endTalk(s, "tapped")
		return
	}
	f.startTalk(talkHere())
}

func (f *Feature) startTalk(env talkEnv) {
	f.mu.Lock()
	entity := f.cam.Entity
	p := f.talkCan[entity]
	up := entity != "" && time.Now().Before(f.cam.Until)
	if f.talk != nil || !up || !p.ok || !config.Get().Security.Talk {
		f.mu.Unlock()
		return
	}
	s := &talkSession{entity: entity, stream: p.stream, law: p.law, token: newTalkToken(), began: time.Now(), done: make(chan struct{})}
	f.talk = s
	f.mu.Unlock()
	f.Changed.Emit(struct{}{})

	if err := f.connectTalk(s, env); err != nil {
		wasOver := s.over()
		f.endTalk(s, "")
		if !wasOver {
			f.noteTalk("Can't talk: " + err.Error())
			slog.Warn("talk: could not start", "entity", entity, "stream", s.stream, "err", err)
		}
		return
	}
	slog.Info("talk: started", "entity", entity, "stream", s.stream)
	go f.watchTalk(s, env)
}

// connectTalk has go2rtc play the talk's address on the camera, and waits until it has come for it.
func (f *Feature) connectTalk(s *talkSession, env talkEnv) error {
	b, ok := env.backend()
	if !ok {
		return errors.New("go2rtc is not set up")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := env.portReady(ctx); err != nil {
		return err
	}
	addr, err := b.LocalAddr(ctx)
	if err != nil {
		return fmt.Errorf("go2rtc does not answer: %w", err)
	}
	src := fmt.Sprintf("http://%s/talk/%s.wav", netip.AddrPortFrom(addr, web.Port), s.token)
	if err := b.Play(ctx, s.stream, src); err != nil {
		return err
	}
	// go2rtc reads the source before it answers. A play it took without reading it is a play of nothing.
	deadline := time.Now().Add(3 * time.Second)
	for !s.served.Load() {
		if s.over() {
			return errors.New("ended while connecting")
		}
		if time.Now().After(deadline) {
			return errors.New("go2rtc never came for the sound")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

// endTalk ends s, whoever asks first; the WAV ending is what stops go2rtc. why is for the log, empty for
// a talk that never started.
func (f *Feature) endTalk(s *talkSession, why string) {
	s.once.Do(func() { close(s.done) })
	f.mu.Lock()
	mine := f.talk == s
	if mine {
		f.talk = nil
	}
	f.mu.Unlock()
	if !mine {
		return
	}
	if why != "" {
		slog.Info("talk: ended", "entity", s.entity, "why", why, "after", time.Since(s.began).Round(time.Second))
	}
	f.Changed.Emit(struct{}{})
}

// watchTalk ends a talk when anything that should end it does, and holds the view open while it goes on.
// It polls, as the camera's sound does (watchCameraSound): the view has no event for its own end.
func (f *Feature) watchTalk(s *talkSession, env talkEnv) {
	for {
		select {
		case <-s.done:
			return
		case <-time.After(env.poll):
		}
		switch {
		case !f.cameraViewUp(s.entity):
			f.endTalk(s, "the view closed")
		case !config.Get().Security.Talk:
			f.endTalk(s, "switched off")
		case env.muted():
			f.endTalk(s, "the microphones are muted")
		case time.Since(s.began) >= env.max:
			f.endTalk(s, "the longest a talk lasts")
		default:
			f.holdView(s.entity)
		}
	}
}

// holdView keeps entity's view up at least talkHold more. The view has to still be up, checked under the
// same lock: a view tapped away since the last look stays away.
func (f *Feature) holdView(entity string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if f.cam.Entity == entity && now.Before(f.cam.Until) && f.cam.Until.Sub(now) < talkHold {
		f.cam.Until = now.Add(talkHold)
	}
}

// talkOpen is the switch and a talk together: the address is there only while somebody is talking.
func (f *Feature) talkOpen() bool { return config.Get().Security.Talk && f.Talking() }

// serveTalk is the talk's address: the WAV header, then the microphones 20 ms at a time, halved and
// encoded, until the talk ends or go2rtc hangs up. It serves the talk's own token, once.
func (f *Feature) serveTalk(w http.ResponseWriter, r *http.Request, env talkEnv) {
	token, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/talk/"), ".wav")
	f.mu.Lock()
	s := f.talk
	f.mu.Unlock()
	if !ok || s == nil || subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 || !s.served.CompareAndSwap(false, true) {
		http.NotFound(w, r)
		return
	}
	frames, stop := env.frames()
	defer stop()
	enc := g711.NewEncoder(s.law)
	w.Header().Set("Content-Type", "audio/wav")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	if _, err := w.Write(g711.Header(s.law)); err != nil {
		f.endTalk(s, "go2rtc hung up")
		return
	}
	for {
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-s.done:
			return
		case <-r.Context().Done():
			f.endTalk(s, "go2rtc hung up")
			return
		case fr, ok := <-frames:
			if !ok {
				f.endTalk(s, "the microphones stopped")
				return
			}
			if _, err := w.Write(enc.Encode(fr)); err != nil {
				f.endTalk(s, "go2rtc hung up")
				return
			}
		}
	}
}

// registerTalk puts the talk's address on the web port. Built with the feature, before anything runs.
func (f *Feature) registerTalk() {
	web.Handle("/talk/", "", f.talkOpen, func(w http.ResponseWriter, r *http.Request) {
		f.serveTalk(w, r, talkHere())
	})
}
```

- [ ] **Step 5: Wire it in**

In `home.go`, next to `shared.buildCameraSoundSwitch()`:

```go
		shared.registerTalk()
```

In `camera.go` `showCamera`, inside the second `if fresh {` (the one after `f.mu.Unlock()` that starts the frames), after the camera sound block:

```go
		// Whether this camera can be talked to is asked while its first picture loads (talk.go).
		go f.probeTalk(entity, name, talkHere())
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/feature/home/ -run 'Talk|Camera' -race -v`
Expected: PASS, all talk tests plus the existing camera sound tests. Then run `go test ./... && go test -tags dot ./... && go test -tags spot ./...`. The Dot build must still compile, since `home` is built there too.

If `go2rtc hung up` is flaky: the server only notices a closed connection on a write or when its background read sees EOF. The test sends a frame for that reason. Raise `eventually`'s deadline, not the production code.

- [ ] **Step 7: Commit**

```bash
git add echod/internal/feature/home
git commit -m "Camera page: talk to a camera's speaker through go2rtc, while its view is up"
```

---

### Task 5: The Show's Talk control

**Files:**
- Modify: `echod/internal/feature/display/render.go` (scene fields near `cameraSound bool` at about line 146; `setCameraTalkAt`/`cameraTalkTapped` beside `setCameraSoundAt` at about line 694; a `cameraTalkAt image.Rectangle` field beside `cameraSoundAt`)
- Modify: `echod/internal/feature/display/render_camera.go`
- Modify: `echod/internal/feature/display/display.go` (scene fill at about line 1706; tap routing at about line 767)
- Test: `echod/internal/feature/display/render_show_test.go`

**Interfaces:**
- Consumes: `home.Get().TalkOffered()`, `Talking()`, `TalkNote()`, `ToggleTalk()` (Task 4).
- Produces: scene fields `cameraTalk`, `cameraTalking bool`, `cameraTalkNote string`; `func (r *renderer) cameraTalkBox(label string, sound bool) image.Rectangle`; `func (r *renderer) cameraTalkTapped(p image.Point) bool`.

- [ ] **Step 1: Write the failing test** (append to `render_show_test.go`)

```go
// Talk sits in the bottom strip beside the sound's control, never on it and never on the hint, and is
// tappable only while it is drawn.
func TestTheTalkControlIsWhereItIsDrawn(t *testing.T) {
	at := time.Date(2026, 9, 30, 14, 7, 0, 0, time.Local)
	for _, size := range []image.Rectangle{image.Rect(0, 0, drawnFor, 600), image.Rect(0, 0, 1280, 800)} {
		r := newRenderer(image.NewRGBA(size))
		cam := scene{now: at, phase: "idle", showCamera: true,
			camera: home.CameraView{Entity: "camera.deck", Name: "Deck"}}
		for _, sound := range []bool{false, true} {
			box := r.cameraTalkBox("End talk", sound)
			centre := box.Min.Add(image.Pt(box.Dx()/2, box.Dy()/2))

			cam.cameraSound, cam.cameraTalk = sound, false
			r.draw(cam)
			if r.cameraTalkTapped(centre) {
				t.Fatalf("%v sound=%v: a tap found a Talk control where none is drawn", size, sound)
			}
			cam.cameraTalk = true
			for _, talking := range []bool{false, true} {
				cam.cameraTalking = talking
				r.draw(cam)
				if !r.cameraTalkTapped(centre) {
					t.Fatalf("%v sound=%v talking=%v: a tap on Talk at %v was missed", size, sound, talking, centre)
				}
				if r.cameraSoundTapped(centre) {
					t.Fatalf("%v: a tap on Talk was taken for the sound's control", size)
				}
			}
			if sound && box.Overlaps(r.cameraSoundBox("Unmute")) {
				t.Errorf("%v: Talk %v overlaps the sound's control", size, box)
			}
			if box.Min.X < r.margin || box.Max.Y > r.h {
				t.Errorf("%v: Talk is off the page at %v", size, box)
			}
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/feature/display/ -run TestTheTalkControl`
Expected: FAIL to build: `cam.cameraTalk undefined`, `r.cameraTalkBox undefined`.

- [ ] **Step 3: Implement**

`render.go`, the scene fields, after `cameraSoundLive bool`:

```go
	// cameraTalk is whether the view's camera can be talked to, which is when Talk is drawn; cameraTalking
	// whether somebody is, which is what it says; cameraTalkNote a refusal to say in the hint's place.
	cameraTalk     bool
	cameraTalking  bool
	cameraTalkNote string
```

`render.go`, beside `cameraSoundAt`, add a field `cameraTalkAt image.Rectangle` (under `weatherMu` like it). Beside `setCameraSoundAt`:

```go
func (r *renderer) setCameraTalkAt(b image.Rectangle) {
	r.weatherMu.Lock()
	r.cameraTalkAt = b
	r.weatherMu.Unlock()
}

// cameraTalkTapped is whether a tap at p landed on the camera page's Talk control, as last drawn.
func (r *renderer) cameraTalkTapped(p image.Point) bool {
	r.weatherMu.Lock()
	defer r.weatherMu.Unlock()
	return !r.cameraTalkAt.Empty() && p.In(r.cameraTalkAt)
}
```

`render_camera.go`: the hint uses the note when there is one:

```go
	if s.cameraTalkNote != "" {
		hint = s.cameraTalkNote
	}
```

(insert just before the strip's `draw.Draw(... r.h-36 ...)`). Replace the sound block's early `return` / trailing `r.setCameraSoundAt(image.Rectangle{})` with an if/else so the function goes on. Then add after it:

```go
	// Talk, beside the sound's control and clear of the hint: a tap sends the room to the camera's
	// speaker, a second tap stops. Only for a camera go2rtc says has a speaker.
	if s.cameraTalk {
		label := "Talk"
		if s.cameraTalking {
			label = "End talk"
		}
		b := r.cameraTalkBox(label, s.cameraSound)
		r.bevel(b, shift(ember, 16), !s.cameraTalking) // pressed in while talking
		m := r.tiny.Metrics()
		mid := b.Min.Y + (b.Dy()+m.Ascent.Ceil()-m.Descent.Ceil())/2
		r.text(r.tiny, label, b.Min.X+(b.Dx()-r.width(r.tiny, label))/2, mid, cream)
		r.setCameraTalkAt(b)
	} else {
		r.setCameraTalkAt(image.Rectangle{})
	}
```

And the box, after `cameraSoundBox`:

```go
// cameraTalkBox is where Talk is drawn: the right end of the bottom strip, or just left of the sound's
// control when there is one. That is measured at its widest ("Unmute"), so Talk does not move when the
// sound's label changes under somebody's finger.
func (r *renderer) cameraTalkBox(label string, sound bool) image.Rectangle {
	right := r.w - r.margin
	if sound {
		right = r.cameraSoundBox("Unmute").Min.X - r.s(10)
	}
	pad := r.s(14)
	w := r.width(r.tiny, label) + 2*pad
	h := r.tiny.Metrics().Height.Ceil() + r.s(10)
	bottom := r.h - r.s(6)
	return image.Rect(right-w, bottom-h, right, bottom)
}
```

`display.go` scene fill, after line 1706:

```go
	s.cameraTalking = home.Get().Talking()
	s.cameraTalk = s.cameraTalking || home.Get().TalkOffered()
	s.cameraTalkNote = home.Get().TalkNote()
```

`display.go` tap routing: in the `if g.Kind == touch.Tap {` block, check Talk first:

```go
			switch p := image.Pt(g.X, g.Y); {
			case d.r != nil && d.r.cameraTalkTapped(p):
				go home.Get().ToggleTalk() // waits on go2rtc; the view stays either way
			case d.r != nil && d.r.cameraSoundTapped(p):
				home.Get().ToggleCameraSound()
			default:
				home.Get().HideCamera()
			}
```

Keep the existing comments on the sound and hide branches.

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/feature/display/ -run 'Camera|Talk' -v && go test ./... && go test -tags spot ./... && go test -tags dot ./...`
Expected: PASS. `TestTheCameraSoundControlIsWhereItIsDrawn` still passes, which shows the sound block's restructure kept its behaviour.

- [ ] **Step 5: Commit**

```bash
git add echod/internal/feature/display
git commit -m "Camera page: a Talk control beside Mute on the Show"
```

---

### Task 6: The Spot's Talk bar

**Files:**
- Modify: `echod/internal/feature/display/render_spot.go` (roundScene fields near `cameraSound bool` at about line 117; `cameraTalkAt` field beside `cameraSoundAt`; clear it where `clearCameraSoundTap` is called, at about line 249)
- Modify: `echod/internal/feature/display/camera_spot.go`
- Modify: `echod/internal/feature/display/display_spot.go` (scene fill at about line 1173; tap at about line 579)
- Test: `echod/internal/feature/display/render_spot_test.go`

**Interfaces:**
- Consumes: Task 4's `home` methods.
- Produces: `func cameraTalkBox(w int, sound bool) image.Rectangle`; `func (r *roundRenderer) cameraTalkTapped(x, y int) bool`.

- [ ] **Step 1: Write the failing test** (append to `render_spot_test.go`)

```go
// On the round face Talk is a second bar, above the sound's, since a circle has no corner for it.
func TestTheTalkControlIsWhereItIsDrawnOnTheSpot(t *testing.T) {
	at := time.Date(2026, 9, 30, 14, 7, 0, 0, time.Local)
	r := newRoundRenderer(image.NewRGBA(image.Rect(0, 0, side, side)))
	cam := roundScene{now: at, phase: "idle", showCamera: true,
		camera: home.CameraView{Entity: "camera.deck", Name: "Deck"}}
	for _, sound := range []bool{false, true} {
		box := cameraTalkBox(r.width(r.label, "End talk")+24, sound)
		centre := box.Min.Add(image.Pt(box.Dx()/2, box.Dy()/2))
		cam.cameraSound, cam.cameraTalk = sound, false
		r.draw(cam)
		if r.cameraTalkTapped(centre.X, centre.Y) {
			t.Fatalf("sound=%v: a tap found Talk where none is drawn", sound)
		}
		cam.cameraTalk = true
		r.draw(cam)
		if !r.cameraTalkTapped(centre.X, centre.Y) {
			t.Fatalf("sound=%v: a tap on Talk at %v was missed", sound, centre)
		}
		if sound {
			if r.cameraSoundTapped(centre.X, centre.Y) {
				t.Fatal("a tap on Talk was taken for the sound's bar")
			}
			snd := cameraSoundBox(r.width(r.label, "Unmute") + 24)
			if box.Inset(-10).Overlaps(snd.Inset(-10)) {
				t.Errorf("the two bars' tap targets overlap: talk %v, sound %v", box, snd)
			}
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test -tags spot ./internal/feature/display/ -run TestTheTalkControlIsWhereItIsDrawnOnTheSpot`
Expected: FAIL to build: `cameraTalkBox undefined`.

- [ ] **Step 3: Implement**

`render_spot.go` roundScene fields, after `cameraSoundLive bool`:

```go
	// cameraTalk, cameraTalking and cameraTalkNote are the Show's (render.go): Talk drawn, talking, and a
	// refusal to say.
	cameraTalk     bool
	cameraTalking  bool
	cameraTalkNote string
```

and a `cameraTalkAt image.Rectangle` beside `cameraSoundAt` (under `zmu`).

`camera_spot.go`, after the sound bar is drawn in the camera face:

```go
	// Talk, a second bar above the sound's: a tap sends the room to the camera's speaker, another stops.
	if s.cameraTalk {
		label := "Talk"
		if s.cameraTalking {
			label = "End talk"
		}
		b := cameraTalkBox(r.width(r.label, label)+24, s.cameraSound)
		shade := color.RGBA{0, 0, 0, 150}
		if s.cameraTalking {
			shade = color.RGBA{120, 30, 20, 200}
		}
		r.line(float64(b.Min.X), float64(b.Min.Y+b.Dy()/2), float64(b.Max.X), float64(b.Min.Y+b.Dy()/2), float64(b.Dy()), shade)
		r.centered(r.label, label, b.Min.Y+b.Dy()/2+8, colText)
		r.setCameraTalkAt(b)
	}
	if s.cameraTalkNote != "" {
		b := cameraTalkBox(r.width(r.label, s.cameraTalkNote)+24, s.cameraSound)
		r.centered(r.label, s.cameraTalkNote, b.Min.Y-12, colText)
	}
```

and beside `cameraSoundBox`:

```go
// cameraTalkBox is where the round camera page's Talk bar is drawn: where the sound's bar is when there is
// none, and above it, clear of its grown tap target, when there is.
func cameraTalkBox(w int, sound bool) image.Rectangle {
	const barH, barMin, gap = 34, 96, 24
	bottom := side - 44
	if sound {
		bottom -= barH + gap
	}
	if w < barMin {
		w = barMin
	}
	return image.Rect(center-w/2, bottom-barH, center+w/2, bottom)
}

func (r *roundRenderer) setCameraTalkAt(b image.Rectangle) {
	r.zmu.Lock()
	r.cameraTalkAt = b
	r.zmu.Unlock()
}

// cameraTalkTapped is whether a tap at x, y is on Talk as last drawn, grown as the sound's bar is.
func (r *roundRenderer) cameraTalkTapped(x, y int) bool {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return !r.cameraTalkAt.Empty() && image.Pt(x, y).In(r.cameraTalkAt.Inset(-10))
}
```

Clear it every frame. Make `clearCameraSoundTap` also call `r.setCameraTalkAt(image.Rectangle{})`, and rename its comment to cover both.

`display_spot.go` scene fill, after line 1173: the same three lines as Task 5's `display.go`. Tap, first in `case touch.Tap:`:

```go
			if d.r != nil && d.r.cameraTalkTapped(g.X, g.Y) {
				go home.Get().ToggleTalk()
				return
			}
```

- [ ] **Step 4: Run the tests**

Run: `go test -tags spot ./internal/feature/display/ -v -run 'Camera|Talk' && go test -tags spot ./... && go test ./... && go test -tags dot ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add echod/internal/feature/display
git commit -m "Spot: a Talk bar on the round camera page"
```

---

### Task 7: The switch, the setup page, the wake word

**Files:**
- Modify: `echod/internal/feature/security/security.go` (switch `talk_back`)
- Modify: `echod/internal/feature/display/sheet.go` (`securityRows`, toggle `case`), `sheet_spot.go` (short label)
- Test: `echod/internal/feature/display/sheet_test.go`
- Create: `echod/internal/feature/home/talk_setup.go`, and a test in `talk_test.go`
- Create: `echod/internal/feature/setup/talk.go`
- Modify: `echod/internal/feature/setup/page.go` (save dispatch at about line 228, the Connections tab at about line 329, `privacySection` at about line 381)
- Modify: `echod/internal/feature/diag/bundle.go` (`settingsSummary`)
- Modify: `echod/internal/feature/detect/detect.go` (`OnDetect`)

**Interfaces:**
- Consumes: Tasks 2–4.
- Produces: `security.Get().SetTalk(on bool)`; `security.State.Talk bool`; `home.SetGo2rtc(addr, user, pass string) (int, error)`; `home.SetTalkStream(entity, stream string) error`.

- [ ] **Step 1: Write the failing tests**

In `sheet_test.go`:

```go
func TestTheTalkSwitchIsUnderPrivacy(t *testing.T) {
	rows, _ := categoryRows(sheetView{st: settings{cat: catSecurity}})
	for _, r := range rows {
		if r.id == "talkback" {
			return
		}
	}
	t.Fatal("Talk through cameras is not on the Privacy & Security card")
}
```

In `feature/home/talk_test.go`:

```go
func TestSetGo2rtcChecksBeforeSaving(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, _ := r.BasicAuth(); u != "admin" || p != "s3cret" {
			http.Error(w, "", http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"a":{},"b":{}}`))
	}))
	defer srv.Close()

	if n, err := SetGo2rtc(srv.URL, "admin", "s3cret"); err != nil || n != 2 {
		t.Fatalf("SetGo2rtc = %d, %v", n, err)
	}
	if n, err := SetGo2rtc(srv.URL, "admin", ""); err != nil || n != 2 {
		t.Fatalf("a password left empty on the form was not the one kept: %d, %v", n, err)
	}
	if _, err := SetGo2rtc(srv.URL, "admin", "wrong"); err == nil {
		t.Fatal("a go2rtc that turned the password down was saved")
	}
	if got := config.Get().Home.Go2rtc.Pass; got != "s3cret" {
		t.Fatalf("a failed save changed what was kept: %q", got)
	}
	if _, err := SetGo2rtc("", "", ""); err != nil || config.Get().Home.Go2rtc.Base != "" {
		t.Fatalf("an empty address did not take go2rtc away: %v", err)
	}
	if err := SetTalkStream("camera.front", "front door"); err == nil {
		t.Fatal("a stream name with a space was taken; go2rtc would refuse it")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/feature/display/ -run TestTheTalkSwitch; go test ./internal/feature/home/ -run TestSetGo2rtc`
Expected: FAIL: row not found; `undefined: SetGo2rtc`.

- [ ] **Step 3: Implement the switch**

`security.go`: add a `talk *esphome.Switch` field. In `build()`:

```go
	f.talk = sw("talk_back", "Talk through cameras", "mdi:account-voice", f.SetTalk)
```

In `Entities()`, inside `if webPages {`: `out = append(out, f.camera, f.screen, f.talk)`. In `Restore`: `f.talk.Set(c.Security.Talk)`. Next to `SetScreen`:

```go
func (f *Feature) SetTalk(on bool) { f.set(f.talk, on, config.Set().Security().Talk) }
```

Add `Talk bool` to `State` and fill it in `State()` from `c.Talk`.

`sheet.go` `securityRows`, after the `screenweb` row:

```go
		settingRow{id: "talkback", label: "Talk through cameras", sub: "Microphones to a camera's speaker, while Talk is on", kind: ctlToggle, on: sec.Talk},
```

and in the toggle switch after `case "screenweb":`:

```go
	case "talkback":
		security.Get().SetTalk(!config.Get().Security.Talk)
```

`sheet_spot.go`, next to `case row.id == "camweb":`:

```go
		case row.id == "talkback":
			row.label = "Talk to cameras"
```

`diag/bundle.go`: change the security line to

```go
	add("security: ssh=%t camera_web=%t screen_web=%t talk_back=%t go2rtc=%t",
		c.Security.SSH, c.Security.Camera, c.Security.Screen, c.Security.Talk, c.Home.Go2rtc.Base != "")
```

(whether go2rtc is set, never its address or account).

`setup/page.go` `privacySection`: add `· Talk through cameras: %s` to the "What is switched on" line with `onOff(s.Talk)`.

- [ ] **Step 4: Implement the setup page**

`feature/home/talk_setup.go`:

```go
package home

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/go2rtc"
)

// SetGo2rtc sets up the go2rtc that cameras are talked to through. It is asked for its streams first, so
// a wrong address or password is found out on the form and not at the door. An empty address takes it
// away; an empty password keeps the one saved.
func SetGo2rtc(addr, user, pass string) (int, error) {
	defer Get().forgetTalkProbes()
	if strings.TrimSpace(addr) == "" {
		return 0, config.Set().Home().Go2rtc(config.Go2rtc{})
	}
	base, err := go2rtc.Normalize(addr)
	if err != nil {
		return 0, err
	}
	if pass == "" {
		pass = config.Get().Home.Go2rtc.Pass
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	streams, err := (&go2rtc.Client{Base: base, User: user, Pass: pass}).Streams(ctx)
	if err != nil {
		return 0, err
	}
	return len(streams), config.Set().Home().Go2rtc(config.Go2rtc{Base: base, User: user, Pass: pass})
}

// SetTalkStream names a camera's go2rtc stream, or goes back to matching by name when stream is empty.
func SetTalkStream(entity, stream string) error {
	stream = strings.TrimSpace(stream)
	if strings.ContainsAny(stream, " \t\r\n#?&/") {
		return errors.New("a go2rtc stream name is one word, as it is in go2rtc's config")
	}
	defer Get().forgetTalkProbes()
	return config.Set().Home().TalkStream(entity, stream)
}
```

`feature/setup/talk.go`, in the shape of the existing camera recorder form (`reolinkSection` in `setup/cameras.go`):

```go
package setup

import (
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
)

// go2rtcSection is the go2rtc the camera page talks to cameras through (feature/home/talk.go), and a
// stream name for each camera the match by name misses. The password is never shown.
func go2rtcSection(w http.ResponseWriter, token string) {
	g := config.Get().Home.Go2rtc
	fmt.Fprint(w, `<fieldset><legend>Talking through cameras (go2rtc)</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "go2rtc", "connections")
	hint := "only if go2rtc's api: sets one"
	if g.Pass != "" {
		hint = "set; leave empty to keep it"
	}
	fmt.Fprintf(w, `<label for="g2addr">go2rtc address</label>
	 <input id="g2addr" name="addr" value="%s" placeholder="192.168.1.5:1984" autocomplete="off">
	 <label for="g2user">User</label>
	 <input id="g2user" name="user" value="%s" autocomplete="off">
	 <label for="g2pass">Password</label>
	 <input id="g2pass" name="pass" type="password" value="" placeholder="%s" autocomplete="off">
	 <p class="note">A go2rtc of its own, the go2rtc add-on, or Frigate's with port 1984 open. Home
	  Assistant's built-in go2rtc does not answer on the network. The Talk switch in Privacy &amp; Security
	  turns it on. Clear the address and save to take it away.</p>
	 <p><button type="submit">Save</button></p></form>`,
		html.EscapeString(g.Base), html.EscapeString(g.User), hint)

	if g.Base != "" {
		fmt.Fprint(w, `<form method="post" action="/setup/save">`)
		hidden(w, token, "talkstreams", "connections")
		fmt.Fprint(w, `<p class="note">Each camera is matched to the go2rtc stream of the same name. Name the
		 stream here for one that is called something else.</p>`)
		named := config.Get().Home.TalkStreams
		for _, c := range home.Get().Cameras() {
			if c.Entity == home.LocalCamera {
				continue
			}
			fmt.Fprintf(w, `<label>%s</label><input name="stream:%s" value="%s" placeholder="by name" autocomplete="off">`,
				html.EscapeString(c.Name), html.EscapeString(c.Entity), html.EscapeString(named[c.Entity]))
		}
		fmt.Fprint(w, `<p><button type="submit">Save stream names</button></p></form>`)
	}
	fmt.Fprint(w, `</fieldset>`)
}

func saveGo2rtc(r *http.Request) string {
	addr := strings.TrimSpace(r.PostFormValue("addr"))
	user := strings.TrimSpace(r.PostFormValue("user"))
	if strings.ContainsAny(addr+user+r.PostFormValue("pass"), "\r\n") {
		return "the address, user and password are one line each"
	}
	n, err := home.SetGo2rtc(addr, user, r.PostFormValue("pass"))
	if err != nil {
		return "could not reach go2rtc: " + err.Error()
	}
	slog.Info("setup page: go2rtc set", "streams", n)
	return ""
}

func saveTalkStreams(r *http.Request) string {
	// The save handler has parsed the form already (page.go), under its size limit.
	for k, v := range r.PostForm {
		entity, ok := strings.CutPrefix(k, "stream:")
		if !ok || len(v) == 0 {
			continue
		}
		if err := home.SetTalkStream(entity, v[0]); err != nil {
			return err.Error()
		}
	}
	return ""
}
```

`page.go`: in the save switch after `case "reolink":`

```go
	case "go2rtc":
		problem = saveGo2rtc(r)
	case "talkstreams":
		problem = saveTalkStreams(r)
```

and on the Connections tab after `reolinkSection(w, token)`: `go2rtcSection(w, token)`.

`hidden(w, token, what, tab string)` is in `setup/tabs.go`. The save handler parses the form under `maxBody` before it dispatches (`page.go`, about line 174), so the save functions read `r.PostForm` directly.

- [ ] **Step 5: Ignore the wake word while talking** (`detect/detect.go`, `OnDetect`)

Change `if phone.Get().Busy() {` to

```go
		// Talking to a camera has the microphones the same way: "Alexa" said to somebody at the door is
		// not said to the device.
		if phone.Get().Busy() || home.Get().Talking() {
```

and add the `feature/home` import. Confirm there is no cycle with `go build ./...`. As of `7ef38f6`, `feature/home` does not import `detect`. This has no unit test, because `OnDetect` is wired to the live engine. It is checked by hand in Task 8.

- [ ] **Step 6: Run everything**

Run: `go vet ./... && go test ./... && go test -tags dot ./... && go test -tags spot ./...`
Expected: PASS. On the Dot the switch is not offered (`webPages` is false there) and `securityRows` is not built.

- [ ] **Step 7: Commit**

```bash
git add echod/internal
git commit -m "Talk through cameras: its switch, go2rtc on the setup page, and the wake word left alone meanwhile"
```

---

### Task 8: At a real door, and the docs

**Files:**
- Modify: `docs/two-way-audio-plan.md` (status, latency and CPU numbers)
- Modify: `docs/setup.md` (the Connections form) and `docs/actions.md` (the `talk_back` switch), next to where the camera's own sound is already described.

This task is by hand. There is nothing to write a failing test for.

- [ ] **Step 1: Check the stream with no camera at all.** Build and install to a Show. Set go2rtc to a machine running `go2rtc` v1.9.14 with one stream. Turn on the switch, open that camera, and tap Talk. On the go2rtc machine, `GET /api/streams?src=<cam>` should list a consumer from `http://<device>:8181/talk/…`. `curl http://<device>:8181/talk/<token>.wav` should return 404, because the address serves once.
- [ ] **Step 2: Cameras with a backchannel.** Use any camera whose go2rtc probe (`GET /api/streams?src=<stream>&microphone`) lists `audio, sendonly, PCMA/8000` or `PCMU/8000`. Try one camera through an RTSP/ONVIF stream and, if one is to hand, one through a native go2rtc source (`tapo:`, `isapi:`, `dvrip:`…), so that both go2rtc paths are exercised. Also check that a camera whose probe shows no sendonly audio gets no Talk control. With a person at the camera, talk while the camera's own sound plays on the device. Confirm they hear the room and not the doorbell's own echo. Confirm "Alexa" said mid-talk does nothing.
- [ ] **Step 3: Every way it ends**: a second tap, a tap on the picture, the view replaced by voice, the switch off in Home Assistant, the mute button, two minutes, and go2rtc restarted mid-talk. After each, go2rtc's stream shows no consumer from the device, and the device's log says `talk: ended` with the reason.
- [ ] **Step 4: Latency.** Clap in front of the device, and record the device and the camera's speaker on one phone. Measure the gap in an audio editor, five times, and give the median. Take `top -b -n 5 -d 1` on the device while talking and while not.
- [ ] **Step 5: Write it down.** Put the numbers in `docs/two-way-audio-plan.md` under Limits, with the camera model and go2rtc version. Change **Status** to built. Add the form and the switch to the user docs.
- [ ] **Step 6: Commit**

```bash
git add docs
git commit -m "docs: talking through a camera, and how long it takes to be heard"
```

---

## Self-review notes

- **Spec coverage:** probe and control only where it works (Tasks 4, 5, 6); one-time address (4); G.711 without ffmpeg (1, 4); ending by closing (4); view held open (4); refusal note (4, 5, 6); stream match and override (4, 7); go2rtc on the setup page with a write-only password (3, 7); switch in Privacy, Home Assistant, the setup Privacy tab and diag (7); wake word (7); Dot as a no-op (7 Step 6); latency and CPU (8). Message playback and dialling out are "later" in the spec and have no task here.
- **Deliberately not built:** a `home_go2rtc` action; the `ffmpeg:` route for Opus cameras; the `src=` empty stop call. The spec explains each.
