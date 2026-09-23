package mic

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// WebRTCHelper is the helper that runs WebRTC's echo canceller (tools/aec/techo5-aec.cpp); the
// daemon feeds it blocks over pipes. Looked up on PATH and next to the daemon.
const WebRTCHelper = "techo5-aec"

// helperStall is how long a block may take before the helper is taken for hung. A block is 20 ms of
// audio and normally comes back in a few; a second is far past anything a busy CPU explains.
const helperStall = time.Second

// external is WebRTC's canceller in a helper process. One block of microphone and one of loopback
// go in; one block comes out, in order, so Process blocks for as long as the helper takes.
//
// A helper that dies or misbehaves marks itself broken and the canceller falls back to the
// built-in filter; the next SetEngine starts it again.
type external struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	in     io.WriteCloser
	out    *bufio.Reader
	buf    []byte
	broken atomic.Bool

	// erle is estimated here from what went in and what came out, averaged over about half a second.
	erle atomic.Int64 // thousandths of a dB
	inE  float64
	outE float64
}

func startExternal(args ...string) (*external, error) {
	path, err := exec.LookPath(WebRTCHelper)
	if err != nil {
		exe, _ := os.Executable()
		if exe != "" {
			path = exe[:len(exe)-len("techo5")] + WebRTCHelper
			if _, err := os.Stat(path); err != nil {
				return nil, fmt.Errorf("%s: not found", WebRTCHelper)
			}
		}
	}
	cmd := exec.Command(path, args...)
	cmd.Stderr = os.Stderr
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	e := &external{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 4*FrameSamples*2)}
	go func() {
		err := cmd.Wait()
		e.broken.Store(true)
		slog.Warn("echo cancellation helper exited", "err", err)
	}()
	slog.Info("echo cancellation helper started", "path", path, "args", args)
	return e, nil
}

// Process runs one block through the helper. len(mic) must equal len(ref).
func (e *external) Process(mic, ref []int16) ([]int16, error) {
	if e.broken.Load() {
		return nil, errors.New("helper is gone")
	}
	if len(mic) != len(ref) {
		return nil, fmt.Errorf("mic %d and reference %d differ", len(mic), len(ref))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	n := len(mic)
	if cap(e.buf) < 4*n {
		e.buf = make([]byte, 4*n)
	}
	b := e.buf[:4*n]
	// A helper that is alive but no longer answering would hold this goroutine, and capture with it,
	// in the Write or the Read below for good. Killing it closes its pipes, which ends either one.
	stall := time.AfterFunc(helperStall, func() {
		e.broken.Store(true)
		slog.Warn("echo cancellation helper stopped answering; killing it", "after", helperStall)
		_ = e.cmd.Process.Kill()
	})
	defer stall.Stop()
	for i, v := range mic {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	for i, v := range ref {
		binary.LittleEndian.PutUint16(b[2*n+2*i:], uint16(v))
	}
	if _, err := e.in.Write(b); err != nil {
		e.broken.Store(true)
		return nil, err
	}
	if _, err := io.ReadFull(e.out, b[:2*n]); err != nil {
		e.broken.Store(true)
		return nil, err
	}
	out := make([]int16, n)
	var inE, outE float64
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
		inE += float64(mic[i]) * float64(mic[i])
		outE += float64(out[i]) * float64(out[i])
	}
	// About half a second at 20 ms blocks.
	const k = 1.0 / 25
	e.inE += (inE - e.inE) * k
	e.outE += (outE - e.outE) * k
	if e.outE > 0 && e.inE > 0 {
		e.erle.Store(int64(10 * math.Log10(e.inE/e.outE) * 1000))
	}
	return out, nil
}

// ERLE is the estimated echo removal in dB.
func (e *external) ERLE() float64 { return float64(e.erle.Load()) / 1000 }

// Healthy reports whether the helper is still there.
func (e *external) Healthy() bool { return !e.broken.Load() }

// Close ends the helper.
func (e *external) Close() {
	// Kill first: a Process stuck on the helper holds the lock, and this is what lets it go.
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.in.Close()
}
