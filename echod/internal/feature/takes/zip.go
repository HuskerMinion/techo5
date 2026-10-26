package takes

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

// What the takes go out as.
//
// Each take is a WAV of its own, 16 kHz mono 16-bit PCM, which is what training-wake-word.md's step 4
// asks for, byte for byte the format feature/detect writes its clips in. The zip holds them in one
// folder named for the word, the way step 5 reads a folder of takes, and a takes.json that says where
// each came from in the recording and what was left out of it. Nothing in it names the device.
//
// The WAVs are stored rather than deflated: speech barely compresses, and a Dot has little CPU to
// spare. They are made one take at a time as the zip is written, straight to the browser.

// ErrNone is a download asked for with no takes to give, or by a browser they are not for.
var ErrNone = errors.New("no takes are kept for this browser")

// errGone is a download stopped part way because the takes were thrown away under it: the switch went
// off, the page closed, or the browser threw them away in another tab.
var errGone = errors.New("the takes were thrown away")

// kept is what the zip is made from, taken out from under mu, and the asking it was kept for.
type kept struct {
	id   uint64
	word string
	kind Kind
	at   time.Time
	mic  config.Microphone
	pcm  []int16
	cuts []span
	left []leftOut
}

// keptFor is the takes for the browser session, if it has any.
func (f *Feature) keptFor(session string) (kept, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.phase != Kept || session == "" || session != f.session || len(f.cuts) == 0 {
		return kept{}, false
	}
	// The slices are never written to once kept: a Discard only lets go of them here.
	return kept{id: f.id, word: f.word, kind: f.kind, at: f.started, mic: f.mic, pcm: f.pcm, cuts: f.cuts, left: f.left}, true
}

// still is whether the takes kept for the asking id are still kept.
func (f *Feature) still(id uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.phase == Kept && f.id == id
}

// Zip is the session's takes as a zip: what the browser saves it as, and what writes it. One look at
// the takes for both, so a name is never given for a zip with nothing in it. The writing stops between
// takes once they are thrown away.
func (f *Feature) Zip(session string) (name string, write func(io.Writer) error, ok bool) {
	k, ok := f.keptFor(session)
	if !ok {
		return "", nil, false
	}
	name = fmt.Sprintf("techo5-takes-%s-%s.zip", Slug(k.word), k.at.UTC().Format("20060102-150405"))
	return name, func(w io.Writer) error { return f.writeZip(w, k) }, true
}

// heardAs is takes.json's word for what the takes are.
func heardAs(m config.Microphone) string {
	if m.Leveling {
		return "the wake word's own audio, mixed and leveled, in a room with nothing playing"
	}
	return "the wake word's own audio, mixed (the leveler was off), in a room with nothing playing"
}

func (f *Feature) writeZip(w io.Writer, k kept) error {
	zw := zip.NewWriter(w)
	dir := folder(k.word, k.kind)
	file := takesFile{
		Version: 1, Word: k.word, Kind: k.kind.String(), RecordedAt: k.at.UTC(),
		Rate: mic.Rate, Channels: 1, Bits: 16,
		HeardAs: heardAs(k.mic),
		Microphone: takesMic{
			Mixing: string(k.mic.Mixing), Leveling: k.mic.Leveling, Denoise: k.mic.Denoise, Gain: k.mic.Gain,
		},
	}
	for i, c := range k.cuts {
		if !f.still(k.id) {
			return errGone
		}
		name := fmt.Sprintf("%s/%s-%02d.wav", dir, dir, i+1)
		pcm := k.pcm[c.from*mic.FrameSamples : c.to*mic.FrameSamples]
		zf, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: k.at})
		if err != nil {
			return err
		}
		if _, err := zf.Write(wave(pcm)); err != nil {
			return err
		}
		file.Takes = append(file.Takes, takeInfo{File: name, StartMs: ms(c.from), LengthMs: ms(c.to - c.from), PeakDBFS: peakDBFS(pcm)})
	}
	for _, l := range k.left {
		file.LeftOut = append(file.LeftOut, leftInfo{StartMs: ms(l.from), LengthMs: ms(l.to - l.from), Why: l.why})
	}
	zf, err := zw.CreateHeader(&zip.FileHeader{Name: "takes.json", Method: zip.Deflate, Modified: k.at})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(zf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(file); err != nil {
		return err
	}
	return zw.Close()
}

// Take is take n, counting from 1, as a WAV, for the setup page to play.
func (f *Feature) Take(n int, session string) ([]byte, time.Time, bool) {
	k, ok := f.keptFor(session)
	if !ok || n < 1 || n > len(k.cuts) {
		return nil, time.Time{}, false
	}
	c := k.cuts[n-1]
	return wave(k.pcm[c.from*mic.FrameSamples : c.to*mic.FrameSamples]), k.at, true
}

// takesFile is takes.json.
type takesFile struct {
	Version    int        `json:"version"`
	Word       string     `json:"word"`
	Kind       string     `json:"kind"`
	RecordedAt time.Time  `json:"recorded_at"`
	Rate       int        `json:"rate"`
	Channels   int        `json:"channels"`
	Bits       int        `json:"bits"`
	HeardAs    string     `json:"heard_as"`
	Microphone takesMic   `json:"microphone"`
	Takes      []takeInfo `json:"takes"`
	LeftOut    []leftInfo `json:"left_out,omitempty"`
}

// takesMic is what of the microphone's settings shapes the sound of a take.
type takesMic struct {
	Mixing   string `json:"mixing"`
	Leveling bool   `json:"leveling"`
	Denoise  bool   `json:"denoise"`
	Gain     int    `json:"gain"`
}

type takeInfo struct {
	File     string  `json:"file"`
	StartMs  int     `json:"start_ms"`
	LengthMs int     `json:"length_ms"`
	PeakDBFS float64 `json:"peak_dbfs"`
}

type leftInfo struct {
	StartMs  int    `json:"start_ms"`
	LengthMs int    `json:"length_ms"`
	Why      string `json:"why"`
}

func ms(n int) int { return int(frames(n) / time.Millisecond) }

// peakDBFS is the loudest sample of a take, in dB under full scale.
func peakDBFS(pcm []int16) float64 {
	peak := 0
	for _, s := range pcm {
		peak = max(peak, int(s), -int(s))
	}
	if peak == 0 {
		return -96
	}
	return math.Round(20*math.Log10(float64(peak)/32768)*10) / 10
}

// zipSize is about how big the zip of these takes is, for the page to say before it is downloaded.
func zipSize(word string, kind Kind, cuts []span) int {
	dir := folder(word, kind)
	size := 2048 // takes.json and the zip's own directory, near enough
	for range cuts {
		size += 30 + 46 + 2*len(dir+"/"+dir+"-00.wav") + 16 + 44
	}
	for _, c := range cuts {
		size += 2 * (c.to - c.from) * mic.FrameSamples
	}
	return size
}

// Slug is a word made fit to name files after: lower case, letters, digits and underscores only, at
// most 32 of them, and "word" when nothing is left. The file names in the zip are built only from it
// and numbers, so nothing typed can become a path.
func Slug(word string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(word) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			under = false
		case !under && b.Len() > 0:
			b.WriteByte('_')
			under = true
		}
		if b.Len() >= 32 {
			break
		}
	}
	s := strings.Trim(b.String(), "_")
	if s == "" {
		return "word"
	}
	return s
}

// folder is the zip's folder for the takes: the word's, or not_ and the word's for a negative set.
func folder(word string, kind Kind) string {
	if kind == NotWord {
		return "not_" + Slug(word)
	}
	return Slug(word)
}

// wave is pcm as a 16 kHz mono 16-bit WAV.
func wave(pcm []int16) []byte {
	var b bytes.Buffer
	b.Grow(44 + 2*len(pcm))
	data := uint32(2 * len(pcm))
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, 36+data)
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(mic.Rate), uint32(2 * mic.Rate), uint16(2), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, data)
	binary.Write(&b, binary.LittleEndian, pcm)
	return b.Bytes()
}
