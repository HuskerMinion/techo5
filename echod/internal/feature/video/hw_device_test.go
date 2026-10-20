//go:build hwdevice && !dot

package video

// On a Show 8 only (go test -c -tags hwdevice, run as root on the unit): the player's hardware path as
// the daemon runs it, from a stream at HW_URL (an http:// address the decoder may reach) to frames
// placed on a 1280x800 RGBA panel, with the CPU it took.

import (
	"context"
	"image"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/gpu"
)

func cpuTicks(t *testing.T) (busy, total int) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		t.Fatal(err)
	}
	f := strings.Fields(strings.SplitN(string(b), "\n", 2)[0])[1:8]
	for i, s := range f {
		v, _ := strconv.Atoi(s)
		total += v
		if i != 3 {
			busy += v
		}
	}
	return busy, total
}

func TestDeviceHWPlay(t *testing.T) {
	url := os.Getenv("HW_URL")
	if url == "" {
		t.Skip("HW_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	info, err := probe(ctx, url, false, "")
	if err != nil {
		t.Fatal(err)
	}
	scr := Screen{W: 1280, H: 800, PixFmt: "rgba"}
	if !HWUsable(info, scr, hwOnce()) {
		t.Fatalf("not for the hardware: %+v (hardware there: %v)", info, hwOnce())
	}
	fit := FitIn(info, scr)
	b0, t0 := cpuTicks(t)
	began := time.Now()
	d, err := startHW(ctx, HWDecodeArgs(Decoding{URL: url, Info: info, Screen: scr}), GstArgs(info, scr), info.AudioIndex >= 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.audio != nil {
		go func() { _, _ = io.Copy(io.Discard, d.audio) }()
	}
	pix := make([]byte, 1280*800*4)
	n := 0
	pace, _ := strconv.Atoi(os.Getenv("HW_PACE")) // frames a second, as the screen takes them
	for ; ; n++ {
		if err := readFrame(d.video, pix, 1280, &fit); err != nil {
			break
		}
		if pace > 0 {
			time.Sleep(time.Until(began.Add(time.Duration(n+1) * time.Second / time.Duration(pace))))
		}
	}
	<-d.done
	d.stop()
	b1, t1 := cpuTicks(t)
	el := time.Since(began)
	t.Logf("%d frames of %dx%d at %+v in %s (%.1f fps); whole unit busy %.0f%% of a core; ended: %v %q",
		n, info.Width, info.Height, fit, el.Round(time.Millisecond), float64(n)/el.Seconds(),
		800*float64(b1-b0)/float64(t1-t0), d.err, d.stderr.all())
	if n == 0 {
		t.Fatal("no frames")
	}
	// The middle of the picture is the stream's, the bars opaque... not written: the session fills them
	if row := pix[(fit.Y+fit.H/2)*1280*4 : (fit.Y+fit.H/2+1)*1280*4]; row[3] != 255 {
		t.Errorf("the picture's alpha is %d", row[3])
	}
}

// The same stream on the cores, as the daemon plays it without the hardware, for comparison.
func TestDeviceSWPlay(t *testing.T) {
	url := os.Getenv("HW_URL")
	if url == "" {
		t.Skip("HW_URL not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	info, err := probe(ctx, url, false, "")
	if err != nil {
		t.Fatal(err)
	}
	scr := Screen{W: 1280, H: 800, PixFmt: "rgba"}
	b0, t0 := cpuTicks(t)
	began := time.Now()
	d, err := start(ctx, DecodeArgs(Decoding{URL: url, Info: info, Screen: scr}), true, info.AudioIndex >= 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.audio != nil {
		go func() { _, _ = io.Copy(io.Discard, d.audio) }()
	}
	pace, _ := strconv.Atoi(os.Getenv("HW_PACE"))
	pix := make([]byte, 1280*800*4)
	n := 0
	for ; ; n++ {
		if err := readFrame(d.video, pix, 1280, nil); err != nil {
			break
		}
		if pace > 0 {
			time.Sleep(time.Until(began.Add(time.Duration(n+1) * time.Second / time.Duration(pace))))
		}
	}
	<-d.done
	d.stop()
	b1, t1 := cpuTicks(t)
	el := time.Since(began)
	t.Logf("%d frames in %s (%.1f fps); whole unit busy %.0f%% of a core; ended: %v", n, el.Round(time.Millisecond),
		float64(n)/el.Seconds(), 800*float64(b1-b0)/float64(t1-t0), d.err)
}

// A camera's HLS stream at HW_CAM (and HW_CAM4, one the hardware does not take), as the camera page
// opens it, 896x672.
func TestDeviceCamera(t *testing.T) {
	for _, env := range []string{"HW_CAM", "HW_CAM4"} {
		url := os.Getenv(env)
		if url == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		b0, t0 := cpuTicks(t)
		c, err := openCamera(ctx, cameraIn(url, 672, false, ""), 896, 672)
		if err != nil {
			t.Fatal(err)
		}
		began := time.Now()
		n := 0
		var img []byte
		for ; n < 150; n++ {
			fr, err := c.Next(20 * time.Second)
			if err != nil && env == "HW_CAM4" {
				// it is to fall back to the cores; whether they read this stream is theirs to say
				t.Logf("%s: frame %d: %v (on the hardware still: %v)", env, n, err, c.hw)
				break
			}
			if err != nil {
				t.Fatalf("%s: frame %d: %v", env, n, err)
			}
			img = fr.Pix
		}
		b1, t1 := cpuTicks(t)
		t.Logf("%s: %d frames in %s, hardware %v at %+v; whole unit busy %.0f%% of a core", env, n,
			time.Since(began).Round(time.Millisecond), c.hw, c.place, 800*float64(b1-b0)/float64(t1-t0))
		if c.hw {
			// the bars black and opaque, the middle the picture's
			if img[3] != 255 || img[0] != 0 || img[(336*896+448)*4+3] != 255 {
				t.Errorf("%s: bars or picture wrong: %v %v", env, img[:4], img[(336*896+448)*4:(336*896+448)*4+4])
			}
		}
		c.Close()
		cancel()
	}
}
