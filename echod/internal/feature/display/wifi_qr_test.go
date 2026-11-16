//go:build !dot && !spot

package display

import (
	"context"
	"errors"
	"image"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// standInWifiQR stands in a camera that shows pic, a join that records what it was asked for, and a
// scan short enough for a test.
func standInWifiQR(t *testing.T, pic *image.RGBA) (joined func() []wifi.QR) {
	t.Helper()
	standInScanCamera(t, func() *image.RGBA { return pic })
	join, found, within, cam := wifiJoinQR, qrFoundFor, wifiQRFor, wifiQRCamera
	t.Cleanup(func() { wifiJoinQR, qrFoundFor, wifiQRFor, wifiQRCamera = join, found, within, cam })
	qrFoundFor, wifiQRFor = 10*time.Millisecond, 2*time.Second
	wifiQRCamera = func() bool { return true }
	var mu sync.Mutex
	var asked []wifi.QR
	wifiJoinQR = func(_ context.Context, q wifi.QR) error {
		mu.Lock()
		asked = append(asked, q)
		mu.Unlock()
		return nil
	}
	return func() []wifi.QR { mu.Lock(); defer mu.Unlock(); return append([]wifi.QR(nil), asked...) }
}

// wifiPageDisplay is a display with the Wi-Fi page up, as openWifi leaves it on a device with a camera.
func wifiPageDisplay() *Display {
	d := scanDisplay()
	d.wifiOpen, d.wifi = true, wifiState{camera: true}
	return d
}

// tapScanQR taps the middle of the Wi-Fi page's bar.
func tapScanQR(d *Display) {
	d.wifiTap(d.r.w/2, d.r.h-d.r.wifiDoneBar()/2)
}

func wifiErr(d *Display) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.wifi.err
}

// Scan QR code on the Wi-Fi page reads the code held up, names the network on the page without the
// passphrase, and joins it, hidden or not, with nothing typed.
func TestTheWifiPageJoinsTheNetworkOfAQRCode(t *testing.T) {
	const code = `WIFI:T:WPA;S:Attic\;Flat;P:correct horse;H:true;;`
	joined := standInWifiQR(t, qrPictureOf(t, code))
	d := wifiPageDisplay()

	tapScanQR(d)
	var said string
	waitFor(t, func() bool {
		if v := d.qrScanView(time.Now()); v != nil && v.code != "" {
			said = v.said
			return true
		}
		return false
	})
	if !strings.Contains(said, "Attic;Flat") || strings.Contains(said, "correct horse") {
		t.Errorf("the page said %q: want the network's name and not its passphrase", said)
	}
	waitFor(t, func() bool { return len(joined()) == 1 })
	want := wifi.QR{SSID: "Attic;Flat", Passphrase: "correct horse", Hidden: true}
	if got := joined()[0]; got != want {
		t.Errorf("joined %+v, want %+v", got, want)
	}
	waitFor(t, func() bool { return !d.qrScanUp() })
}

// A code that is not a Wi-Fi code does not end the scan, and nothing is joined or said of it.
func TestTheWifiScanLooksPastOtherCodes(t *testing.T) {
	joined := standInWifiQR(t, qrPictureOf(t, "https://example.com/"))
	wifiQRFor = 300 * time.Millisecond
	d := wifiPageDisplay()

	tapScanQR(d)
	waitFor(t, d.qrScanUp)
	time.Sleep(150 * time.Millisecond)
	if v := d.qrScanView(time.Now()); v == nil || v.code != "" {
		t.Fatalf("the scan ended or took another code: %+v", v)
	}
	waitFor(t, func() bool { return !d.qrScanUp() })
	if len(joined()) != 0 || wifiErr(d) != "" {
		t.Errorf("joined %v, said %q: want nothing", joined(), wifiErr(d))
	}
}

// A Wi-Fi code for a network the device will not join ends the scan with why, said on the page too,
// and nothing is joined.
func TestTheWifiScanRefusesWhatItCannotJoin(t *testing.T) {
	for code, want := range map[string]string{
		"WIFI:T:SAE;S:Home;P:hunter2hunter;;":               tr("wifi_qr_wpa3"),
		"WIFI:T:WPA2-EAP;S:Campus;E:PEAP;I:me;P:secret12;;": tr("wifi_qr_enterprise"),
		"WIFI:T:WEP;S:Old;P:abcde;;":                        tr("wifi_qr_wep"),
		"WIFI:T:WPA;S:Home;;":                               tr("wifi_qr_invalid"),
	} {
		t.Run(code, func(t *testing.T) {
			joined := standInWifiQR(t, qrPictureOf(t, code))
			d := wifiPageDisplay()
			tapScanQR(d)
			waitFor(t, func() bool { return wifiErr(d) != "" })
			if got := wifiErr(d); got != want {
				t.Errorf("said %q, want %q", got, want)
			}
			if len(joined()) != 0 {
				t.Errorf("joined %v, want nothing", joined())
			}
		})
	}
}

// Without a camera the bar is Rescan and Done, halves, as before; with one, Scan QR code is its middle.
func TestTheWifiBarHasScanQROnlyWithACamera(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	y := r.h - r.wifiDoneBar()/2
	for _, c := range []struct {
		camera bool
		x      int
		want   wifiHit
	}{
		{false, r.w / 4, wifiHit{row: -1, rescan: true}},
		{false, r.w/2 - 1, wifiHit{row: -1, rescan: true}},
		{false, r.w / 2, wifiHit{row: -1, done: true}},
		{false, r.w - 1, wifiHit{row: -1, done: true}},
		{true, r.w / 6, wifiHit{row: -1, rescan: true}},
		{true, r.w / 2, wifiHit{row: -1, qr: true}},
		{true, r.w - 1, wifiHit{row: -1, done: true}},
	} {
		if got := r.wifiListHit(c.x, y, c.camera); got != c.want {
			t.Errorf("camera=%v x=%d: %+v, want %+v", c.camera, c.x, got, c.want)
		}
	}
}

// First run's Wi-Fi step offers the scan on a device with a camera, and it opens the Wi-Fi page with
// the camera's picture over it.
func TestFirstRunOffersToScanAWifiQRCode(t *testing.T) {
	if !hasFirstRunStep(frWifi) {
		t.Skip("a Panel's tablet has the Wi-Fi: no Wi-Fi step")
	}
	joined := standInWifiQR(t, qrPictureOf(t, "WIFI:T:WPA;S:Home;P:hunter2hunter;;"))
	d, _ := firstRunDisplay(t, false, false)
	setStep(t, frWifi)

	var acts []string
	for _, b := range firstRunButtons(d.firstRunView(time.Now())) {
		acts = append(acts, b.act)
	}
	if strings.Join(acts, " ") != "next wifiqr wifi" {
		t.Fatalf("buttons %v, want [next wifiqr wifi]", acts)
	}
	tapButton(t, d, "wifiqr")
	d.mu.Lock()
	open := d.wifiOpen
	d.mu.Unlock()
	if !open || d.firstRunActive() {
		t.Fatal("Scan QR code did not open the Wi-Fi page")
	}
	waitFor(t, func() bool { return len(joined()) == 1 })
	if got := joined()[0]; got.SSID != "Home" || got.Passphrase != "hunter2hunter" {
		t.Errorf("joined %+v", got)
	}
	d.closeWifi()

	wifiQRCamera = func() bool { return false }
	acts = nil
	for _, b := range firstRunButtons(d.firstRunView(time.Now())) {
		acts = append(acts, b.act)
	}
	if strings.Join(acts, " ") != "next wifi" {
		t.Errorf("without a camera: buttons %v, want [next wifi]", acts)
	}
}

// With the shutter closed, the Wi-Fi scan's page says so and waits; once it is opened the camera is
// taken and the code read.
func TestTheWifiScanWaitsForTheShutter(t *testing.T) {
	joined := standInWifiQR(t, qrPictureOf(t, "WIFI:T:WPA;S:Home;P:hunter2hunter;;"))
	acquire, covered, muted, every := qrScanAcquire, qrCovered, qrMuted, qrBlockedEvery
	t.Cleanup(func() { qrScanAcquire, qrCovered, qrMuted, qrBlockedEvery = acquire, covered, muted, every })
	var mu sync.Mutex
	shut := true
	qrCovered = func() bool { mu.Lock(); defer mu.Unlock(); return shut }
	qrMuted = func() bool { return false }
	qrBlockedEvery = 5 * time.Millisecond
	qrScanAcquire = func() (func(), error) {
		if qrCovered() {
			return nil, errors.New("the lens cover is closed")
		}
		return acquire()
	}
	d := wifiPageDisplay()

	tapScanQR(d)
	waitFor(t, func() bool { v := d.qrScanView(time.Now()); return v != nil && v.blocked == "cover" })
	time.Sleep(30 * time.Millisecond)
	if len(joined()) != 0 || wifiErr(d) != "" || !d.qrScanUp() {
		t.Fatalf("with the shutter closed: joined %v, said %q, up %v; want the page waiting", joined(), wifiErr(d), d.qrScanUp())
	}
	mu.Lock()
	shut = false
	mu.Unlock()
	waitFor(t, func() bool { return len(joined()) == 1 })
}

// The scan opened from the Wi-Fi page is drawn over it, waiting or reading; the page is drawn as
// before once it is down.
func TestTheWifiScanIsDrawnOverTheWifiPage(t *testing.T) {
	at := time.Date(2026, 11, 11, 18, 30, 0, 0, time.Local)
	draw := func(s scene) *image.RGBA {
		img := image.NewRGBA(image.Rect(0, 0, showWide, showHigh))
		newRenderer(img).draw(s)
		return img
	}
	wifiOnly := draw(scene{now: at, phase: "idle", showWifi: true, wifi: wifiState{camera: true}})
	for name, v := range map[string]qrScanView{
		"waiting": {title: "Scan", left: 50 * time.Second, blocked: "cover"},
		"reading": {title: "Scan", left: 50 * time.Second, started: true, w: 1280, h: 720, preview: qrPictureOf(t, "x")},
	} {
		both := draw(scene{now: at, phase: "idle", showWifi: true, wifi: wifiState{camera: true}, qrscan: &v})
		alone := draw(scene{now: at, phase: "idle", qrscan: &v})
		if string(both.Pix) == string(wifiOnly.Pix) {
			t.Errorf("%s: the Wi-Fi page hides the scan", name)
		}
		if string(both.Pix) != string(alone.Pix) {
			t.Errorf("%s: the scan over the Wi-Fi page is not the scan page", name)
		}
	}
}

// A second tap on Scan QR code while the page comes up is not taken as Cancel, which is where the
// button was; a tap after that is.
func TestCancelWaitsAMomentAfterTheScanOpens(t *testing.T) {
	standInWifiQR(t, image.NewRGBA(image.Rect(0, 0, 1280, 720)))
	qrCancelAfter = 200 * time.Millisecond
	d := wifiPageDisplay()
	tapScanQR(d)
	waitFor(t, d.qrScanUp)
	c := d.r.qrCancelRect()
	d.qrScanTap(c.Min.X+c.Dx()/2, c.Min.Y+c.Dy()/2)
	time.Sleep(20 * time.Millisecond)
	if !d.qrScanUp() {
		t.Fatal("a tap just after the page opened closed it")
	}
	time.Sleep(250 * time.Millisecond)
	d.qrScanTap(c.Min.X+c.Dx()/2, c.Min.Y+c.Dy()/2)
	waitFor(t, func() bool { return !d.qrScanUp() })
}
