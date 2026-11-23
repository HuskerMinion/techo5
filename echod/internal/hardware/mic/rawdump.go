package mic

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// A raw dump of the capture while the daemon runs, for measuring the array over the daemon's own
// playback (music from Home Assistant through the real speaker path), which `echod tools mic` cannot:
// it needs the daemon held, and then nothing plays.
//
// Writing a number of seconds into rawDumpTrigger starts one: the frames exactly as ListenRaw hands
// them (Channels at Rate, S24_3LE: the microphones, then the reference) go to rawDumpDir as
// rawdump-<unix seconds>.s24, and the trigger is removed when it is done. Both live on tmpfs, so
// nothing is written to flash and a reboot clears them. Nothing is recorded while the trigger is
// absent: all that runs is a look for it once a second.
const (
	rawDumpTrigger = "/run/techo5/rawdump"
	rawDumpDir     = "/tmp"
	rawDumpMax     = 300 // seconds; 72 MB at the Dot 5's 240 KB/s
)

var rawDumpOnce sync.Once

// startRawDump watches for the trigger, once per process.
func (s *Source) startRawDump() {
	rawDumpOnce.Do(func() {
		safe.Go("raw dump", func() {
			for {
				time.Sleep(time.Second)
				b, err := os.ReadFile(rawDumpTrigger)
				if err != nil {
					continue
				}
				secs, err := strconv.Atoi(strings.TrimSpace(string(b)))
				if err != nil || secs <= 0 {
					secs = 30
				}
				s.rawDump(min(secs, rawDumpMax))
				_ = os.Remove(rawDumpTrigger)
			}
		})
	})
}

func (s *Source) rawDump(secs int) {
	path := rawDumpDir + "/rawdump-" + strconv.FormatInt(time.Now().Unix(), 10) + ".s24"
	f, err := os.Create(path + ".part")
	if err != nil {
		slog.Error("raw dump", "err", err)
		return
	}
	frames, done := s.ListenRaw()
	defer done()
	slog.Info("raw dump started", "path", path, "seconds", secs, "channels", Channels, "rate", Rate)
	want := secs * Rate * Channels * 3
	got := 0
	deadline := time.After(time.Duration(secs+5) * time.Second)
	for got < want {
		select {
		case buf, ok := <-frames:
			if !ok {
				want = got
				break
			}
			n := min(len(buf), want-got)
			if _, err := f.Write(buf[:n]); err != nil {
				slog.Error("raw dump", "err", err)
				want = got
			}
			got += n
		case <-deadline:
			want = got
		}
	}
	_ = f.Close()
	_ = os.Rename(path+".part", path)
	slog.Info("raw dump done", "path", path, "bytes", got)
}
