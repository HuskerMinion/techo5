package streaming

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/home"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

func item(typ, code, data string) string {
	return fmt.Sprintf("<item><type>%s</type><code>%s</code><length>%d</length>\n<data encoding=\"base64\">\n%s</data></item>\n",
		hex.EncodeToString([]byte(typ)), hex.EncodeToString([]byte(code)), len(data), base64.StdEncoding.EncodeToString([]byte(data)))
}

// rawItem is an item with its data written as given: base64 as it came, damaged or not.
func rawItem(typ, code, b64 string) string {
	return fmt.Sprintf("<item><type>%s</type><code>%s</code><length>%d</length>\n<data encoding=\"base64\">\n%s</data></item>\n",
		hex.EncodeToString([]byte(typ)), hex.EncodeToString([]byte(code)), len(b64), b64)
}

// AirPlay's metadata names the song field by field, and its cover apart, and the end of a session
// clears both; what the decoder cannot take on the way is passed over, a cover that will not decode
// included.
func TestAirPlayMetadata(t *testing.T) {
	picture := strings.Repeat("\xff\xd8 a picture ", 200) // past the 1024 bytes a field may hold
	badPicture := "<item><type>73736e63</type><code>50494354</code><length>3</length>\n<data encoding=\"base64\">\n!!!</data></item>\n"
	stream := item("ssnc", "mdst", "") + item("core", "minm", "Take It Easy") + `<?xml version="2.0"?>` + "</junk>" +
		item("core", "asar", "Eagles") + item("core", "asal", "Eagles") + item("ssnc", "mden", "") +
		item("ssnc", "PICT", picture) + badPicture + item("ssnc", "pend", "")
	var got [][3]string
	var pictures []string
	followAirPlayMetadata(strings.NewReader(stream), func(title, artist, album string) {
		got = append(got, [3]string{title, artist, album})
	}, func(b []byte) {
		pictures = append(pictures, string(b))
	}, nil)
	if len(got) != 4 || got[2] != [3]string{"Take It Easy", "Eagles", "Eagles"} || got[3] != [3]string{} {
		t.Errorf("told %v", got)
	}
	if len(pictures) != 2 || pictures[0] != picture || pictures[1] != "" {
		t.Errorf("pictured %d times: %q", len(pictures), pictures)
	}
}

// An item past what the decoder may read for one is given up on part way, and the items after it are
// still read.
func TestAirPlayMetadataPassesOverAnItemTooBig(t *testing.T) {
	was := largestItem
	largestItem = 4096
	t.Cleanup(func() { largestItem = was })
	stream := item("ssnc", "PICT", strings.Repeat("p", 8192)) + item("core", "minm", "After") +
		item("ssnc", "PICT", "small") + item("ssnc", "pend", "")
	var titles, pictures []string
	followAirPlayMetadata(strings.NewReader(stream), func(title, artist, album string) {
		titles = append(titles, title)
	}, func(b []byte) {
		pictures = append(pictures, string(b))
	}, nil)
	if !slices.Equal(titles, []string{"After", ""}) || !slices.Equal(pictures, []string{"small", ""}) {
		t.Errorf("titles %q, pictures %q", titles, pictures)
	}
}

// An empty cover, or one with no data at all, is the phone's word for a song without one, and takes
// the last one down; a field that cannot be read leaves the song as it was, and one far too long is
// cut at a whole character.
func TestAirPlayCoverAndFields(t *testing.T) {
	unreadable := "<item><type>636f7265</type><code>6d696e6d</code><length>3</length>\n<data encoding=\"base64\">\n!!!</data></item>\n"
	long := strings.Repeat("€", 700) // 2100 bytes, three to a character
	noData := "<item><type>73736e63</type><code>50494354</code><length>0</length></item>\n"
	stream := item("ssnc", "PICT", "one") + item("core", "minm", "One") + unreadable +
		item("ssnc", "PICT", "") + item("core", "minm", long) + item("ssnc", "PICT", "two") + noData +
		item("ssnc", "pend", "")
	var titles, pictures []string
	followAirPlayMetadata(strings.NewReader(stream), func(title, artist, album string) {
		titles = append(titles, title)
	}, func(b []byte) {
		pictures = append(pictures, string(b))
	}, nil)
	cut := strings.Repeat("€", 341) // 1023 bytes: the 342nd character would end past 1024
	if !slices.Equal(titles, []string{"One", cut, ""}) {
		t.Errorf("titles %d long: %q", len(titles), titles)
	}
	if !slices.Equal(pictures, []string{"one", "", "two", "", ""}) {
		t.Errorf("pictured %q", pictures)
	}
}

// latest does each thing in order, dropping one still waiting when a newer one comes, and starts
// again for what is put after it has gone quiet.
func TestLatestKeepsTheNewest(t *testing.T) {
	var mu sync.Mutex
	var got []string
	started, hold, broke := make(chan struct{}), make(chan struct{}), make(chan struct{})
	l := &latest{do: func(b []byte) {
		switch string(b) {
		case "first":
			close(started)
			<-hold
		case "boom":
			close(broke)
			panic("a cover that breaks the layout")
		}
		mu.Lock()
		got = append(got, string(b))
		mu.Unlock()
	}}
	l.put([]byte("first"))
	<-started
	l.put([]byte("dropped"))
	l.put([]byte("newest"))
	close(hold)
	wait := func(want ...string) {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Second); ; time.Sleep(5 * time.Millisecond) {
			mu.Lock()
			g := slices.Clone(got)
			mu.Unlock()
			if slices.Equal(g, want) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("did %q, want %q", g, want)
			}
		}
	}
	wait("first", "newest")
	l.put(nil)
	wait("first", "newest", "")
	l.put([]byte("boom"))
	<-broke
	l.put([]byte("after"))
	wait("first", "newest", "", "after")
}

// librespot's event lines name a new song and its cover, and a stop clears it; anything else is let
// by, a line far too long included, without losing what comes after it.
func TestSpotifyEvents(t *testing.T) {
	lines := "track_changed\tHotel California\tEagles, Don Henley\tHotel California\thttps://i.scdn.co/image/a\t\n" +
		"half a line\n" +
		"track_changed\t" + strings.Repeat("x", 40<<10) + "\ta\tb\t\t\n" +
		"track_changed\tCaf\xc3\tEagles\tB\t\t\n" + // cut inside a character
		"stopped\t\t\t\t\t\n"
	var got [][4]string
	followSpotifyEvents(strings.NewReader(lines), spotifyEvents{
		track: func(title, artist, album, cover string) {
			got = append(got, [4]string{title, artist, album, cover})
		},
		now: time.Now,
	})
	if len(got) != 3 || got[0] != [4]string{"Hotel California", "Eagles, Don Henley", "Hotel California", "https://i.scdn.co/image/a"} ||
		got[1] != [4]string{"Caf", "Eagles", "B", ""} || got[2] != [4]string{} {
		t.Errorf("told %v", got)
	}
}

// The app's volume is passed on, marked as only where librespot was left for everything in the moments
// after a phone connects, however many it says. A volume that is not one is let by.
func TestSpotifyVolumeEvents(t *testing.T) {
	lines := "volume_changed\t\t\t\t\t32768\n" +
		"session_connected\t\t\t\t\t\n" +
		"volume_changed\t\t\t\t\t65535\n" + // as the phone connected
		"volume_changed\t\t\t\t\t13107\n" + // and again, still connecting
		"session_connected\t\t\t\t\t\n" +
		"volume_changed\t\t\t\t\t0\n" + // long after that connect: the app's
		"volume_changed\t\t\t\t\t70000\n" +
		"volume_changed\t\t\t\t\tloud\n"
	// The clock as each line that asks for it is read: the connect, two volumes a second apart, then
	// the second connect and a volume well after it.
	clock := []int64{1000, 1001, 1002, 2000, 2100}
	now := func() time.Time {
		if len(clock) == 0 {
			t.Fatal("asked the time more often than expected")
		}
		v := clock[0]
		clock = clock[1:]
		return time.Unix(v, 0)
	}
	type vol struct {
		v          int
		connecting bool
	}
	var got []vol
	followSpotifyEvents(strings.NewReader(lines), spotifyEvents{
		track:  func(string, string, string, string) {},
		volume: func(v int, connecting bool) { got = append(got, vol{v, connecting}) },
		now:    now,
	})
	want := []vol{{32768, false}, {65535, true}, {13107, true}, {0, false}}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// The slider moves the device by as many steps as it moves: the first level heard and those heard as a
// phone connects only mark where it is, so a slider at 100% over a quiet device does not send it to full
// at the first nudge.
func TestTheSliderMovesTheDeviceByWhatItMoved(t *testing.T) {
	s := slider{last: -1}
	for i, c := range []struct {
		v          int
		connecting bool
		by         int
	}{
		{65535, true, 0},   // connected at 100%
		{61166, false, -2}, // nudged down: two steps, not to 28
		{61166, false, 0},  // said again
		{65535, false, 2},
		{0, false, -30},
		{32768, true, 0}, // a reconnect at half
		{39321, false, 3},
	} {
		if by := s.moved(c.v, c.connecting, time.Unix(int64(1000+10*i), 0)); by != c.by {
			t.Errorf("%d: moved %d, want %d", i, by, c.by)
		}
	}
	if by := (&slider{last: -1}).moved(30000, false, time.Now()); by != 0 {
		t.Errorf("the first level ever heard moved %d", by)
	}
}

// The app's slider runs the device's volume steps end to end, and the gain undoes librespot's linear
// scaling exactly.
func TestSpotifyVolumeIsTheDevicesVolume(t *testing.T) {
	for v, step := range map[int]int{0: 0, 65535: 30, 32768: 15, 2184: 1, 1000: 0} {
		if got := spotifyStep(v); got != step {
			t.Errorf("app volume %d is step %d, want %d", v, got, step)
		}
	}
	for v, g := range map[int]float64{65535: 1, 0: 1, 32768: 65535.0 / 32768, 6553: 65535.0 / 6553} {
		if got := spotifyGain(v); got != g {
			t.Errorf("app volume %d undone by %v, want %v", v, got, g)
		}
	}
}

// Each of the device's steps is handed to librespot as a level that comes back as the same step.
func TestEveryStepGoesToTheAppAndBack(t *testing.T) {
	for step := range media.VolumeSteps + 1 {
		v := spotifyValue(step)
		if v < 0 || v > spotifyFull || spotifyStep(v) != step {
			t.Errorf("step %d is app volume %d, which is step %d", step, v, spotifyStep(v))
		}
	}
	if spotifyValue(0) != 0 || spotifyValue(media.VolumeSteps) != spotifyFull {
		t.Errorf("the ends are %d and %d", spotifyValue(0), spotifyValue(media.VolumeSteps))
	}
}

// fakeSpotify is a device and a librespot around a spotifySync: what librespot was handed, and the
// device's level, set the way media.Player sets it, which tells the sync as OnLevel does.
type fakeSpotify struct {
	s      *spotifySync
	step   int
	told   []int // levels handed to librespot
	sets   []int // levels the app set the device to
	takes  bool  // whether librespot takes what it is handed
	night  int   // the night volume in quiet hours, 0 for none
	clock  time.Time
	settle func() // the settle to come, nil for none
}

func newFakeSpotify(step int) *fakeSpotify {
	f := &fakeSpotify{step: step, takes: true, clock: time.Unix(1000, 0)}
	s := newSpotifySync()
	s.write = func(v int) bool {
		if f.takes {
			f.told = append(f.told, v)
		}
		return f.takes
	}
	s.device = func() int { return f.step }
	s.set = func(step int) {
		f.sets = append(f.sets, step)
		f.press(step)
	}
	s.most = func(to int) int {
		if f.night > 0 {
			return min(to, max(f.step, f.night))
		}
		return to
	}
	s.now = func() time.Time { return f.clock }
	s.after = func(d time.Duration, fn func()) func() bool {
		if d != settleAfter {
			panic("not the settle")
		}
		f.settle = fn
		return func() bool { f.settle = nil; return true }
	}
	f.s = s
	return f
}

// press changes the device's level as anything but the app does: a button, Home Assistant, night volume.
func (f *fakeSpotify) press(step int) {
	f.step = step
	f.s.levelChanged(step)
}

func (f *fakeSpotify) later(d time.Duration) { f.clock = f.clock.Add(d) }

func values(steps ...int) []int {
	var vs []int
	for _, s := range steps {
		vs = append(vs, spotifyValue(s))
	}
	return vs
}

// A phone that connects is told the device's level, whatever librespot was left at; after that the
// app's slider sets the device to where it is, at once and both ways, with nothing handed back to it.
func TestTheAppsSliderSetsTheDevice(t *testing.T) {
	f := newFakeSpotify(8)
	f.s.heard(65535, false) // before anything: only librespot saying where it was
	f.s.session(true)
	f.s.heard(65535, true)           // as the phone connected
	f.s.heard(spotifyValue(8), true) // the device's own level coming back
	if len(f.sets) != 0 {
		t.Fatalf("connecting set the device to %v", f.sets)
	}
	if want := values(8, 8, 8); !slices.Equal(f.told, want) {
		t.Fatalf("told librespot %v, want %v", f.told, want)
	}
	f.later(5 * time.Second)
	f.told = nil
	f.s.heard(45875, false) // 70%
	f.s.heard(45875, false) // said again
	f.s.heard(0, false)
	f.s.heard(spotifyFull, false)
	if want := []int{21, 0, 30}; !slices.Equal(f.sets, want) {
		t.Errorf("set the device to %v, want %v", f.sets, want)
	}
	if len(f.told) != 0 {
		t.Errorf("handed the app's own levels back: %v", f.told)
	}
}

// Whatever moves the device's level moves the app's slider, while a phone is connected and only then.
func TestTheDeviceMovesTheAppsSlider(t *testing.T) {
	f := newFakeSpotify(8)
	f.press(9) // nobody connected
	f.s.session(true)
	f.press(12)
	f.press(12) // the same level again: the slider is there already
	f.press(3)
	f.s.session(false)
	f.press(20)
	if want := values(9, 12, 3); !slices.Equal(f.told, want) {
		t.Errorf("told librespot %v, want %v", f.told, want)
	}
}

// librespot says each level it is handed back as the app's: those are let by, in whatever order they
// come, rather than pulling the device back to where it was a moment ago. Long after, the same level
// is the app's again.
func TestLibrespotsEchoIsNotTakenBack(t *testing.T) {
	f := newFakeSpotify(10)
	f.s.session(true)
	f.later(5 * time.Second)
	f.press(11)
	f.press(12)
	f.s.heard(spotifyValue(12), false)
	f.s.heard(spotifyValue(11), false)
	if len(f.sets) != 0 || f.step != 12 {
		t.Fatalf("the device went to %v and is at %d", f.sets, f.step)
	}
	f.later(echoWait + time.Second)
	f.s.heard(spotifyValue(11), false)
	if !slices.Equal(f.sets, []int{11}) {
		t.Errorf("the app's level, long after: set %v, want [11]", f.sets)
	}
}

// In quiet hours the app's slider raises the device no further than the night volume, and is put back
// where the device stopped; a lower level is taken as it is.
func TestNightVolumeStopsTheAppsRaise(t *testing.T) {
	f := newFakeSpotify(5)
	f.night = 8
	f.s.session(true)
	f.later(5 * time.Second)
	f.told = nil
	f.s.heard(spotifyValue(25), false)
	if !slices.Equal(f.sets, []int{8}) || !slices.Equal(f.told, values(8)) {
		t.Fatalf("set %v and told %v, want [8] and the slider back at 8", f.sets, f.told)
	}
	f.s.heard(spotifyValue(8), false) // the slider put back, coming back
	f.s.heard(spotifyValue(30), false)
	if !slices.Equal(f.sets, []int{8}) || !slices.Equal(f.told, values(8, 8)) {
		t.Fatalf("at the night volume already: set %v and told %v", f.sets, f.told)
	}
	f.s.heard(spotifyValue(3), false)
	if !slices.Equal(f.sets, []int{8, 3}) {
		t.Errorf("down: set %v", f.sets)
	}
}

// A level that librespot did not take is not counted as handed to it: the next change hands it again.
func TestALevelLibrespotDidNotTakeIsHandedAgain(t *testing.T) {
	f := newFakeSpotify(8)
	f.takes = false
	f.s.session(true)
	f.press(12)
	f.takes = true
	f.press(12)
	if !slices.Equal(f.told, values(12)) {
		t.Errorf("told librespot %v, want the 12 once it took it", f.told)
	}
	// And a level the app sent that matches one never taken is the app's.
	f.later(5 * time.Second)
	f.s.heard(spotifyValue(8), false)
	if !slices.Equal(f.sets, []int{8}) {
		t.Errorf("set %v, want [8]", f.sets)
	}
}

// The event lines say when a phone connects and goes.
func TestSpotifySessions(t *testing.T) {
	var got []bool
	followSpotifyEvents(strings.NewReader("session_connected\t\t\t\t\t\nstopped\t\t\t\t\t\nsession_disconnected\t\t\t\t\t\n"), spotifyEvents{
		track:   func(string, string, string, string) {},
		session: func(c bool) { got = append(got, c) },
		now:     time.Now,
	})
	if !slices.Equal(got, []bool{true, false}) {
		t.Errorf("sessions %v", got)
	}
}

// Handing librespot a level never waits: with no pipe, with nobody reading it, or with it full, the
// level is dropped. One read is a line.
func TestHandingLibrespotALevelNeverWaits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "volume")
	if writeSpotifyVolume(path, 100) {
		t.Error("handed a level to a pipe that is not there")
	}
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if writeSpotifyVolume(path, 100) {
			t.Error("handed a level with nobody reading")
		}
		r, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			t.Error(err)
			return
		}
		defer r.Close()
		if !writeSpotifyVolume(path, 32768) {
			t.Error("a level was not taken")
		}
		b := make([]byte, 64)
		if n, _ := r.Read(b); string(b[:n]) != "32768\n" {
			t.Errorf("read %q", b[:n])
		}
		for range 1 << 20 { // far more than a pipe holds
			if !writeSpotifyVolume(path, 65535) {
				return
			}
		}
		t.Error("a full pipe took every level")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("handing a level waited")
	}
}

// Only a librespot that says it takes a volume pipe is given one.
func TestOnlyALibrespotThatTakesVolumesIsGivenThem(t *testing.T) {
	was := librespotPath
	t.Cleanup(func() { librespotPath = was })
	dir := t.TempDir()
	for name, help := range map[string]bool{"  --volume-fifo PATH  Read volumes": true, "  --onevent PROGRAM": false} {
		librespotPath = filepath.Join(dir, fmt.Sprint(help))
		if err := os.WriteFile(librespotPath, []byte("#!/bin/sh\necho '"+name+"'\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := librespotTakesVolume(); got != help {
			t.Errorf("%q: takes volumes %v", name, got)
		}
	}
	librespotPath = filepath.Join(dir, "missing")
	if librespotTakesVolume() {
		t.Error("a missing librespot takes volumes")
	}
}

// A lower volume's larger gain waits until the audio already in the pipe, any read from it but not yet
// played, and a read's worth more have gone through; a higher volume's smaller one applies at once.
func TestTheGainChangesInStepWithTheAudio(t *testing.T) {
	l := newLevel()
	l.fromPipe(1000)
	l.passed(1000)
	l.set(4, 600) // turned down, with 600 bytes at the old level still in the pipe
	if g := l.gain(); g != 1 {
		t.Fatalf("gain %v before the old audio was played", g)
	}
	l.fromPipe(600 + inFlight)
	l.passed(600 + inFlight - 1)
	if g := l.gain(); g != 1 {
		t.Fatalf("gain %v with a byte of the old audio left", g)
	}
	l.passed(1)
	if g := l.gain(); g != 4 {
		t.Fatalf("gain %v once the old audio was played, want 4", g)
	}
	l.set(2, 600) // turned up: at once
	if g := l.gain(); g != 2 {
		t.Fatalf("gain %v after turning up, want 2", g)
	}
	l.set(8, 100)
	l.set(1, 100) // turned back up before the lower one came in: at once, and the lower one is dropped
	l.fromPipe(100 + inFlight)
	l.passed(100 + inFlight)
	if g := l.gain(); g != 1 {
		t.Fatalf("gain %v, want 1", g)
	}

	// Read ahead of playing (the pump's first read of a track), with the pipe itself empty: the gain
	// still waits for what was read.
	l = newLevel()
	l.fromPipe(16384)
	l.set(5, 0)
	l.passed(16384)
	if g := l.gain(); g != 1 {
		t.Fatalf("gain %v over audio read ahead", g)
	}
	l.fromPipe(inFlight)
	l.passed(inFlight)
	if g := l.gain(); g != 5 {
		t.Fatalf("gain %v once it was played, want 5", g)
	}
}

// Every byte read from the pipe is counted as gone through exactly once, however it went: played in
// odd-sized reads, a half sample held back, dropped when the track ended, or drained after it.
func TestEveryByteIsCountedOnce(t *testing.T) {
	was := setAside
	setAside = 50 * time.Millisecond
	t.Cleanup(func() { setAside = was })
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	l := newLevel()
	l.set(2, 0)

	// What the pump read before the track started, odd-sized and short, so most of what the track
	// plays comes from the pipe itself.
	first := make([]byte, 101)
	if _, err := w.Write(first); err != nil {
		t.Fatal(err)
	}
	n, err := r.Read(make([]byte, 101))
	if err != nil {
		t.Fatal(err)
	}
	l.fromPipe(n)
	src := &pipeSource{f: r, pending: first[:n], lv: l, done: make(chan struct{})}

	if _, err := w.Write(make([]byte, 3333)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 7)
	for range 100 { // the pending bytes, then part of the pipe's, played in odd-sized reads
		if _, err := src.Read(buf); err != nil {
			t.Fatal(err)
		}
	}
	src.Close() // the track ends with some read and not played
	if _, err := w.Write(make([]byte, 4097)); err != nil {
		t.Fatal(err)
	}
	drain(context.Background(), r, make([]byte, 16384), l, func() bool { return false }) // what it went on sending, set aside
	w.Close()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.read != l.through {
		t.Errorf("read %d bytes, counted %d as gone through", l.read, l.through)
	}
	if l.read != 101+3333+4097 {
		t.Errorf("read %d bytes, want all %d", l.read, 101+3333+4097)
	}
}

// What is waiting in the pipe is measured as it is: the bytes written and not yet read.
func TestSpotifyQueuedMeasuresThePipe(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()
	if n := spotifyQueued(); n != 0 {
		t.Fatalf("no pipe: %d queued", n)
	}
	spotifyPipe.Store(r)
	t.Cleanup(func() { spotifyPipe.Store(nil) })
	if _, err := w.Write(make([]byte, 1234)); err != nil {
		t.Fatal(err)
	}
	if n := spotifyQueued(); n != 1234 {
		t.Fatalf("%d queued, want 1234", n)
	}
	if _, err := r.Read(make([]byte, 234)); err != nil {
		t.Fatal(err)
	}
	if n := spotifyQueued(); n != 1000 {
		t.Fatalf("%d queued after a read, want 1000", n)
	}
}

// Scaled samples come out whole and in step, an odd byte from the pipe waiting for its other half, and
// a sample that would pass full scale stops there.
func TestScaledReadsStayInStep(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	in := []int16{100, -200, 300, 20000, -20000}
	raw := make([]byte, 2*len(in))
	for i, v := range in {
		binary.LittleEndian.PutUint16(raw[2*i:], uint16(v))
	}
	// The first byte arrives with what pump read before the track; the rest splits mid-sample.
	lv := &level{now: 2} // a gain already in force
	src := &pipeSource{f: r, pending: raw[:3], lv: lv, done: make(chan struct{})}
	go func() {
		_, _ = w.Write(raw[3:5])
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write(raw[5:])
		w.Close()
	}()
	if n, err := src.Read(make([]byte, 1)); n != 0 || err != nil {
		t.Fatalf("a one-byte read took %d bytes (%v)", n, err)
	}
	var out []byte
	buf := make([]byte, 7)
	for len(out) < len(raw) {
		n, err := src.Read(buf)
		if n%2 != 0 {
			t.Fatalf("read handed on %d bytes, half a sample", n)
		}
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	want := []int16{200, -400, 600, 32767, -32768}
	for i, v := range want {
		if 2*i+1 >= len(out) {
			t.Fatalf("only %d bytes came out", len(out))
		}
		if got := int16(binary.LittleEndian.Uint16(out[2*i:])); got != v {
			t.Errorf("sample %d: %d, want %d", i, got, v)
		}
	}
}

// The event program writes one tab-separated line per event, the artists joined and nothing in a name
// able to break the line.
func TestTheSpotifyEventProgram(t *testing.T) {
	dir := t.TempDir()
	script, out := filepath.Join(dir, "event"), filepath.Join(dir, "events")
	if err := os.WriteFile(script, []byte(spotifyEvent), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(env ...string) {
		cmd := exec.Command("/bin/sh", script)
		// The script's tr, cut and awk are found on the test machine's PATH, as on the device they are
		// on the daemon's: not every system keeps them in the shell's default /bin and /usr/bin.
		cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "TECHO5_SPOTIFY_EVENTS=" + out}, env...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, b)
		}
	}
	run("PLAYER_EVENT=track_changed", "NAME=One\tTwo", "ARTISTS=Eagles\nDon Henley", "ALBUM=Hotel California",
		"COVERS=https://i.scdn.co/image/big\nhttps://i.scdn.co/image/small")
	b, _ := os.ReadFile(out)
	if got := string(b); got != "track_changed\tOne Two\tEagles, Don Henley\tHotel California\thttps://i.scdn.co/image/big\t\n" {
		t.Errorf("wrote %q", got)
	}
	_ = os.Remove(out)
	run("PLAYER_EVENT=volume_changed", "VOLUME=32768")
	b, _ = os.ReadFile(out)
	if got := string(b); got != "volume_changed\t\t\t\t\t32768\n" {
		t.Errorf("wrote %q", got)
	}
	_ = os.Remove(out)
	run("PLAYER_EVENT=playing")
	if _, err := os.Stat(out); err == nil {
		t.Error("an event nothing reads wrote a line")
	}
}

// A name is a libconfig string whatever was typed for it, and covers are asked for only where there is
// a screen to show them.
func TestShairportConf(t *testing.T) {
	c := shairportConf(`Den's "Desk" \ one`+"\x01", "/run/x")
	if !strings.Contains(c, `name = "Den's \"Desk\" \\ one";`) {
		t.Errorf("conf:\n%s", c)
	}
	if want := fmt.Sprintf(`include_cover_art = "%s";`, yesNo(home.HasScreen())); !strings.Contains(c, want) {
		t.Errorf("conf without %s:\n%s", want, c)
	}
	// One session at a time, which airplaySession counts on.
	if strings.Contains(c, "allow_session_interruption") {
		t.Errorf("conf lets a sender break in:\n%s", c)
	}
}

// A receiver's audio plays as a track when it arrives; once that track is over (quiet, or something
// else played), what the receiver goes on sending is set aside until it pauses, and then plays again.
func TestThePumpLeavesTheSpeakerToWhatWasPlayedLast(t *testing.T) {
	was, wasAside := play, setAside
	setAside = 150 * time.Millisecond
	t.Cleanup(func() { play, setAside = was, wasAside })
	var mu sync.Mutex
	var started []media.PCMSource
	play = func(name string, src media.PCMSource, rate, channels int) {
		mu.Lock()
		started = append(started, src)
		mu.Unlock()
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(started) }
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pump(ctx, "AirPlay", r, nil, nil)

	w.Write(make([]byte, 4096))
	waitUntil(t, func() bool { return count() == 1 })
	// The track is taken over (something else played): the sender keeps going, and is set aside.
	mu.Lock()
	started[0].Close()
	mu.Unlock()
	for i := 0; i < 5; i++ {
		w.Write(make([]byte, 4096))
		time.Sleep(50 * time.Millisecond)
	}
	if count() != 1 {
		t.Fatal("a sender still going took the speaker straight back")
	}
	// It pauses, then plays again: that is asked for.
	time.Sleep(300 * time.Millisecond)
	w.Write(make([]byte, 4096))
	waitUntil(t, func() bool { return count() == 2 })
}

// A sender that starts to play again while what it sent is set aside takes the speaker back at once,
// without the quiet: an iPhone told to pause keeps its session, and silence coming, for seconds, and a
// play on it within them was set aside with the rest.
func TestPlayingAgainIsNotSetAside(t *testing.T) {
	was, wasAside := play, setAside
	setAside = 5 * time.Second // long, so only the new session can end the setting aside
	t.Cleanup(func() { play, setAside = was, wasAside })
	var mu sync.Mutex
	var started []media.PCMSource
	play = func(name string, src media.PCMSource, rate, channels int) {
		mu.Lock()
		started = append(started, src)
		mu.Unlock()
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return len(started) }
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	starts := &startCount{}
	go pump(ctx, "AirPlay", r, nil, starts)

	w.Write(make([]byte, 4096))
	waitUntil(t, func() bool { return count() == 1 })
	// Stopped here: the sender keeps going a while, and is set aside.
	mu.Lock()
	started[0].Close()
	mu.Unlock()
	sending := make(chan struct{})
	go func() {
		for {
			select {
			case <-sending:
				return
			default:
			}
			w.Write(make([]byte, 4096))
			time.Sleep(20 * time.Millisecond)
		}
	}()
	defer close(sending)
	time.Sleep(200 * time.Millisecond)
	if count() != 1 {
		t.Fatal("a sender still going took the speaker straight back")
	}
	starts.start() // the phone plays again
	waitUntil(t, func() bool { return count() == 2 })
}

// A track that ended on its own, the sender gone quiet (paused on the phone), is simply over: what
// arrives next plays at once, with nothing set aside.
func TestAQuietEndIsNotSetAside(t *testing.T) {
	was, wasAside := play, setAside
	setAside = 5 * time.Second // long, so a drain would show
	t.Cleanup(func() { play, setAside = was, wasAside })
	var mu sync.Mutex
	started := 0
	play = func(name string, src media.PCMSource, rate, channels int) {
		mu.Lock()
		started++
		mu.Unlock()
		// As the player does: reads until the sender has been quiet a while, then closes.
		go func() {
			buf := make([]byte, 4096)
			for {
				_ = src.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				if _, err := src.Read(buf); err != nil {
					src.Close()
					return
				}
			}
		}()
	}
	count := func() int { mu.Lock(); defer mu.Unlock(); return started }
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go pump(ctx, "AirPlay", r, nil, nil)

	w.Write(make([]byte, 4096))
	waitUntil(t, func() bool { return count() == 1 })
	time.Sleep(300 * time.Millisecond) // quiet: the track ends
	w.Write(make([]byte, 4096))
	waitUntil(t, func() bool { return count() == 2 })
}

// A program that keeps stopping is started again, each time after a longer pause.
func TestAProgramIsStartedAgain(t *testing.T) {
	was, wasMost := restartFirst, restartMost
	restartFirst, restartMost = 20*time.Millisecond, 80*time.Millisecond
	t.Cleanup(func() { restartFirst, restartMost = was, wasMost })
	runs := filepath.Join(t.TempDir(), "runs")
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	supervise(ctx, program{name: "test", path: "/bin/sh", args: []string{"-c", "echo x >> " + runs + "; exit 1"}})
	b, _ := os.ReadFile(runs)
	if n := strings.Count(string(b), "x"); n < 3 {
		t.Errorf("started %d times in 600 ms", n)
	}
}

// The receivers start only once avahi says it is up, and when avahi stops they stop with it and start
// again once a new avahi is up: they register with it once, as they start.
func TestTheReceiversFollowAvahi(t *testing.T) {
	wasRun, wasUp, wasAir, wasSpot, wasFirst, wasLeft := runAvahi, avahiUp, runAirPlay, runSpotify, restartFirst, leftovers
	t.Cleanup(func() {
		runAvahi, avahiUp, runAirPlay, runSpotify, restartFirst, leftovers = wasRun, wasUp, wasAir, wasSpot, wasFirst, wasLeft
	})
	leftovers = func() {}
	restartFirst = 10 * time.Millisecond
	var mu sync.Mutex
	var up bool
	var avahis, airplays, spotifies, running int
	stopAvahi := make(chan struct{}, 1)
	runAvahi = func(ctx context.Context) {
		mu.Lock()
		avahis++
		first := avahis == 1
		mu.Unlock()
		if first {
			time.Sleep(400 * time.Millisecond) // slow to come up
		}
		mu.Lock()
		up = true
		mu.Unlock()
		select {
		case <-ctx.Done():
		case <-stopAvahi:
		}
		mu.Lock()
		up = false
		mu.Unlock()
	}
	avahiUp = func(context.Context) bool { mu.Lock(); defer mu.Unlock(); return up }
	receiver := func(count *int) func(context.Context, string) {
		return func(ctx context.Context, _ string) {
			mu.Lock()
			if !up {
				t.Error("a receiver started before avahi was up")
			}
			*count++
			running++
			mu.Unlock()
			<-ctx.Done()
			mu.Lock()
			running--
			mu.Unlock()
		}
	}
	runAirPlay, runSpotify = receiver(&airplays), receiver(&spotifies)
	get := func() (int, int, int, int) { mu.Lock(); defer mu.Unlock(); return avahis, airplays, spotifies, running }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runGroup(ctx, "Kitchen", true, true); close(done) }()
	waitUntil(t, func() bool { _, a, s, r := get(); return a == 1 && s == 1 && r == 2 })
	stopAvahi <- struct{}{}
	waitUntil(t, func() bool { n, a, s, r := get(); return n == 2 && a == 2 && s == 2 && r == 2 })
	cancel()
	<-done
	if _, _, _, r := get(); r != 0 {
		t.Errorf("%d receivers still running after the group ended", r)
	}
}

func waitUntil(t *testing.T, ok func() bool) {
	t.Helper()
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("timed out")
}

// A librespot that crashed never said its phone went. Started again, it has none: a volume it says
// before the next phone's connect line is only where it was left, not a move of the slider.
func TestARestartedLibrespotHasNoPhone(t *testing.T) {
	f := newFakeSpotify(8)
	f.s.session(true)
	f.later(time.Minute)
	f.s.started() // crashed and started again
	f.later(time.Minute)
	f.s.heard(spotifyFull, false) // the next phone's connect, its volume line first
	f.s.session(true)
	f.s.heard(spotifyFull, true)
	if len(f.sets) != 0 || f.step != 8 {
		t.Errorf("the device went to %v", f.sets)
	}
	if f.told[len(f.told)-1] != spotifyValue(8) {
		t.Errorf("the app was not told the device's level: %v", f.told)
	}
}

// librespot skips saying a phone went when telling Spotify fails, so the device can think one is still
// connected when the next connects. A volume heard just before that connect's line was the connect's:
// it is taken back, and the app told the device's level.
func TestAVolumeJustBeforeAConnectIsTakenBack(t *testing.T) {
	f := newFakeSpotify(8)
	f.s.session(true)
	f.later(time.Minute) // the phone goes, unsaid; another connects
	f.s.heard(spotifyFull, false)
	f.later(100 * time.Millisecond)
	f.told = nil
	f.s.session(true)
	if f.step != 8 || !slices.Equal(f.sets, []int{30, 8}) {
		t.Errorf("the device is at %d after %v, want back at 8", f.step, f.sets)
	}
	if len(f.told) == 0 || f.told[len(f.told)-1] != spotifyValue(8) {
		t.Errorf("told %v", f.told)
	}
	// A move long before a connect is the slider's, and stays.
	f.later(time.Minute)
	f.s.heard(spotifyValue(20), false)
	f.later(time.Minute)
	f.s.session(true)
	if f.step != 20 {
		t.Errorf("a move a minute before a connect was taken back: at %d", f.step)
	}
}

// Each level handed to librespot is let by once when it comes back: the same level heard again soon
// after is a move of the slider, back to where it was.
func TestAnEchoIsLetByOnce(t *testing.T) {
	f := newFakeSpotify(10)
	f.s.session(true)
	f.later(5 * time.Second)
	f.press(11)
	f.s.heard(spotifyValue(11), false) // librespot saying it back
	f.press(12)
	f.s.heard(spotifyValue(12), false)
	f.later(time.Second)
	f.s.heard(spotifyValue(11), false) // the slider dragged back to 11
	if !slices.Equal(f.sets, []int{11}) || f.step != 11 {
		t.Errorf("the slider's 11 was taken as an echo: set %v", f.sets)
	}
}

// librespot's lines can come in another order than it said them: a level the device handed it and a
// drag of the slider crossing leave the app at the device's old level and the device at the slider's.
// Once the slider is still, the device's level is handed to librespot again, so the two agree.
func TestTheSliderSettlesOnTheDevicesLevel(t *testing.T) {
	f := newFakeSpotify(10)
	f.s.session(true)
	f.later(5 * time.Second)
	f.press(12)                        // a button; librespot takes it after the drag below
	f.s.heard(spotifyValue(15), false) // the drag
	f.s.heard(spotifyValue(12), false) // the button's level, said last: the app shows 12
	if f.step != 15 {
		t.Fatalf("the device is at %d", f.step)
	}
	if f.settle == nil {
		t.Fatal("no settle after the drag")
	}
	f.told = nil
	f.settle()
	if !slices.Equal(f.told, values(15)) {
		t.Errorf("settling told librespot %v, want the device's 15", f.told)
	}
	// Its echo is let by, and settles nothing more.
	f.settle = nil
	f.s.heard(spotifyValue(15), false)
	if f.settle != nil || f.step != 15 {
		t.Error("the settle's own echo started another")
	}
	// A restart stops a settle to come.
	f.s.heard(spotifyValue(20), false)
	f.s.started()
	if f.settle != nil {
		t.Error("a restart left a settle to come")
	}
}

// A settle that hands librespot the level it said last is not said back (librespot says a volume only
// when it changes), so it is not waited for as an echo: the slider dragged away and back to its end
// within moments moves the device back there.
func TestASettleLibrespotHasAlreadyIsNotWaitedFor(t *testing.T) {
	f := newFakeSpotify(20)
	f.s.session(true)
	f.later(5 * time.Second)
	f.s.heard(spotifyFull, false) // the slider to the top
	if f.step != 30 || f.settle == nil {
		t.Fatalf("the device is at %d, settle %v", f.step, f.settle != nil)
	}
	f.settle()
	f.later(time.Second)
	f.s.heard(spotifyValue(20), false)
	f.s.heard(spotifyFull, false) // and back to the top
	if !slices.Equal(f.sets, []int{30, 20, 30}) || f.step != 30 {
		t.Errorf("set %v and at %d: the slider's return to the top was taken for the settle's echo", f.sets, f.step)
	}
	// Should librespot say the settle's level after all, it is the device's own and moves nothing.
	f.settle = nil
	f.s.heard(spotifyFull, false)
	if !slices.Equal(f.sets, []int{30, 20, 30}) {
		t.Errorf("an unexpected echo moved the device: set %v", f.sets)
	}
}

// With an older librespot the slider moves the device by steps: a restart, or a phone connecting or
// going, starts it afresh, and a move heard just before a connect is taken back.
func TestTheFallbackSliderAcrossSessions(t *testing.T) {
	step := 8
	device := func() int { return step }
	set := func(s int) { step = s }
	a := &spotifyFallback{slider: slider{last: -1}}
	a.session(true, device, set)
	a.heard(32768, true, device, set)
	a.heard(39321, false, device, set) // 15 to 18
	if step != 11 {
		t.Fatalf("at %d, want 11", step)
	}
	a.started() // crashed; the next phone's volume comes before its connect
	a.heard(65535, false, device, set)
	if step != 11 {
		t.Errorf("the first level after a restart moved the device to %d", step)
	}
	// A phone going unsaid, then a level and a connect.
	a.heard(32768, false, device, set) // 30 to 15: -15
	a.session(true, device, set)
	if step != 11 {
		t.Errorf("a move just before a connect was kept: at %d", step)
	}
}

// librespot's arguments: with a volume pipe it takes the device's volume from it and sends the audio
// unscaled, with nothing for the pump to undo; without one it starts at full and scales linearly,
// which the pump undoes.
func TestSpotifyArgs(t *testing.T) {
	args, lv := spotifyArgs("Kitchen", "/c", "/s", "/run/v")
	j := strings.Join(args, " ")
	if lv != nil || !strings.Contains(j, "--volume-fifo /run/v") || strings.Contains(j, "--initial-volume") || strings.Contains(j, "--volume-ctrl") {
		t.Errorf("with the pipe: %v, level %v", args, lv)
	}
	args, lv = spotifyArgs("Kitchen", "/c", "/s", "")
	j = strings.Join(args, " ")
	if lv != spotifyLevel || strings.Contains(j, "--volume-fifo") || !strings.Contains(j, "--initial-volume 100 --volume-ctrl linear") {
		t.Errorf("without: %v, level %v", args, lv)
	}
	if !strings.Contains(j, "--name Kitchen") || !strings.Contains(j, "--onevent /s") || !strings.Contains(j, "--cache /c") {
		t.Errorf("args %v", args)
	}
}

// The volume pipe is made only for a librespot that takes volumes; one that cannot be made is none,
// and librespot is started without it.
func TestTheSpotifyVolumePipe(t *testing.T) {
	was := librespotTakesVolume
	t.Cleanup(func() { librespotTakesVolume = was })
	dir := t.TempDir()
	librespotTakesVolume = func() bool { return false }
	if p := spotifyVolumePipe(dir, nil); p != "" {
		t.Errorf("an older librespot was given %q", p)
	}
	librespotTakesVolume = func() bool { return true }
	p := spotifyVolumePipe(dir, nil)
	if st, err := os.Stat(p); p == "" || err != nil || st.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("the pipe %q: %v", p, err)
	}
	if p := spotifyVolumePipe(filepath.Join(dir, "missing"), nil); p != "" {
		t.Errorf("a pipe that could not be made is %q", p)
	}
}

// supervise says each time it is about to start the program, the first time and every time after.
func TestSuperviseSaysEachStart(t *testing.T) {
	was := restartFirst
	restartFirst = 20 * time.Millisecond
	t.Cleanup(func() { restartFirst = was })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	runs := filepath.Join(t.TempDir(), "runs")
	starts := 0
	supervise(ctx, program{name: "test", path: "/bin/sh", args: []string{"-c", "echo x >> " + runs + "; exit 1"},
		started: func() { starts++ }})
	b, _ := os.ReadFile(runs)
	if n := strings.Count(string(b), "x"); starts < 2 || starts < n {
		t.Errorf("started %d times, said %d", n, starts)
	}
}
