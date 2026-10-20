//go:build linux

// Package vencpipe is the 2nd gen Show 5's H.264 encoder: the MT8163's, driven by MediaTek's own
// libraries through vcodec-enc (custom-kernel/cronos: patches/mt8163-vcodec.c, /dev/Vcodec;
// vcodec-user/harness/vcodec_enc.c), which runs on the Android runtime the image carries in
// /usr/local/lib/techo5-vcodec with the unit's own libraries from /vendor/lib. A frame goes in as
// I420 and comes back as its H.264, one at a time; the encoder does the work, the cores only move the
// bytes.
package vencpipe

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
)

// Dir is the image's Android runtime and vcodec-enc; TECHO5_VCODEC names another (a build tried in
// place before it is installed).
var Dir = func() string {
	if p := os.Getenv("TECHO5_VCODEC"); p != "" {
		return p
	}
	return "/usr/local/lib/techo5-vcodec"
}()

// vendorLib is where the unit keeps MediaTek's libraries.
var vendorLib = "/vendor/lib"

// Available is whether the encoder is there: the driver, the runtime and the unit's libraries.
func Available() bool {
	for _, p := range []string{"/dev/Vcodec", filepath.Join(Dir, "system/bin/linker"),
		filepath.Join(Dir, "system/bin/vcodec_enc"), filepath.Join(vendorLib, "libvcodecdrv.so")} {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

// Config is the stream: its size (even), rate, bitrate and keyframe spacing in frames.
type Config struct {
	W, H, FPS, Bitrate, GOP int
}

// Encoder is one run of vcodec-enc.
type Encoder struct {
	mu   sync.Mutex
	cfg  Config
	cmd  *exec.Cmd
	in   io.WriteCloser
	out  *bufio.Reader
	head []byte // the parameter sets, its first record
	err  error
	// pending is whether a frame is in the encoder whose record has not been read: Encode hands back
	// the frame before, so the encoder works on one frame while the cores develop the next.
	pending bool
	// records are the encoder's frames as a goroutine of their own reads them, so a keyframe larger than
	// the pipe never leaves the encoder waiting to write it while Encode waits to write the next frame.
	records chan record
	done    chan struct{} // closed by Close: the reader stops
}

type record struct {
	data  []byte
	flags uint32
	err   error
}

// record flags (vcodec_enc.c)
const (
	flagKey     = 1
	flagHeaders = 2
)

// New starts the encoder.
func New(cfg Config) (*Encoder, error) {
	if cfg.W <= 0 || cfg.H <= 0 || cfg.W%2 != 0 || cfg.H%2 != 0 || cfg.FPS <= 0 {
		return nil, fmt.Errorf("vencpipe: a stream of %+v", cfg)
	}
	cmd := exec.Command(filepath.Join(Dir, "system/bin/linker"), filepath.Join(Dir, "system/bin/vcodec_enc"),
		"-s", strconv.Itoa(cfg.W)+"x"+strconv.Itoa(cfg.H), "-b", strconv.Itoa(cfg.Bitrate),
		"-r", strconv.Itoa(cfg.FPS), "-g", strconv.Itoa(cfg.GOP))
	cmd.Dir = "/"
	cmd.Env = []string{"LD_LIBRARY_PATH=" + filepath.Join(Dir, "system/lib") + ":" + filepath.Join(Dir, "shim") + ":" + vendorLib,
		"VCODEC_PROP_FILE=" + filepath.Join(Dir, "etc/vcodec.prop")}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("vencpipe: %w", err)
	}
	e := &Encoder{cfg: cfg, cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20)}
	data, flags, err := e.record()
	if err != nil || flags&flagHeaders == 0 {
		e.Close()
		if err == nil {
			err = errors.New("no parameter sets")
		}
		return nil, fmt.Errorf("vencpipe: the encoder did not start: %w", err)
	}
	e.head = data
	e.records, e.done = make(chan record, 4), make(chan struct{})
	go func() {
		defer close(e.records)
		for {
			data, flags, err := e.record()
			select {
			case e.records <- record{data, flags, err}:
			case <-e.done:
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return e, nil
}

func (e *Encoder) record() ([]byte, uint32, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(e.out, hdr[:]); err != nil {
		return nil, 0, err
	}
	n, flags := binary.LittleEndian.Uint32(hdr[:]), binary.LittleEndian.Uint32(hdr[4:])
	if n > 16<<20 {
		return nil, 0, errors.New("vencpipe: a record of the wrong size")
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(e.out, data); err != nil {
		return nil, 0, err
	}
	return data, flags, nil
}

// Size is the stream's picture.
func (e *Encoder) Size() (w, h int) { return e.cfg.W, e.cfg.H }

// Headers is the stream's parameter sets (SPS and PPS, Annex-B).
func (e *Encoder) Headers() []byte { return e.head }

// Encode hands the encoder one I420 frame of the stream's size and returns the frame before's H.264
// (Annex-B; a keyframe has the parameter sets in front) and whether it is a keyframe; nothing for the
// first. The encoder works on a frame while the caller develops the next, so a frame takes the longer
// of the two rather than both (1600x1200: the cores' 36 ms and the encoder's 20).
func (e *Encoder) Encode(i420 []byte) ([]byte, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.err != nil {
		return nil, false, e.err
	}
	if len(i420) != e.cfg.W*e.cfg.H*3/2 {
		return nil, false, errors.New("vencpipe: a frame of another size")
	}
	// The frame before is done or nearly: the encoder reads this one only once it has written that.
	if _, err := e.in.Write(i420); err != nil {
		e.err = fmt.Errorf("vencpipe: the encoder: %w", err)
		return nil, false, e.err
	}
	if !e.pending {
		e.pending = true
		return nil, false, nil
	}
	r, ok := <-e.records
	if !ok {
		r.err = io.ErrUnexpectedEOF
	}
	data, flags, err := r.data, r.flags, r.err
	if err != nil {
		e.err = fmt.Errorf("vencpipe: the encoder: %w", err)
		return nil, false, e.err
	}
	return data, flags&flagKey != 0, nil
}

// ForceKey makes the next frame a keyframe (a viewer has joined).
func (e *Encoder) ForceKey() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Signal(syscall.SIGUSR1)
	}
}

// Close ends the encoder.
func (e *Encoder) Close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cmd == nil {
		return
	}
	e.in.Close()
	if e.done != nil {
		close(e.done)
	}
	_ = syscall.Kill(-e.cmd.Process.Pid, syscall.SIGKILL)
	_ = e.cmd.Wait()
	e.cmd = nil
	if e.err == nil {
		e.err = errors.New("vencpipe: closed")
	}
}
