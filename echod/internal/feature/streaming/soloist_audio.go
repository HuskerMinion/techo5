package streaming

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"os"
	"sync/atomic"

	"golang.org/x/sys/unix"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
)

// PulseAudio's software gain is cubic in pa_volume_t. The official client sets its sink-input
// volume to floor(percent * 65535 / 100), verified on-device. Undo that attenuation in float32
// before S16 conversion; doing it after S16 would discard most precision at low phone volumes.
func soloistVolumeGain(percent int) float64 {
	if percent <= 0 || percent >= 100 {
		return 1
	}
	raw := float64(percent*65535/100) / 65536
	return 1 / (raw * raw * raw)
}

func soloistVolumeValue(percent int) int {
	return spotifyValue((percent*media.VolumeSteps + 50) / 100)
}

func pipeQueuedBytes(f *os.File) int {
	rc, err := f.SyscallConn()
	if err != nil {
		return 0
	}
	n := 0
	_ = rc.Control(func(fd uintptr) { n, _ = unix.IoctlGetInt(int(fd), unix.TIOCINQ) })
	return n
}

// Record the clocked null sink's monitor continuously. Discard idle silence; while playing,
// normalize the float stream to unity and give TECHO5 S16 PCM at its own device volume.
func forwardSoloistFloatPCM(ctx context.Context, r io.Reader, w io.Writer, playing *atomic.Bool, lv *level) {
	b := make([]byte, 16384)
	var pending []byte
	for ctx.Err() == nil {
		n, err := r.Read(b)
		if n > 0 {
			lv.fromPipe(n)
			data := append(pending, b[:n]...)
			count := len(data) / 4 * 4
			if !playing.Load() {
				pending = append(pending[:0], data[count:]...)
				lv.passed(count)
				if err != nil {
					return
				}
				continue
			}
			out := make([]byte, count/2)
			gain := lv.gain()
			for i := 0; i < count; i += 4 {
				v := float64(math.Float32frombits(binary.LittleEndian.Uint32(data[i:]))) * gain * 32768
				if math.IsNaN(v) {
					v = 0
				}
				v = math.Max(-32768, math.Min(32767, math.Round(v)))
				binary.LittleEndian.PutUint16(out[i/2:], uint16(int16(v)))
			}
			pending = append(pending[:0], data[count:]...)
			if playing.Load() {
				if _, writeErr := w.Write(out); writeErr != nil {
					return
				}
			}
			lv.passed(count)
		}
		if err != nil {
			return
		}
	}
}
