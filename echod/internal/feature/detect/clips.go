package detect

// Wake word clips, for training a wake word model on what this room really says and hears (#140).
// Off unless Keep wake clips is turned on, per device.
//
// The last few seconds the detector heard are kept in memory while it is on, and three moments save
// them to the device as 16 kHz mono WAVE files under layout.WakeClipDir:
//
//   - mistake/: a wake nobody meant (voice.Mistaken): nothing said after it. What the model has to
//     learn to leave alone.
//   - noise/: a wake followed by only what speech-to-text writes for noise ("Thank you."). Mostly the
//     same, but somebody may have said the word and then thanked it: each wants listening to.
//   - nearmiss/: a word that peaked over NearMiss and fell short of the threshold. Somebody saying it
//     and not being heard, or a sound like it: each wants listening to before it is used.
//   - wake/: a wake its turn did not call a mistake within mistakeWithin, the word as this room says
//     it.
//
// Each clip has a sidecar beside it with what the detector made of it: the kind, when (UTC), which
// slot, the model, the score and the cutoff it was judged against. Each kind keeps its newest
// clipsKept. Home Assistant lists them with wake_clips and fetches one with wake_clip_audio, the same
// way it reaches a kept recording (feature/recording).
//
// Nothing is kept while a call, the intercom or a talk through a camera has the room, nor for four
// seconds after one starts or ends or after the stop word, so that none of those is ever in a clip. A
// clip of less than clipShortest is not kept either. Turning the switch off deletes every clip at once,
// and while it is off the actions answer nothing.

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/recording"
	"github.com/HuskerMinion/techo5/echod/internal/feature/takes"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safename"
)

const (
	// clipRate is the detector's: 16 kHz mono.
	clipRate = 16000

	// clipSeconds is how much of what came before a moment a clip holds. A wake is saved a Hold after
	// it fired and a near miss NearMissSettle after its peak, so this is the word and what led up to it.
	clipSeconds = 4

	// clipsKept is how many of each kind are kept, the oldest going first: at 128 kB a clip, four
	// kinds come to about 51 MB.
	clipsKept = 100

	// clipsPerPage is how many clips one wake_clips answer lists. Four hundred would not fit in one
	// message (recording.Page).
	clipsPerPage = 50

	// clipShortest is the least a clip holds, in samples: one second. Less is what is left of the ring
	// just after it was emptied (a mute, a call, the switch turned on), too little of the word to train
	// on.
	clipShortest = clipRate

	// mistakeWithin is how long after a wake its turn has to say it was a mistake. A turn that heard
	// nothing ends when Home Assistant gives up listening, well inside this.
	mistakeWithin = 30 * time.Second
)

// clip is one saved moment.
type clip struct {
	kind         string // mistake, noise, nearmiss, wake
	at           time.Time
	slot         int
	model        string
	peak, cutoff float64
	pcm          []int16
}

// clips is the recorder. frame runs on the audio path with the engine's lock held, so it only copies.
type clips struct {
	// on is Keep wake clips. Off, nothing is heard into the ring and nothing is saved.
	on atomic.Bool

	mu   sync.Mutex
	ring []int16
	next int  // where the next sample goes
	full bool // the ring has gone round once

	// pending are the wakes of the last mistakeWithin, until their turn says they were a mistake or
	// that time passes. More than one: a wake heard again while its turn listens is part of that turn.
	pending []*clip

	// quietUntil is the end of the four seconds after the stop word was heard, or a call or a talk
	// started or ended.
	quietUntil time.Time

	// held is whether a call or a talk through a camera has the room, as last told (roomChanged): the
	// ring takes nothing while it does. Read on the audio path, so it is not behind a lock of theirs.
	held atomic.Bool
	// room is held through roomChanged, so the last to look at the room is the last to set held.
	room sync.Mutex

	dir      string
	keep     int                         // of each kind: clipsKept, fewer in tests
	shortest int                         // the least a clip holds: clipShortest, none in tests
	blocked  func() bool                 // something else has the room: see clipsBlocked
	model    func(slot int) string       // the slot's model id
	cutoff   func(slot int) float64      // the slot's threshold, for a near miss
	now      func() time.Time            // replaced in tests
	after    func(time.Duration, func()) // time.AfterFunc; replaced in tests
	save     func(clip)                  // writes off the caller's goroutine; replaced in tests

	// counts is how many clips of each kind are on the device, for the Wake clips sensors: counted from
	// the folders once at the start, then kept as clips are written, pruned and purged. Its own lock,
	// since writes run off the audio path and never take mu.
	countMu sync.Mutex
	// disk is held while clips are written and pruned, and while they are purged, so the counts the
	// sensors show are read from the disk in the order it changed.
	disk    sync.Mutex
	counts  map[string]int
	publish func(total int, kinds string) // the sensors; nil until wired
}

// clipKinds are the folders, in the order the sensor lists them.
var clipKinds = []string{"mistake", "noise", "nearmiss", "wake"}

func newClips(dir string) *clips {
	c := &clips{ring: make([]int16, clipRate*clipSeconds), dir: dir, keep: clipsKept, shortest: clipShortest,
		blocked: clipsBlocked, model: func(int) string { return "" }, cutoff: func(int) float64 { return 0 },
		now: time.Now, after: func(d time.Duration, f func()) { time.AfterFunc(d, f) },
		counts: map[string]int{}}
	c.save = func(cl clip) { safe.Go("wake clip", func() { c.write(cl) }) }
	return c
}

// startClips wires a recorder to the engine and to the turns, and publish to how many it keeps. It
// starts off: Restore turns it on if it was left on.
func startClips(e *Engine, publish func(total int, kinds string)) *clips {
	c := newClips(layout.WakeClipDir)
	c.publish = publish
	c.model = func(slot int) string {
		m, _ := e.Loaded(slot)
		return m.ID
	}
	c.cutoff = func(slot int) float64 {
		if e.Threshold == nil {
			return 0
		}
		return e.Threshold(slot)
	}
	c.recount()
	e.OnFrame = c.frame
	prevScored, prevNear := e.OnScored, e.OnNearMiss
	e.OnScored = func(slot int, peak, cutoff float64) {
		if prevScored != nil {
			prevScored(slot, peak, cutoff)
		}
		c.scored(slot, peak, cutoff)
	}
	e.OnNearMiss = func(slot int, peak float64) {
		if prevNear != nil {
			prevNear(slot, peak)
		}
		c.nearMiss(slot, peak)
	}
	voice.Mistaken.Listen(c.mistaken)
	mute.Get().Changed.Listen(c.muted)
	phone.Get().Changed.Listen(func(phone.State) { c.roomChanged() })
	talkback.Get().Changed.Listen(func(struct{}) { c.roomChanged() })
	takes.Get().Changed.Listen(func(struct{}) { c.roomChanged() })
	c.roomChanged()
	return c
}

// clipsBlocked is whether a call, the intercom, a talk through a camera or takes being recorded has the
// room. The detector goes on scoring through all of them, and none of them is anybody talking to the
// device: takes are kept by the recorder, cut and padded, and a clip of each would be thirty more of the
// same word sorted as wakes.
func clipsBlocked() bool {
	return phone.Get().Busy() || talkback.Get().Busy() || takes.Get().Holding()
}

// roomChanged is the phone, the talkback or the recorder saying something changed. As a call, a talk or
// a recording starts and as it ends, the ring is emptied and nothing is kept for the next four seconds;
// held keeps the ring from taking anything in between. So nothing heard during one is ever in a clip.
//
// Asked before mu, as allowed does: the phone and the talkback are never waited on under mu.
func (c *clips) roomChanged() {
	c.room.Lock()
	defer c.room.Unlock()
	busy := c.blocked()
	if busy == c.held.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.held.Store(busy)
	c.reset()
	c.quietUntil = c.now().Add(clipSeconds * time.Second)
}

// enable turns the recorder on or off. Off lets go of every clip, kept and pending.
func (c *clips) enable(on bool) {
	if on {
		c.mu.Lock()
		c.reset()
		c.mu.Unlock()
		c.on.Store(true)
		return
	}
	c.on.Store(false)
	c.purge()
}

// purge lets go of every clip, kept and pending.
func (c *clips) purge() {
	c.mu.Lock()
	c.pending = nil
	c.reset()
	c.mu.Unlock()
	c.disk.Lock()
	defer c.disk.Unlock()
	if err := os.RemoveAll(c.dir); err != nil {
		slog.Warn("wake clips: not removed", "err", err)
	}
	// What is left, which is nothing unless the removal failed.
	c.recountLocked()
}

// recount counts what is on the device, once at the start: what an earlier run kept.
func (c *clips) recount() {
	c.disk.Lock()
	defer c.disk.Unlock()
	c.recountLocked()
}

// recountLocked is recount with disk held.
func (c *clips) recountLocked() {
	c.countMu.Lock()
	for _, k := range clipKinds {
		files, _ := filepath.Glob(filepath.Join(c.dir, k, "*.wav"))
		c.counts[k] = len(files)
	}
	c.countMu.Unlock()
	c.published()
}

// counted is how many of a kind there are now.
func (c *clips) counted(kind string, n int) {
	c.countMu.Lock()
	c.counts[kind] = n
	c.countMu.Unlock()
	c.published()
}

// published tells the sensors: the total, and each kind ("mistake 0 · noise 1 · near miss 3 · wake 7").
func (c *clips) published() {
	if c.publish == nil {
		return
	}
	c.countMu.Lock()
	total, parts := 0, make([]string, 0, len(clipKinds))
	for _, k := range clipKinds {
		n := c.counts[k]
		total += n
		name := k
		if k == "nearmiss" {
			name = "near miss"
		}
		parts = append(parts, fmt.Sprintf("%s %d", name, n))
	}
	c.countMu.Unlock()
	c.publish(total, strings.Join(parts, " · "))
}

// allowed is whether a moment heard from slot may be kept, and takes mu when it is: the caller
// unlocks. A word that is not one of Home Assistant's wake words (the stop word) keeps the next four
// seconds out of every clip.
//
// Whether something else has the room is asked before mu is taken: the phone and the talkback have
// locks of their own, and neither is ever waited on under mu, which the audio path takes.
func (c *clips) allowed(slot int) bool {
	ok := slot < wakeword.Slots && c.on.Load() && !c.blocked() && !c.held.Load()
	c.mu.Lock()
	now := c.now()
	if slot >= wakeword.Slots {
		c.quietUntil = now.Add(clipSeconds * time.Second)
	}
	if ok && now.After(c.quietUntil) {
		return true
	}
	c.mu.Unlock()
	return false
}

// frame keeps one frame of what the detector heard: none while a call or a talk has the room.
func (c *clips) frame(f []int16) {
	if !c.on.Load() || c.held.Load() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(f) > 0 {
		n := copy(c.ring[c.next:], f)
		f = f[n:]
		c.next += n
		if c.next == len(c.ring) {
			c.next, c.full = 0, true
		}
	}
}

// reset empties the ring, so that what was heard before a gap (the microphones cut) is never spliced
// onto what comes after it. Held with mu.
func (c *clips) reset() { c.next, c.full = 0, false }

// muted empties the ring as the microphones are cut: nothing is heard until they are back.
func (c *clips) muted(on bool) {
	if on {
		c.mu.Lock()
		c.reset()
		c.mu.Unlock()
	}
}

// short is whether the ring holds too little to keep (clipShortest). Held with mu.
func (c *clips) short() bool { return !c.full && c.next < c.shortest }

// snapshot is the ring in order, oldest first. Held with mu.
func (c *clips) snapshot() []int16 {
	if !c.full {
		return slices.Clone(c.ring[:c.next])
	}
	return append(slices.Clone(c.ring[c.next:]), c.ring[:c.next]...)
}

// scored is a wake, a Hold after it fired. Only Home Assistant's wake words: the stop word is not what
// this is for.
func (c *clips) scored(slot int, peak, cutoff float64) {
	// Asked before mu: the engine's lock is taken before mu on the audio path, never after it.
	model := c.model(slot)
	if !c.allowed(slot) {
		return
	}
	defer c.mu.Unlock()
	if c.short() {
		return
	}
	cl := &clip{kind: "wake", at: c.now(), slot: slot, model: model, peak: peak, cutoff: cutoff, pcm: c.snapshot()}
	c.pending = append(c.pending, cl)
	c.after(mistakeWithin, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.take(func(p *clip) bool { return p == cl }, "")
	})
}

// take saves and lets go of the pending wakes which, kept as kind, or as they are when kind is
// empty. Held with mu.
func (c *clips) take(which func(*clip) bool, kind string) {
	c.pending = slices.DeleteFunc(c.pending, func(p *clip) bool {
		if !which(p) {
			return false
		}
		if kind != "" {
			p.kind = kind
		}
		c.save(*p)
		return true
	})
}

// settle saves every pending wake as one that was meant. Held with mu.
func (c *clips) settle() { c.take(func(*clip) bool { return true }, "") }

// mistaken is a turn saying its wake was a mistake: every pending wake of its slot since the turn
// opened is saved as one. A wake is scored a Hold after it fires and the turn opens as it fires, so
// the turn's own wakes come after it opened; an earlier one was somebody else's, and stays pending.
func (c *clips) mistaken(m voice.Mistake) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kind := "mistake"
	if m.Heard != "" {
		kind = "noise"
	}
	since := m.Opened.Add(-time.Second)
	c.take(func(p *clip) bool { return p.slot == m.Slot && !p.at.Before(since) }, kind)
}

// nearMiss is a word that peaked and fell short, NearMissSettle after its peak. The floor is the
// engine's NearMiss: anything under it is not reported at all.
func (c *clips) nearMiss(slot int, peak float64) {
	model, cutoff := c.model(slot), c.cutoff(slot)
	if !c.allowed(slot) {
		return
	}
	defer c.mu.Unlock()
	if c.short() {
		return
	}
	c.save(clip{kind: "nearmiss", at: c.now(), slot: slot, model: model, peak: peak, cutoff: cutoff, pcm: c.snapshot()})
}

// clipMeta is what is known about a clip: its sidecar, and one entry of what wake_clips answers.
type clipMeta struct {
	// ID is how wake_clip_audio asks for it, "kind/name". Not stored: it is where the file is.
	ID     string  `json:"id,omitempty"`
	Kind   string  `json:"kind"`
	At     string  `json:"at"`   // UTC, RFC 3339
	Slot   int     `json:"slot"` // 1 or 2, the assistant
	Model  string  `json:"model"`
	Score  float64 `json:"score"`
	Cutoff float64 `json:"cutoff"`
}

// write puts a clip on the device, with its sidecar, and lets the oldest of its kind go. The name
// says when (UTC, to the millisecond), which slot, how high it scored and what it had to reach:
// 20261012-213301-123-s1-p0.79-c0.90.wav.
func (c *clips) write(cl clip) {
	dir := filepath.Join(c.dir, cl.kind)
	// UTC, so that name order stays time order across a change of the clocks; the milliseconds at a
	// fixed width, so that two in one second are two files, still in order.
	at := cl.at.UTC()
	name := fmt.Sprintf("%s-%03d-s%d-p%.2f", at.Format("20060102-150405"), at.Nanosecond()/1e6, cl.slot+1, cl.peak)
	if cl.cutoff > 0 {
		name += fmt.Sprintf("-c%.2f", cl.cutoff)
	}
	path := filepath.Join(dir, name+".wav")
	meta, _ := json.Marshal(clipMeta{Kind: cl.kind, At: cl.at.UTC().Format(time.RFC3339), Slot: cl.slot + 1,
		Model: cl.model, Score: round3(cl.peak), Cutoff: round3(cl.cutoff)})
	// disk from before the switch is looked at: turned off before this, the folders the purge took away
	// are not made again; turned off after, the purge waits for disk and takes this clip with the rest.
	c.disk.Lock()
	defer c.disk.Unlock()
	if !c.on.Load() {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("wake clip: no folder", "err", err)
		return
	}
	// The sidecar last: a clip without one is not offered, and is pruned.
	if err := os.WriteFile(path, wave(cl.pcm), 0o600); err != nil {
		os.Remove(path)
		slog.Warn("wake clip: not saved", "err", err)
		return
	}
	if err := os.WriteFile(sidecar(path), meta, 0o600); err != nil {
		os.Remove(path)
		os.Remove(sidecar(path))
		slog.Warn("wake clip: not saved", "err", err)
		return
	}
	slog.Info("wake clip saved", "kind", cl.kind, "slot", cl.slot+1, "peak", cl.peak)
	c.counted(cl.kind, prune(dir, c.keep))
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// sidecar is the metadata file beside a clip.
func sidecar(wav string) string { return strings.TrimSuffix(wav, ".wav") + ".json" }

// prune keeps the newest keep files in dir, and says how many are left. The names start with the
// time, so name order is age.
func prune(dir string, keep int) int {
	files, err := filepath.Glob(filepath.Join(dir, "*.wav"))
	if err != nil {
		return 0
	}
	if len(files) <= keep {
		return len(files)
	}
	slices.Sort(files)
	left := len(files)
	for _, f := range files[:len(files)-keep] {
		os.Remove(sidecar(f))
		if os.Remove(f) == nil {
			left--
		}
	}
	return left
}

// ClipList is one page of what wake_clips answers, newest first.
type ClipList struct {
	Version int        `json:"version"`
	Page    int        `json:"page"`
	Pages   int        `json:"pages"`
	Total   int        `json:"total"`
	Clips   []clipMeta `json:"clips"`
}

// errClipsOff is what the actions answer while Keep wake clips is off.
var errClipsOff = errors.New("wake clips are not kept: Keep wake clips is off")

// list is the clips of kind (every kind when it is empty), newest first, a page at a time. Keyed off
// the sidecars, so a clip still being written is not offered. "near miss", as the sensor writes it,
// is nearmiss.
func (c *clips) list(kind string, page int) (*ClipList, error) {
	if !c.on.Load() {
		return nil, errClipsOff
	}
	kinds := clipKinds
	if kind == "near miss" {
		kind = "nearmiss"
	}
	if kind != "" {
		if !slices.Contains(clipKinds, kind) {
			return nil, fmt.Errorf("%q is not a kind of wake clip: mistake, noise, near miss, wake", kind)
		}
		kinds = []string{kind}
	}
	var all []clipMeta
	for _, k := range kinds {
		files, _ := filepath.Glob(filepath.Join(c.dir, k, "*.json"))
		for _, f := range files {
			blob, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			var m clipMeta
			if json.Unmarshal(blob, &m) != nil {
				continue
			}
			m.ID = k + "/" + strings.TrimSuffix(filepath.Base(f), ".json")
			all = append(all, m)
		}
	}
	// Newest first; the name, which starts with the time, settles a tie.
	slices.SortFunc(all, func(a, b clipMeta) int {
		if a.At != b.At {
			return strings.Compare(b.At, a.At)
		}
		return strings.Compare(b.ID, a.ID)
	})
	pages := max((len(all)+clipsPerPage-1)/clipsPerPage, 1)
	if page < 0 || page >= pages {
		return nil, fmt.Errorf("page %d of %d", page, pages)
	}
	end := min((page+1)*clipsPerPage, len(all))
	out := &ClipList{Version: 1, Page: page, Pages: pages, Total: len(all), Clips: []clipMeta{}}
	if len(all) > 0 {
		out.Clips = all[page*clipsPerPage : end]
	}
	return out, nil
}

// audio is one page of a clip, in the same answer turn_audio gives for a recording.
//
// The id comes in over the wire, so it is whatever the caller cared to send: it names a file and it
// is checked before it does.
func (c *clips) audio(id string, page int) (*recording.Answer, error) {
	if !c.on.Load() {
		return nil, errClipsOff
	}
	kind, name, ok := strings.Cut(id, "/")
	if !ok || !slices.Contains(clipKinds, kind) || !safename.OK(name) {
		return nil, fmt.Errorf("%q is not a wake clip id", id)
	}
	whole, err := os.ReadFile(filepath.Join(c.dir, kind, name+".wav"))
	if err != nil {
		return nil, fmt.Errorf("no wake clip %s", id)
	}
	pages := (len(whole) + recording.Page - 1) / recording.Page
	if page < 0 || page >= pages {
		return nil, fmt.Errorf("page %d of %d", page, pages)
	}
	end := min((page+1)*recording.Page, len(whole))
	return &recording.Answer{
		Version: 1,
		ID:      id,
		Page:    page,
		Pages:   pages,
		MIME:    "audio/wav",
		Data:    base64.StdEncoding.EncodeToString(whole[page*recording.Page : end]),
	}, nil
}

// actions is how Home Assistant reaches the clips: wake_clips lists them with what the detector made
// of each, wake_clip_audio fetches one, paged as turn_audio is.
func (c *clips) actions() []*esphome.Action {
	return []*esphome.Action{
		{
			Name:    "wake_clips",
			Args:    []esphome.Arg{{Name: "kind", Type: esphome.ArgString}, {Name: "page", Type: esphome.ArgInt}},
			Answers: true,
			Run: func(call esphome.Call) (any, error) {
				return c.list(call.String("kind"), call.Int("page"))
			},
		},
		{
			Name:    "wake_clip_audio",
			Args:    []esphome.Arg{{Name: "id", Type: esphome.ArgString}, {Name: "page", Type: esphome.ArgInt}},
			Answers: true,
			Run: func(call esphome.Call) (any, error) {
				return c.audio(call.String("id"), call.Int("page"))
			},
		},
	}
}

// wave is pcm as a 16 kHz mono 16-bit WAVE file.
func wave(pcm []int16) []byte {
	var b bytes.Buffer
	data := uint32(2 * len(pcm))
	b.WriteString("RIFF")
	binary.Write(&b, binary.LittleEndian, 36+data)
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(clipRate), uint32(2 * clipRate), uint16(2), uint16(16)} {
		binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, data)
	binary.Write(&b, binary.LittleEndian, pcm)
	return b.Bytes()
}
