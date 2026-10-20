//go:build linux && hwdevice && !dot && !spot

package camera

import (
	"math"
	"os"
	"testing"
	"time"
)

// On a 2nd gen Show 5 with the ISP develop only: live frames developed on the ISP and on the cores,
// the time each took and how far apart they are (PSNR per plane). HW_OUT, if set, is a prefix the last
// frame's two I420 pictures are written under (.isp.i420, .cpu.i420), and its raw (.raw).
func TestDeviceISPDevelop(t *testing.T) {
	if boardSensor() != sensorOV02B10 {
		t.Skip("not a 2nd gen Show 5")
	}
	release, err := Get().Acquire()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	frames := make(chan *Frame, 1)
	defer Get().Frames.Listen(func(fr *Frame) {
		select {
		case frames <- fr:
		default:
		}
	})()
	time.Sleep(3 * time.Second) // auto-exposure settles
	isp, cpu := make([]byte, 1600*1200*3/2), make([]byte, 1600*1200*3/2)
	var tISP, tCPU time.Duration
	var fr *Frame
	const n = 10
	for i := 0; i < n; i++ {
		fr = <-frames
		began := time.Now()
		if _, _, ok := fr.ISPI420(isp); !ok {
			t.Fatal("the ISP did not develop the frame")
		}
		tISP += time.Since(began)
		began = time.Now()
		fr.I420(cpu)
		tCPU += time.Since(began)
	}
	psnr := func(a, b []byte) float64 {
		var se float64
		for i := range a {
			d := float64(a[i]) - float64(b[i])
			se += d * d
		}
		return 10 * math.Log10(255*255*float64(len(a))/se)
	}
	const ys, cs = 1600 * 1200, 1600 * 1200 / 4
	fr.mu.Lock()
	tn := fr.level()
	fr.mu.Unlock()
	t.Logf("tone %+v", tn)
	t.Logf("ISP %.1f ms a frame (the call, waiting for a blanking included), cores %.1f ms", tISP.Seconds()*1000/n, tCPU.Seconds()*1000/n)
	t.Logf("ISP against the cores: Y %.2f dB, Cb %.2f dB, Cr %.2f dB", psnr(isp[:ys], cpu[:ys]),
		psnr(isp[ys:ys+cs], cpu[ys:ys+cs]), psnr(isp[ys+cs:], cpu[ys+cs:]))
	if p := os.Getenv("HW_OUT"); p != "" {
		os.WriteFile(p+".isp.i420", isp, 0o644)
		os.WriteFile(p+".cpu.i420", cpu, 0o644)
		os.WriteFile(p+".raw", fr.raw, 0o644)
	}
}
