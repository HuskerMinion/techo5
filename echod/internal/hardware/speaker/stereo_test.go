package speaker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/lib/asp"
)

func TestInternalStereoBoards(t *testing.T) {
	for _, tc := range []struct {
		board   string
		enabled bool
		stereo  bool
	}{
		{"athena", false, false}, {"athena", true, true},
		{"crown", false, false}, {"crown", true, false},
		{"checkers", true, false}, {"cronos", true, false},
		{"biscuit", true, false}, {"radar", true, false},
		{"rook", true, false}, {"skylight", true, false}, {"", true, false},
	} {
		if got := stereoInternalSpeakers(tc.board, tc.enabled); got != tc.stereo {
			t.Errorf("board %q: stereo=%t, want %t", tc.board, got, tc.stereo)
		}
	}
}

func TestSpeakerStereoAndMonoUntuned(t *testing.T) {
	for _, tc := range []struct {
		name   string
		out    Output
		stereo bool
		left   int16
		right  int16
	}{
		{"mono speaker", OutputSpeaker, false, 200, 200},
		{"stereo speaker", OutputSpeaker, true, 100, 300},
		{"mono board, both outputs", OutputBoth, false, 200, 200},
		{"stereo board, both outputs", OutputBoth, true, 100, 300},
		{"mono board, headphones", OutputHeadphone, false, 100, 300},
		{"stereo board, headphones", OutputHeadphone, true, 100, 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestPlayer()
			p.out, p.canStereo, p.swapLR = tc.out, true, false
			p.stereo.Store(tc.stereo)
			p.outNow.Store(tc.out)
			p.pending = []int16{100, 300}
			got := played(t, p)
			if got[0] != tc.left || got[1] != tc.right {
				t.Errorf("channels %d/%d, want %d/%d", got[0], got[1], tc.left, tc.right)
			}
		})
	}
}

// Synthetic 1024-tap identity FIR and conservative four-band limiter: no vendor coefficients checked in.
func syntheticTunedPlayer(t testing.TB, stereo bool) *Player {
	t.Helper()
	dir := t.TempDir()
	coeffs := "1,\n" + strings.Repeat("0,\n", 1023)
	if err := os.WriteFile(filepath.Join(dir, "EQ.cfg"), []byte(coeffs), 0o600); err != nil {
		t.Fatal(err)
	}
	mbcl := `{
		"Bypass":false, "PreFilterBypass":true, "inVol":0,
		"NumBands":4, "FilterBank FC":[115,1000,5000],
		"Bands Definition":[
			{"comp_ratio":2, "comp_thresh":-24, "comp_gainMin":-24,
			 "lim_thresh":-1, "lim_release":20},
			{"comp_ratio":1, "comp_thresh":-24, "comp_gainMin":-24,
			 "lim_thresh":-1, "lim_release":20},
			{"comp_ratio":1, "comp_thresh":-24, "comp_gainMin":-24,
			 "lim_thresh":-1, "lim_release":20},
			{"comp_ratio":1, "comp_thresh":-24, "comp_gainMin":-24,
			 "lim_thresh":-1, "lim_release":20}
		],
		"Full-band limiter":{"lim_thresh":-1, "lim_release":20}
	}`
	if err := os.WriteFile(filepath.Join(dir, "MBCL.cfg"), []byte(mbcl), 0o600); err != nil {
		t.Fatal(err)
	}
	set := asp.Set{
		Name: "stereo-fixture",
		EQ:   []asp.Bucket{{UpTo: 1, Name: "EQ.cfg"}},
		MBCL: "MBCL.cfg", Taps: 1024,
	}
	tun, err := asp.LoadSet(dir, set)
	if err != nil {
		t.Fatal(err)
	}
	p := newTestPlayer()
	p.out, p.canStereo, p.tuning = OutputSpeaker, stereo, tun
	p.stereo.Store(stereo)
	p.outNow.Store(OutputSpeaker)
	p.on.Store(true)
	p.step.Store(VolumeSteps)
	lanes := 1
	if stereo {
		lanes = Channels
	}
	for range lanes {
		c, err := tun.Chain(period)
		if err != nil {
			t.Fatal(err)
		}
		p.chains = append(p.chains, c)
		p.lanes = append(p.lanes, make([]float32, period))
	}
	return p
}

func queueChannel(p *Player, left, right int16) {
	p.pending = make([]int16, period*Channels)
	for i := 0; i < period*Channels; i += Channels {
		p.pending[i], p.pending[i+1] = left, right
	}
}

func TestStereoDSPChannelIsolationAndReset(t *testing.T) {
	p := syntheticTunedPlayer(t, true)
	heardLeft := false
	for range 5 {
		queueChannel(p, 10000, 0)
		out := played(t, p)
		for i := 0; i < len(out); i += Channels {
			if out[i] != 0 {
				heardLeft = true
			}
			if out[i+1] != 0 {
				t.Fatalf("left-only music leaked into right: frame %d = %d", i/2, out[i+1])
			}
		}
	}
	if !heardLeft {
		t.Fatal("left DSP chain produced no signal")
	}

	p.stale.Store(true)
	heardRight := false
	for range 5 {
		queueChannel(p, 0, 10000)
		out := played(t, p)
		for i := 0; i < len(out); i += Channels {
			if out[i] != 0 {
				t.Fatalf("after reset, old left leaked: frame %d = %d", i/2, out[i])
			}
			if out[i+1] != 0 {
				heardRight = true
			}
		}
	}
	if !heardRight {
		t.Fatal("right DSP chain produced no signal")
	}
}

func TestStereoDSPOffKeepsChannels(t *testing.T) {
	p := syntheticTunedPlayer(t, true)
	p.on.Store(false)
	p.pending = []int16{100, 300}
	out := played(t, p)
	if out[0] != 100 || out[1] != 300 {
		t.Errorf("speaker EQ off produced %d/%d, want 100/300", out[0], out[1])
	}
}

func TestMonoDSPStillFeedsBothChannels(t *testing.T) {
	p := syntheticTunedPlayer(t, false)
	audible := false
	for range 5 {
		queueChannel(p, 9000, 0)
		out := played(t, p)
		for i := 0; i < len(out); i += Channels {
			if out[i] != out[i+1] {
				t.Fatalf("mono DSP differs: %d/%d", out[i], out[i+1])
			}
			if out[i] != 0 {
				audible = true
			}
		}
	}
	if !audible {
		t.Fatal("mono DSP produced no signal")
	}
}

type stereoConstantSource struct{ left, right int16 }

func (s stereoConstantSource) Render(_ uint64, out []int16) {
	for i := 0; i < len(out); i += Channels {
		out[i], out[i+1] = s.left, s.right
	}
}

func benchmarkSpeakerDSP(b *testing.B, stereo bool) {
	p := syntheticTunedPlayer(b, stereo)
	p.Attach(stereoConstantSource{left: 9500, right: 4500})
	buf := make([]byte, period*Channels*2)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		p.fill(buf)
		p.written.Add(period)
	}
}

func BenchmarkSpeakerDSPMono(b *testing.B)   { benchmarkSpeakerDSP(b, false) }
func BenchmarkSpeakerDSPStereo(b *testing.B) { benchmarkSpeakerDSP(b, true) }

func TestStereoDSPAppliesToneToBothChannels(t *testing.T) {
	p := syntheticTunedPlayer(t, true)
	for _, tone := range []asp.Tone{
		{Bass: -5, Treble: -3},
		{Bass: 4, Treble: 2},
		{},
	} {
		p.SetTone(tone)
		audible := false
		for range 5 {
			queueChannel(p, 8000, 8000)
			out := played(t, p)
			for i := 0; i < len(out); i += Channels {
				if out[i] != out[i+1] {
					t.Fatalf("tone %+v differs between L/R: %d/%d", tone, out[i], out[i+1])
				}
				if out[i] != 0 {
					audible = true
				}
			}
		}
		if !audible {
			t.Fatalf("tone %+v muted both channels", tone)
		}
	}
	p.SetASP(false)
	if !p.SetASP(true) {
		t.Fatal("enabling tuning on a two-chain player was refused")
	}
	queueChannel(p, 8000, 8000)
	out := played(t, p)
	for i := 0; i < len(out); i += Channels {
		if out[i] != out[i+1] {
			t.Fatalf("EQ re-enable changed L/R differently: %d/%d", out[i], out[i+1])
		}
	}
}

// An optional integration test against local vendor files, deliberately never checked into git.
// Run with TECHO5_STEREO_VENDOR_DIR=/path/to/vendor/audio-algorithms.
func TestStereoWithLocalVendorTuning(t *testing.T) {
	dir := os.Getenv("TECHO5_STEREO_VENDOR_DIR")
	if dir == "" {
		t.Skip("set TECHO5_STEREO_VENDOR_DIR for Crown's vendor EQ and MBCL")
	}
	tun, err := asp.LoadSet(dir, asp.Crown)
	if err != nil {
		t.Fatal(err)
	}
	p := newTestPlayer()
	p.out, p.canStereo, p.tuning = OutputSpeaker, true, tun
	p.stereo.Store(true)
	p.outNow.Store(OutputSpeaker)
	p.on.Store(true)
	p.step.Store(20)
	for range Channels {
		chain, err := tun.Chain(period)
		if err != nil {
			t.Fatal(err)
		}
		p.chains = append(p.chains, chain)
		p.lanes = append(p.lanes, make([]float32, period))
	}
	heard := false
	for range 5 {
		queueChannel(p, 3500, 0)
		out := played(t, p)
		for i := 0; i < len(out); i += Channels {
			if out[i] != 0 {
				heard = true
			}
			if out[i+1] != 0 {
				t.Fatalf("vendor DSP cross-fed right from left input: frame %d = %d", i/2, out[i+1])
			}
		}
	}
	if !heard {
		t.Fatal("vendor tuning did not pass audible left signal")
	}
}

// Athena's physical speaker assignment is reversed relative to interleaved
// playback PCM. This fix must not reverse the headphone-only output.
func TestAthenaPhysicalChannelMapUntuned(t *testing.T) {
	for _, tc := range []struct {
		name        string
		out         Output
		left, right int16
	}{
		{"speaker", OutputSpeaker, 300, 100},
		{"both", OutputBoth, 300, 100},
		{"headphones only", OutputHeadphone, 100, 300},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newTestPlayer()
			p.canStereo, p.swapLR = true, true
			p.stereo.Store(true)
			p.outNow.Store(tc.out)
			p.pending = []int16{100, 300}
			got := played(t, p)
			if got[0] != tc.left || got[1] != tc.right {
				t.Fatalf("physical playback slots %d/%d, want %d/%d", got[0], got[1], tc.left, tc.right)
			}
		})
	}
}

// Verify channel mapping in both independent EQ/limiter chains and
// ensure the opposite channel retains no stale DSP history after reset.
func TestAthenaPhysicalChannelMapTuned(t *testing.T) {
	p := syntheticTunedPlayer(t, true)
	p.swapLR = true
	for _, tc := range []struct {
		name                      string
		logicalLeft, logicalRight int16
		physicalSlot              int
	}{
		{"left to physical right PCM", 10000, 0, 1},
		{"right to physical left PCM", 0, 10000, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.stale.Store(true)
			heard := false
			for range 5 {
				queueChannel(p, tc.logicalLeft, tc.logicalRight)
				out := played(t, p)
				for i := 0; i < len(out); i += Channels {
					other := tc.physicalSlot ^ 1
					if out[i+other] != 0 {
						t.Fatalf("unexpected leakage into PCM slot %d: frame %d = %d", other, i/2, out[i+other])
					}
					if out[i+tc.physicalSlot] != 0 {
						heard = true
					}
				}
			}
			if !heard {
				t.Fatalf("DSP did not output on PCM slot %d", tc.physicalSlot)
			}
		})
	}
}

// Stereo turns on and off while playing on a board that can, starting the tuning's history again,
// and stays mono on one that cannot.
func TestSetStereoIsLive(t *testing.T) {
	p := syntheticTunedPlayer(t, true)
	p.stereo.Store(false)
	p.stale.Store(false)
	if !p.SetStereo(true) || !p.stereo.Load() || !p.stale.Load() {
		t.Fatal("stereo did not come on, or the tuning was not started again")
	}
	p.stale.Store(false)
	if p.SetStereo(true); p.stale.Load() {
		t.Error("asking for what is already on started the tuning again")
	}
	if p.SetStereo(false) || p.stereo.Load() {
		t.Error("stereo did not go off")
	}

	mono := syntheticTunedPlayer(t, false)
	if mono.SetStereo(true) || mono.stereo.Load() {
		t.Error("a board with one driver went stereo")
	}
}
