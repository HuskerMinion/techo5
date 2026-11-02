//go:build linux && asp_c_integration

package asp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise the independently built C decoder/processor. They are
// intentionally excluded from stock go test; set ASP_C_MOCK, ASP_C_FAULTS and
// ASP_C_DECODER to compiled integration artifacts when running this tag.
func compiledMock(t *testing.T) string {
	t.Helper()
	p := os.Getenv("ASP_C_MOCK")
	if p == "" {
		t.Fatal("ASP_C_MOCK required; must be accepted compiled C runtime/mock")
	}
	if _, e := os.Stat(p); e != nil {
		t.Fatal(e)
	}
	return p
}

func cFixture(t *testing.T) (*os.File, string, []string) {
	t.Helper()
	root, assets, env := fixture(t, compiledMock(t))
	clean := env[:0]
	for _, v := range env {
		if v != "ASP_TEST_CHILD=1" {
			clean = append(clean, v)
		}
	}
	return root, assets, clean
}

func TestCompiledCIPCAndFDPins(t *testing.T) {
	root, assets, env := cFixture(t)
	defer root.Close()
	s, e := startFixture(t, context.Background(), root, supported(), env)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	exe, err := os.Stat(filepath.Join(assets, "bin/external-asp"))
	if err != nil {
		t.Fatal(err)
	}
	fd3, err := os.Stat("/proc/" + itoa(s.cmd.Process.Pid) + "/fd/3")
	if err != nil || !os.SameFile(exe, fd3) {
		t.Fatalf("FD3 not pinned executable: %v", err)
	}
	a, err := os.Stat(assets)
	if err != nil {
		t.Fatal(err)
	}
	fd4, err := os.Stat("/proc/" + itoa(s.cmd.Process.Pid) + "/fd/4")
	if err != nil || !os.SameFile(a, fd4) {
		t.Fatalf("FD4 not pinned assets: %v", err)
	}
	input := pcm()
	first, e := s.Exchange(context.Background(), input)
	if e != nil {
		t.Fatal(e)
	}
	if len(first) != OutputBytes || !bytes.Equal(first[:128], make([]byte, 128)) || bytes.Equal(first[128:], make([]byte, 512)) {
		t.Fatal("first output must be 640 bytes, with exactly one initial 64-sample FIFO pad")
	}
	saved := bytes.Clone(first)
	for i := 0; i < 4; i++ {
		input[0][0] = byte(i + 1)
		got, e := s.Exchange(context.Background(), input)
		if e != nil {
			t.Fatal(e)
		}
		if len(got) != OutputBytes {
			t.Fatal("short wire output")
		}
		if i == 0 && bytes.Equal(got[:128], make([]byte, 128)) {
			t.Fatal("one-time padding repeated")
		}
	}
	if !bytes.Equal(first, saved) {
		t.Fatal("previously returned output mutated: ownership violated")
	}
	// The fixture is shared independently with accepted C source. Exact bytes.
	if ReadyByte != 0xa5 {
		t.Fatal("READY fixture changed")
	}
	if e := s.Close(); e != nil && !strings.Contains(e.Error(), "signal: killed") {
		t.Logf("child stop result: %v", e)
	}
	select {
	case <-s.done:
	default:
		t.Fatal("Wait reaper did not join")
	}
}

func TestCompiledCRejectsUnsupportedDescriptor(t *testing.T) {
	root, _, env := cFixture(t)
	defer root.Close()
	d := supported()
	d.Buffers = d.Buffers[:1]
	d.Channels = d.Channels[:4]
	_, e := startFixture(t, context.Background(), root, d, env)
	if e == nil || !strings.Contains(e.Error(), "READY") {
		t.Fatalf("valid unsupported geometry did not reject before READY: %v", e)
	}
}

func TestCompiledCPartialAndEINTR(t *testing.T) {
	root, _, env := cFixture(t)
	defer root.Close()
	preload := os.Getenv("ASP_C_FAULTS")
	if preload == "" {
		t.Fatal("ASP_C_FAULTS required for real C EINTR/short I/O injection")
	}
	env = append(env, "LD_PRELOAD="+preload) // ONLY test's compiled C mock; production Start hardcodes fixed env.
	s, e := startFixture(t, context.Background(), root, supported(), env)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Close()
	for i := 0; i < 2; i++ {
		if o, e := s.Exchange(context.Background(), pcm()); e != nil || len(o) != 640 {
			t.Fatalf("real C partial/EINTR exchange %d: %v", i, e)
		}
	}
}

func TestCompiledCDecodesGoldenVectors(t *testing.T) {
	decoder := os.Getenv("ASP_C_DECODER")
	if decoder == "" {
		t.Fatal("ASP_C_DECODER must name an independently built C decoder")
	}
	for name, desc := range vectors() {
		t.Run(name, func(t *testing.T) {
			got, err := Encode(desc)
			if err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(decoder)
			cmd.Stdin = bytes.NewReader(got)
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			// The literal digest and exact length come from the independently
			// accepted .expect file committed at 76379ddd, not the Go encoder
			// or this C decoder. Preserve complete-transcript verification.
			want := transcriptSHA256[name]
			observed := fmt.Sprintf("%x", sha256.Sum256(out))
			if len(out) != want.length || observed != want.digest {
				t.Fatalf("%s: C transcript length=%d (want %d), SHA-256=%s (want %s)",
					name, len(out), want.length, observed, want.digest)
			}
		})
	}
}
func TestCompiledCStereoReferenceSamples(t *testing.T) {
	decoder := os.Getenv("ASP_C_DECODER")
	if decoder == "" {
		t.Fatal("ASP_C_DECODER required")
	}
	descriptor, err := Encode(vectors()["stereo_s24"])
	if err != nil {
		t.Fatal(err)
	}
	pcm, err := os.ReadFile("testdata/stereo_s24.pcm")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(decoder, "--sample")
	cmd.Stdin = bytes.NewReader(append(descriptor, pcm...))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte("S 2 0 a1b2c3\n")) || !bytes.Contains(out, []byte("S 2 1 d4e5f6\n")) {
		t.Fatal("independent stereo REF source bytes not preserved")
	}
}
func TestPCMBlocksAreByteExactAcrossCompiledC(t *testing.T) {
	decoder := os.Getenv("ASP_C_DECODER")
	if decoder == "" {
		t.Fatal("ASP_C_DECODER is required for C integration")
	}
	for _, name := range []string{"stereo_s24", "separate_three"} {
		desc, err := Encode(vectors()[name])
		if err != nil {
			t.Fatal(err)
		}
		var pcm []byte
		if name == "separate_three" {
			// Immutable 16-byte PCM oracle formerly in separate_three.pcm.
			pcm = []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
				0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
		} else {
			pcm, err = os.ReadFile("testdata/stereo_s24.pcm")
			if err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command(decoder, "--roundtrip")
		cmd.Stdin = bytes.NewReader(append(desc, pcm...))
		got, err := cmd.Output()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got, pcm) {
			t.Fatalf("%s: changed physical PCM bytes", name)
		}
	}
}
