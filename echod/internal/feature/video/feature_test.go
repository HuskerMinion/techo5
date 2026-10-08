//go:build !dot

package video

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The test binary stands in for ffmpeg: run with TECHO5_FAKE_FFMPEG set, it answers a probe the way
// ffmpeg does, and for a decode writes frames to fd 3 and a second of silence to its standard output,
// then ends, or waits to be killed as a live stream does.
func TestMain(m *testing.M) {
	if os.Getenv("TECHO5_FAKE_FFMPEG") != "" {
		fakeFFmpeg()
		return
	}
	os.Exit(m.Run())
}

func fakeFFmpeg() {
	args := os.Args[1:]
	if !slices.Contains(args, "pipe:3") {
		os.Stderr.WriteString(probeMP4)
		os.Exit(1)
	}
	size, _ := strconv.Atoi(os.Getenv("TECHO5_FAKE_FRAME"))
	frames, _ := strconv.Atoi(os.Getenv("TECHO5_FAKE_FRAMES"))
	video := os.NewFile(3, "video")
	if slices.Contains(args, "pipe:1") {
		_, _ = os.Stdout.Write(make([]byte, 48000*4))
	}
	frame := make([]byte, size)
	for range frames {
		if _, err := video.Write(frame); err != nil {
			os.Exit(1)
		}
	}
	if os.Getenv("TECHO5_FAKE_HOLD") != "" {
		select {}
	}
	os.Exit(0)
}

// frameBytes is the tests' tiny panel's frame.
const testW, testH = 8, 16

func fake(t *testing.T, frames int, hold bool) *Feature {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().Video().On(true); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	savedPath, savedCred, savedLimits, savedEnv := ffmpegPath, decoderCred, limits, decoderEnv
	t.Cleanup(func() { ffmpegPath, decoderCred, limits, decoderEnv = savedPath, savedCred, savedLimits, savedEnv })
	ffmpegPath = exe
	decoderCred = func() (*syscall.Credential, error) { return nil, nil }
	limits = nil // the Go runtime of the stand-in reserves more address space than ffmpeg is allowed
	decoderEnv = []string{"PATH=/usr/bin:/bin", "TECHO5_FAKE_FFMPEG=1", "TECHO5_FAKE_FRAME=" + strconv.Itoa(FrameBytes(testW, testH)),
		"TECHO5_FAKE_FRAMES=" + strconv.Itoa(frames)}
	if hold {
		decoderEnv = append(decoderEnv, "TECHO5_FAKE_HOLD=1")
	}
	f := build()
	f.UseScreen(Screen{W: testH, H: testW, Rotated: true, PixFmt: "bgra"}, testW, testH)
	t.Cleanup(func() {
		f.Stop()
		f.runs.Wait()
	})
	return f
}

// until waits for the player to reach phase, taking the frames off as the screen would.
func until(t *testing.T, f *Feature, phase Phase) State {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if fr, _, _ := f.Next(); fr != nil {
			f.Done(fr)
		}
		st := f.State()
		if st.Phase == phase {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("still %s (%+v), want %s", st.Phase, st, phase)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNothingPlaysWithTheSwitchOff(t *testing.T) {
	f := fake(t, 10, false)
	if err := config.Set().Video().On(false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4"}); !errors.Is(err, ErrOff) {
		t.Errorf("played with the switch off: %v", err)
	}
	if err := config.Set().Video().On(true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4", Origin: FromDLNA, From: "192.168.1.30"}); !errors.Is(err, ErrOff) {
		t.Errorf("a DLNA video played with DLNA video off: %v", err)
	}
	if _, err := f.Play(Request{URL: "file:///etc/passwd"}); err == nil {
		t.Error("a file was played")
	}
	if f.State().Phase != Idle {
		t.Errorf("state %s", f.State().Phase)
	}
}

// Play, pause, carry on, stop.
func TestPlayPauseResumeStop(t *testing.T) {
	f := fake(t, 100000, true)
	id, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4", Title: "A film"})
	if err != nil {
		t.Fatal(err)
	}
	st := until(t, f, Playing)
	if st.ID != id || st.Title != "A film" || st.Host != "192.168.1.20" || st.Dur == 0 || !st.Frames {
		t.Errorf("playing: %+v", st)
	}
	f.Pause()
	until(t, f, Paused)
	f.Resume()
	until(t, f, Playing)
	f.Stop()
	if st := f.State(); st.Phase != Idle || st.Ended == id {
		t.Errorf("stopped: %+v", st)
	}
}

// A second video takes the place of the first.
func TestASecondVideoReplacesTheFirst(t *testing.T) {
	f := fake(t, 100000, true)
	first, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	until(t, f, Playing)
	second, err := f.Play(Request{URL: "http://192.168.1.21/b.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	st := until(t, f, Playing)
	if st.ID != second || st.ID == first || st.Host != "192.168.1.21" {
		t.Errorf("after the second: %+v", st)
	}
	f.StopID(first) // the first's controller stopping it is too late to stop the second
	if f.State().ID != second || !f.State().Active() {
		t.Error("stopping the first stopped the second")
	}
}

// A video that runs out ends by itself, and says so.
func TestAVideoEndsByItself(t *testing.T) {
	f := fake(t, 20, false)
	id, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4"})
	if err != nil {
		t.Fatal(err)
	}
	st := until(t, f, Idle)
	if st.Ended != id || st.Err != "" {
		t.Errorf("ended: %+v", st)
	}
}

// Something over the video pauses it, and it carries on once that has gone; a video somebody paused
// stays paused.
func TestACoveredVideoPausesUnderIt(t *testing.T) {
	f := fake(t, 100000, true)
	if _, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4"}); err != nil {
		t.Fatal(err)
	}
	until(t, f, Playing)
	f.Covered(true)
	until(t, f, Paused)
	f.Covered(false)
	until(t, f, Playing)
	f.Pause()
	f.Covered(true)
	f.Covered(false)
	if st := until(t, f, Paused); st.Phase != Paused {
		t.Error("a paused video was carried on")
	}
}

// The first DLNA video from an address asks on the screen: Not now leaves it and refuses that address
// for a while; Allow plays it and remembers the address, which does not ask again.
func TestADLNAVideoAsksFirst(t *testing.T) {
	f := fake(t, 100000, true)
	if err := config.Set().Video().DLNA(true); err != nil {
		t.Fatal(err)
	}
	req := Request{URL: "http://192.168.1.20/a.mp4", Title: "Clip", Origin: FromDLNA, From: "192.168.1.30"}
	id, err := f.Play(req)
	if err != nil {
		t.Fatal(err)
	}
	if st := f.State(); st.Phase != Asking || st.ID != id || st.From != "192.168.1.30" {
		t.Fatalf("asking: %+v", st)
	}
	if aid, from, title, ok := f.Asking(); !ok || aid != id || from != "192.168.1.30" || title != "Clip" {
		t.Errorf("the question: %v %q %q %v", aid, from, title, ok)
	}
	f.Answer(id, false)
	if st := f.State(); st.Phase != Idle {
		t.Errorf("after Not now: %s", st.Phase)
	}
	if _, err := f.Play(req); !errors.Is(err, ErrDeclined) {
		t.Errorf("asked again at once: %v", err)
	}

	req.From = "192.168.1.31"
	id, err = f.Play(req)
	if err != nil {
		t.Fatal(err)
	}
	f.Answer(id+1, true) // an answer to another question is nothing
	if f.State().Phase != Asking {
		t.Fatal("an answer to another question was taken")
	}
	f.Answer(id, true)
	until(t, f, Playing)
	if !config.Get().Video.IsAllowed("192.168.1.31", time.Now()) {
		t.Error("the address was not remembered")
	}
	f.Stop()
	if _, err := f.Play(req); err != nil {
		t.Fatal(err)
	}
	if f.State().Phase == Asking {
		t.Error("an allowed address was asked again")
	}
	// Each play starts the address's thirty days again.
	if used := config.Get().Video.Allowed; len(used) != 1 || time.Since(used[0].Used) > time.Minute {
		t.Errorf("allowed %+v", used)
	}
	// One unused for thirty days asks again.
	f.Stop()
	if err := config.Set().Video().Allow("192.168.1.40", time.Now().Add(-config.AllowedFor-time.Minute)); err != nil {
		t.Fatal(err)
	}
	req.From = "192.168.1.40"
	if _, err := f.Play(req); err != nil || f.State().Phase != Asking {
		t.Errorf("a run-out allowance played without asking: %v %s", err, f.State().Phase)
	}
	// Home Assistant never asks.
	f.Stop()
	if _, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4"}); err != nil || f.State().Phase == Asking {
		t.Errorf("Home Assistant was asked: %v", err)
	}
}

// Turning DLNA video off stops a DLNA video, and turning Video off stops any.
func TestTheSwitchesStopWhatTheyCover(t *testing.T) {
	f := fake(t, 100000, true)
	if err := config.Set().Video().DLNA(true); err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Video().Allow("192.168.1.30", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4", Origin: FromDLNA, From: "192.168.1.30"}); err != nil {
		t.Fatal(err)
	}
	until(t, f, Playing)
	f.SetDLNA(false)
	if f.State().Active() {
		t.Error("DLNA video off left a DLNA video playing")
	}
	if _, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4"}); err != nil {
		t.Fatal(err)
	}
	until(t, f, Playing)
	f.SetOn(false)
	if f.State().Active() {
		t.Error("Video off left a video playing")
	}
}

// The decoder runs bounded: little memory, no files written, no core, few files open.
func TestTheDecoderIsBounded(t *testing.T) {
	saved, savedPath, savedCred := limits, ffmpegPath, decoderCred
	t.Cleanup(func() { limits, ffmpegPath, decoderCred = saved, savedPath, savedCred })
	ffmpegPath = "/bin/sh"
	decoderCred = func() (*syscall.Credential, error) { return nil, nil }
	// From its first instruction: no sleep before it looks.
	d, err := start(context.Background(), []string{"-c", "cat /proc/self/limits >&2; echo nice=$(cut -d' ' -f19 /proc/self/stat) oom=$(cat /proc/self/oom_score_adj) >&2"}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	<-d.done
	out := d.stderr.all()
	for _, want := range []string{"Max address space", "Max file size", "Max core file size", "Max open files"} {
		line := ""
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, want) {
				line = l
			}
		}
		if line == "" || strings.Contains(line, "unlimited") {
			t.Errorf("%s: %q", want, line)
		}
	}
	if !strings.Contains(out, "nice=5") || !strings.Contains(out, "oom=1000") {
		t.Errorf("not niced: %s", out)
	}
}

// The address reaches the shell as an argument, never as its script.
func TestTheWrapperTakesNothingAsACommand(t *testing.T) {
	name, argv := wrap("/usr/local/bin/techo5-ffmpeg", []string{"-i", "http://x/$(reboot);`id`"})
	if name != "/bin/sh" || argv[0] != "-c" || strings.Contains(argv[1], "http") || argv[2] != "/usr/local/bin/techo5-ffmpeg" ||
		argv[len(argv)-1] != "http://x/$(reboot);`id`" {
		t.Errorf("%s %q", name, argv)
	}
}

// Every address in what ffmpeg says is cut down as the log's is: a redirect's or a playlist part's token
// is not passed on.
func TestWhatFFmpegSaysLosesItsTokens(t *testing.T) {
	got := scrubURLs("Server returned 403 for 'https://cdn.example.com/seg1.ts?token=secret' after http://user:pw@nas/x.m3u8")
	if strings.Contains(got, "secret") || strings.Contains(got, "pw") || !strings.Contains(got, "cdn.example.com/seg1.ts") {
		t.Errorf("scrubbed: %s", got)
	}
}

// The fence is made once and found again: each rule and the jump for the user added only when missing,
// IPv4 and IPv6 both, and a rule that will not go in stops the decoder from starting.
func TestTheFenceIsMadeOnceForTheUser(t *testing.T) {
	saved := iptables
	t.Cleanup(func() { iptables, noOwner = saved, nil })
	have := map[string]bool{}
	var added []string
	fail := ""
	iptables = func(tool string, args ...string) error {
		key := tool + " " + strings.Join(args[1:], " ")
		switch args[0] {
		case "-N":
			return nil
		case "-C":
			if have[key] {
				return nil
			}
			return errors.New("no such rule")
		}
		if fail != "" && strings.Contains(key, fail) {
			return errors.New("refused")
		}
		k := tool + " " + strings.Join(args[1:], " ")
		if args[0] == "-I" {
			k = tool + " " + args[1] + " " + strings.Join(args[3:], " ")
		}
		have[k] = true
		added = append(added, k)
		return nil
	}
	if err := ensureFence(89); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(added, "\n")
	for _, want := range []string{
		"iptables-legacy TECHO5-VIDEO -o lo -j REJECT",
		"iptables-legacy TECHO5-VIDEO -d 127.0.0.0/8 -j REJECT",
		"ip6tables-legacy TECHO5-VIDEO -d ::1/128 -j REJECT",
		"iptables-legacy OUTPUT -m owner --uid-owner 89 -j TECHO5-VIDEO",
		"ip6tables-legacy OUTPUT -m owner --uid-owner 89 -j TECHO5-VIDEO",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("not added: %s\n%s", want, joined)
		}
	}
	added = nil
	if err := ensureFence(89); err != nil || len(added) != 0 {
		t.Errorf("made again: %v %q", err, added)
	}
	fail = "65534"
	if err := ensureFence(65534); err == nil || noOwner != nil {
		t.Errorf("a fence that would not go in: %v, kept as for good: %v", err, noOwner)
	}
	if !ownerMissing(errors.New("iptables-legacy [-I OUTPUT 1 -m owner --uid-owner 65534 -j TECHO5-VIDEO]: exit status 1: Warning: Extension owner revision 0 not supported, missing kernel module? iptables: No chain/target/match by that name.")) {
		t.Error("the Spot's answer was not taken for a missing owner match")
	}
	if ownerMissing(errors.New("iptables-legacy: exit status 4: Another app is currently holding the xtables lock")) {
		t.Error("a held lock was taken for a missing owner match")
	}
	if !ownerMissing(errors.New("iptables v1.8.13 (legacy): Couldn't load match `owner':No such file or directory")) {
		t.Error("a missing extension was not taken for a missing owner match")
	}
	if ownerMissing(errors.New("iptables-legacy [-I OUTPUT 1 -m owner --uid-owner 89 -j TECHO5-VIDEO]: exit status 1: iptables: No chain/target/match by that name.")) {
		t.Error("a missing chain was taken for a missing owner match")
	}
}

// A DLNA video asked about while another plays: the state says so (AskID), and while the question is
// up a second address is refused rather than put in its place; Not now holds that address off.
func TestTheQuestionStaysWhatWasAsked(t *testing.T) {
	f := fake(t, 100000, true)
	if err := config.Set().Video().DLNA(true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4"}); err != nil {
		t.Fatal(err)
	}
	until(t, f, Playing)
	id, err := f.Play(Request{URL: "http://192.168.1.20/b.mp4", Origin: FromDLNA, From: "192.168.1.30"})
	if err != nil {
		t.Fatal(err)
	}
	if st := f.State(); st.Phase != Playing || st.AskID != id {
		t.Errorf("asking while playing: %+v", st)
	}
	if _, err := f.Play(Request{URL: "http://192.168.1.20/c.mp4", Origin: FromDLNA, From: "192.168.1.31"}); !errors.Is(err, ErrBusy) {
		t.Errorf("a second address replaced the question: %v", err)
	}
	if aid, _, _, _ := f.Asking(); aid != id {
		t.Errorf("the question is %d, want %d", aid, id)
	}
	f.Answer(id, false)
	if until := f.notNow["192.168.1.30"]; time.Until(until) < 9*time.Minute {
		t.Errorf("Not now holds the address off only until %v", until)
	}
}

func TestAZonedAddressIsTheDevicesOwn(t *testing.T) {
	if !ownHost("fe80::1%wlan0") || ownHost("192.0.2.1") {
		t.Error("ownHost is wrong about zones")
	}
}

// An Allow tapped after Video went off plays nothing.
func TestAnAllowAfterVideoWentOffPlaysNothing(t *testing.T) {
	f := fake(t, 100000, true)
	if err := config.Set().Video().DLNA(true); err != nil {
		t.Fatal(err)
	}
	id, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4", Origin: FromDLNA, From: "192.168.1.30"})
	if err != nil {
		t.Fatal(err)
	}
	if err := config.Set().Video().On(false); err != nil {
		t.Fatal(err)
	}
	f.Answer(id, true)
	if st := f.State(); st.Active() {
		t.Errorf("played with Video off: %+v", st)
	}
}

// Why a video could not be played is in the Video error sensor, cleared once one plays.
func TestTheVideoErrorSensor(t *testing.T) {
	f := fake(t, 0, false) // a decoder that gives no picture at all
	if _, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4"}); err != nil {
		t.Fatal(err)
	}
	until(t, f, Idle)
	deadline := time.Now().Add(5 * time.Second)
	for f.lastErr.Get() == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if f.lastErr.Get() == "" {
		t.Fatal("no error said")
	}
	decoderEnv = append(decoderEnv[:len(decoderEnv)-1:len(decoderEnv)-1], "TECHO5_FAKE_FRAMES=100000", "TECHO5_FAKE_HOLD=1")
	if _, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4"}); err != nil {
		t.Fatal(err)
	}
	until(t, f, Playing)
	for f.lastErr.Get() != "" && time.Now().Before(deadline.Add(5*time.Second)) {
		time.Sleep(10 * time.Millisecond)
	}
	if e := f.lastErr.Get(); e != "" {
		t.Errorf("still says %q while one plays", e)
	}
	if _, err := f.Play(Request{URL: "file:///etc/passwd"}); err == nil {
		t.Fatal("a file was played")
	}
}
