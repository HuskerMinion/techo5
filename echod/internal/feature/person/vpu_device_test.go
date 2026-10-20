//go:build linux && !dot && hwdevice

package person

import (
	"math"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/camera"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/vpu"
)

// On a Show 8 with the VPU: the model's convolutions on the VPU, byte for byte the uint8 model on the
// CPU, and the scores against the float model's (PERSON_IMAGES: 96x96 gray pictures, back to back).
func TestDeviceVPU(t *testing.T) {
	if !vpu.Available() {
		t.Skip("no " + vpu.Path)
	}
	d, q := loadQuant(t)
	began := time.Now()
	tr, err := openVPUTrunk(q)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("VPU up in %s", time.Since(began).Round(time.Microsecond))
	defer tr.close()
	out := make([]uint8, q.trunkOut())
	var best, total time.Duration
	var sum, worst float64
	flips5, flips6, high := 0, 0, 0
	imgs := testImages(t)
	for i, g := range imgs {
		began := time.Now()
		if err := tr.run(g, out); err != nil {
			t.Fatal(err)
		}
		el := time.Since(began)
		total += el
		if i == 0 || el < best {
			best = el
		}
		want := q.cpu(g)
		for k := range want {
			if out[k] != want[k] {
				t.Fatalf("picture %d: byte %d is %d on the VPU, %d on the CPU", i, k, out[k], want[k])
			}
		}
		f, err := d.score(g)
		if err != nil {
			t.Fatal(err)
		}
		u := q.tail(out)
		e := math.Abs(float64(f - u))
		sum, worst = sum+e, max(worst, e)
		if (f >= 0.5) != (u >= 0.5) {
			flips5++
		}
		if (f >= threshold) != (u >= threshold) {
			flips6++
		}
		if f >= 0.5 {
			high++
		}
		if len(imgs) <= 40 || e > 0.1 {
			t.Logf("picture %3d: float %.3f, VPU %.3f; %s", i, f, u, el.Round(time.Microsecond))
		}
	}
	n := float64(len(imgs))
	t.Logf("%d pictures (%d at 0.5 or more in float), all byte-exact: |VPU - float| mean %.4f, worst %.3f; decided otherwise: %d at 0.5, %d at %.1f; %s an inference at best, %s on average",
		len(imgs), high, sum/n, worst, flips5, flips6, threshold, best.Round(time.Microsecond), (total / time.Duration(len(imgs))).Round(time.Microsecond))
}

// The whole score through the VPU, the tail included, against the cores' (BenchmarkScore).
func BenchmarkDeviceVPUScore(b *testing.B) {
	if !vpu.Available() {
		b.Skip("no " + vpu.Path)
	}
	_, q := loadQuant(b)
	tr, err := openVPUTrunk(q)
	if err != nil {
		b.Fatal(err)
	}
	defer tr.close()
	g := testImages(b)[0]
	out := make([]uint8, q.trunkOut())
	cpu := cpuTime()
	for b.Loop() {
		if err := tr.run(g, out); err != nil {
			b.Fatal(err)
		}
		q.tail(out)
	}
	b.ReportMetric(float64(cpuTime()-cpu)/float64(b.N), "cpu-ns/op")
}

// The same on the cores, with the CPU time each takes.
func BenchmarkDeviceCPUScore(b *testing.B) {
	d, err := load()
	if err != nil {
		b.Fatal(err)
	}
	d = cpuOnly(d)
	g := testImages(b)[0]
	cpu := cpuTime()
	for b.Loop() {
		if _, err := d.score(g); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(cpuTime()-cpu)/float64(b.N), "cpu-ns/op")
}

// cpuTime is the process's CPU time so far, user and system.
func cpuTime() time.Duration {
	var r syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &r)
	return time.Duration(r.Utime.Nano() + r.Stime.Nano())
}

// The camera's pictures as the model gets them (Luma, 96x96), one every `every` for PERSON_CROPS_FOR
// (a duration), written back to back to PERSON_CROPS_OUT, for TestDeviceVPU's PERSON_IMAGES. With
// echod held: the camera is the test's. Scratch only: nothing of it is to be kept.
func TestDeviceCameraCrops(t *testing.T) {
	out, d := os.Getenv("PERSON_CROPS_OUT"), os.Getenv("PERSON_CROPS_FOR")
	if out == "" || d == "" {
		t.Skip("PERSON_CROPS_OUT, PERSON_CROPS_FOR not set")
	}
	dur, err := time.ParseDuration(d)
	if err != nil {
		t.Fatal(err)
	}
	release, err := camera.Get().Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	frames := make(chan *camera.Frame, 1)
	stop := camera.Get().Frames.Listen(func(fr *camera.Frame) {
		select {
		case frames <- fr:
		default:
		}
	})
	defer stop()
	f, err := os.Create(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	end, last, n := time.Now().Add(dur), time.Time{}, 0
	for time.Now().Before(end) {
		fr := <-frames
		if time.Since(last) < every-everySlack {
			continue
		}
		last = time.Now()
		if g := fr.Luma(side, side); g != nil {
			f.Write(g)
			n++
		}
	}
	t.Logf("%d pictures", n)
}

// echod's own path, on a Show 8 with the VPU and echod held: the detector as load() makes it, two
// looks a second for PERSON_LOOKS_FOR (default 30s) on the VPU and then on the cores, with the CPU
// time each costs; and the VPU powered down once let go.
func TestDeviceDetectorLooks(t *testing.T) {
	if !vpu.Available() {
		t.Skip("no " + vpu.Path)
	}
	dur := 30 * time.Second
	if v, err := time.ParseDuration(os.Getenv("PERSON_LOOKS_FOR")); err == nil {
		dur = v
	}
	const status = "/sys/devices/platform/soc/soc:vpu@19100000/power/runtime_status"
	power := func() string { b, _ := os.ReadFile(status); return string(b) }
	imgs := testImages(t)
	for _, onVPU := range []bool{true, false} {
		d, err := load()
		if err != nil {
			t.Fatal(err)
		}
		if !onVPU {
			d = cpuOnly(d)
		} else if d.vpu == nil {
			t.Fatal("no VPU path")
		}
		cpu, began, n := cpuTime(), time.Now(), 0
		for time.Since(began) < dur {
			if _, err := d.score(imgs[n%len(imgs)]); err != nil {
				t.Fatal(err)
			}
			n++
			if n == 1 && onVPU {
				t.Logf("VPU while looking: %s", power())
			}
			d.idle(time.Now())
			time.Sleep(every)
		}
		used := cpuTime() - cpu
		if onVPU && (d.vpu == nil || vpuFailed.Load()) {
			t.Fatal("the VPU failed")
		}
		d.close()
		t.Logf("VPU %v: %d looks in %s, %s of CPU time (%.3f%% of a core)", onVPU, n, time.Since(began).Round(time.Second),
			used.Round(time.Millisecond), 100*used.Seconds()/time.Since(began).Seconds())
		if onVPU {
			t.Logf("VPU let go: %s", power())
		}
	}
}

// On a Show 8 with the VPU: the model's convolutions while the camera stream's noise reduction (swnr,
// hardware/vpu's Chroma) runs on the same VPU from another goroutine, as echod does in a dim room:
// each loads its algorithm again in turn, and the convolutions stay byte for byte the CPU's.
func TestDeviceVPUWithStreamNR(t *testing.T) {
	if !vpu.Available() {
		t.Skip("no " + vpu.Path)
	}
	_, q := loadQuant(t)
	tr, err := openVPUTrunk(q)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.close()
	d, err := vpu.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	c, err := d.NewChroma(1456, 1088, nil)
	if err != nil {
		t.Fatal(err)
	}
	stop, done := make(chan struct{}), make(chan int)
	go func() {
		uv := make([]byte, 1456*544)
		n := 0
		for {
			select {
			case <-stop:
				done <- n
				return
			case <-time.After(66 * time.Millisecond):
			}
			if err := c.NV12(uv, 1456, vpu.NRParams{K: 1000, Strength: 600, BlendW: 8, BlendTh: 60}, 1000); err != nil {
				t.Error(err)
			}
			n++
		}
	}()
	out := make([]uint8, q.trunkOut())
	imgs := testImages(t)
	var total time.Duration
	for r := 0; r < 4; r++ {
		for i, g := range imgs {
			began := time.Now()
			if err := tr.run(g, out); err != nil {
				t.Fatal(err)
			}
			total += time.Since(began)
			want := q.cpu(g)
			for k := range want {
				if out[k] != want[k] {
					t.Fatalf("picture %d: byte %d is %d on the VPU, %d on the CPU", i, k, out[k], want[k])
				}
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	close(stop)
	n := <-done
	t.Logf("%d looks byte-exact, %s each on average, alongside %d pictures smoothed", 4*len(imgs), total/time.Duration(4*len(imgs)), n)
}
