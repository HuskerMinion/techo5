package cast

import (
	"context"
	"fmt"
	"image"
	"net"
	"os"
	"sync"
	"testing"
	"time"
)

// benchSink counts what a cast delivers and how far from its due time each frame is shown.
type benchSink struct {
	mu            sync.Mutex
	frames, audio int
	audioBytes    int
	begin         time.Time
	ended         chan struct{}
}

func (s *benchSink) Begin(Hello) (Welcome, error) {
	s.begin = time.Now()
	return Welcome{OK: true, W: 960, H: 480, Rate: 48000, Channel: 2, LatencyMs: 250}, nil
}
func (s *benchSink) Frame(image.Image) { s.mu.Lock(); s.frames++; s.mu.Unlock() }
func (s *benchSink) Audio(p []byte, _ time.Time) {
	s.mu.Lock()
	s.audio++
	s.audioBytes += len(p)
	s.mu.Unlock()
}
func (s *benchSink) End() { close(s.ended) }

// TestBench listens for castsend and reports the frame rate and audio the device really sustains.
// It shows nothing on a screen. Build it for the device with
//
//	GOOS=linux GOARCH=arm GOARM=7 go test -c -o castbench ./internal/feature/cast
//
// and run it there as: CAST_BENCH=:8940 CAST_KEY=pairing-key ./castbench -test.run TestBench -test.v
func TestBench(t *testing.T) {
	addr := os.Getenv("CAST_BENCH")
	if addr == "" {
		t.Skip("CAST_BENCH not set")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	sink := &benchSink{ended: make(chan struct{})}
	r := &Receiver{Key: func() string { return os.Getenv("CAST_KEY") }, Sink: sink}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Serve(ctx, ln)
	fmt.Println("waiting for castsend on", ln.Addr())

	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	var lastF int
	for {
		select {
		case <-tick.C:
			sink.mu.Lock()
			f, a := sink.frames, sink.audioBytes
			sink.mu.Unlock()
			if !sink.begin.IsZero() {
				fmt.Printf("shown %d frames (%.1f fps), audio %.1f s\n", f, float64(f-lastF)/2, float64(a)/(48000*4))
			}
			lastF = f
		case <-sink.ended:
			sink.mu.Lock()
			fmt.Printf("done: %d frames in %.1f s = %.1f fps; audio %.1f s\n", sink.frames,
				time.Since(sink.begin).Seconds(), float64(sink.frames)/time.Since(sink.begin).Seconds(),
				float64(sink.audioBytes)/(48000*4))
			sink.mu.Unlock()
			return
		}
	}
}
