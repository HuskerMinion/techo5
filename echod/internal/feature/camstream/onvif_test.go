package camstream

import (
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// nonceSeq numbers the tests' logins.
var nonceSeq atomic.Int64

// token is a WS-Security UsernameToken header, a digest of pass made at created; text sends it as is.
func token(user, pass string, created time.Time, text bool) string {
	nonce := []byte(fmt.Sprintf("nonce-%010d", nonceSeq.Add(1))) // a login of its own each time
	c := created.UTC().Format(time.RFC3339)
	kind, value := "#PasswordDigest", ""
	if text {
		kind, value = "#PasswordText", pass
	} else {
		sum := sha1.Sum(append(append(append([]byte(nil), nonce...), c...), pass...))
		value = base64.StdEncoding.EncodeToString(sum[:])
	}
	return `<s:Header><Security xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd">
<UsernameToken><Username>` + user + `</Username>
<Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0` + kind + `">` + value + `</Password>
<Nonce>` + base64.StdEncoding.EncodeToString(nonce) + `</Nonce>
<Created xmlns="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd">` + c + `</Created>
</UsernameToken></Security></s:Header>`
}

func soap(t *testing.T, f *Feature, from, header, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://192.0.2.10:8181"+mediaPath, strings.NewReader(
		`<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" `+
			`xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tds="http://www.onvif.org/ver10/device/wsdl">`+
			header+`<s:Body>`+body+`</s:Body></s:Envelope>`))
	req.RemoteAddr = from + ":40000"
	w := httptest.NewRecorder()
	f.onvif(w, req)
	return w.Code, w.Body.String()
}

func onvifSetup(t *testing.T) *Feature {
	t.Helper()
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if err := config.Set().CamStream().Login("viewer", "secret12"); err != nil {
		t.Fatal(err)
	}
	return build()
}

func TestONVIFStreamURI(t *testing.T) {
	f := onvifSetup(t)
	code, body := soap(t, f, "198.51.100.1", token("viewer", "secret12", time.Now(), false),
		`<trt:GetStreamUri><trt:ProfileToken>main</trt:ProfileToken></trt:GetStreamUri>`)
	if code != 200 || !strings.Contains(body, "<tt:Uri>rtsp://192.0.2.10:8554/camera</tt:Uri>") {
		t.Fatalf("%d %s", code, body)
	}
	code, body = soap(t, f, "198.51.100.1", token("viewer", "secret12", time.Now(), false), `<trt:GetProfiles/>`)
	if code != 200 || !strings.Contains(body, `<trt:Profiles token="main"`) || !strings.Contains(body, "<tt:Encoding>JPEG</tt:Encoding>") {
		t.Fatalf("%d %s", code, body)
	}
}

func TestONVIFAsksFirstWithoutALogin(t *testing.T) {
	f := onvifSetup(t)
	for _, op := range []string{"<tds:GetSystemDateAndTime/>", "<tds:GetCapabilities/>", "<tds:GetServices/>"} {
		if code, body := soap(t, f, "198.51.100.1", "", op); code != 200 || strings.Contains(body, "Fault") {
			t.Errorf("%s: %d %s", op, code, body)
		}
	}
}

func TestONVIFRefusesWrongLogins(t *testing.T) {
	f := onvifSetup(t)
	cases := map[string]string{
		"none":       "",
		"wrong pass": token("viewer", "guess", time.Now(), false),
		"wrong user": token("someone", "secret12", time.Now(), false),
		"plain text": token("viewer", "secret12", time.Now(), true),
		"old":        token("viewer", "secret12", time.Now().Add(-time.Hour), false),
	}
	for name, header := range cases {
		code, body := soap(t, f, "198.51.100.2", header, `<trt:GetStreamUri/>`)
		if code != 400 || !strings.Contains(body, "ter:NotAuthorized") || strings.Contains(body, "rtsp://") {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}
}

func TestONVIFLimitsWrongLogins(t *testing.T) {
	f := onvifSetup(t)
	for range lockAfter {
		soap(t, f, "198.51.100.3", token("viewer", "guess", time.Now(), false), `<trt:GetProfiles/>`)
	}
	_, body := soap(t, f, "198.51.100.3", token("viewer", "secret12", time.Now(), false), `<trt:GetProfiles/>`)
	if !strings.Contains(body, "too many") {
		t.Errorf("the right login let in straight after %d wrong ones: %s", lockAfter, body)
	}
	// Another address is not held to it.
	if code, _ := soap(t, f, "198.51.100.4", token("viewer", "secret12", time.Now(), false), `<trt:GetProfiles/>`); code != 200 {
		t.Errorf("another address refused: %d", code)
	}
}

// Many wrong logins at once are no faster than one after another: at most lockAfter are checked.
func TestONVIFParallelGuessesAreLimited(t *testing.T) {
	f := onvifSetup(t)
	var wg sync.WaitGroup
	var checked atomic.Int32
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, body := soap(t, f, "198.51.100.9", token("viewer", "guess", time.Now(), false), `<trt:GetProfiles/>`)
			if strings.Contains(body, "the login is wrong") {
				checked.Add(1)
			}
		}()
	}
	wg.Wait()
	// Those turned away as busy (maxONVIF) were never checked: the guesser goes on one at a time.
	for range lockAfter {
		if _, body := soap(t, f, "198.51.100.9", token("viewer", "guess", time.Now(), false), `<trt:GetProfiles/>`); strings.Contains(body, "the login is wrong") {
			checked.Add(1)
		}
	}
	if n := checked.Load(); n > lockAfter {
		t.Errorf("%d wrong logins checked", n)
	}
	if !f.guard.refused("198.51.100.9") {
		t.Error("not locked out")
	}
}

// A login is taken once.
func TestONVIFReplayRefused(t *testing.T) {
	f := onvifSetup(t)
	tok := token("viewer", "secret12", time.Now(), false)
	if code, _ := soap(t, f, "198.51.100.10", tok, `<trt:GetProfiles/>`); code != 200 {
		t.Fatalf("first: %d", code)
	}
	if code, body := soap(t, f, "198.51.100.10", tok, `<trt:GetProfiles/>`); code != 400 || !strings.Contains(body, "NotAuthorized") {
		t.Errorf("replayed: %d", code)
	}
}

// The same login written differently (a line break in the nonce, which base64 passes over) is the same
// login.
func TestONVIFReplayRewrittenRefused(t *testing.T) {
	f := onvifSetup(t)
	tok := token("viewer", "secret12", time.Now(), false)
	if code, _ := soap(t, f, "198.51.100.11", tok, `<trt:GetProfiles/>`); code != 200 {
		t.Fatalf("first: %d", code)
	}
	i := strings.Index(tok, "<Nonce>") + len("<Nonce>") + 4
	again := tok[:i] + "\r\n" + tok[i:]
	if code, _ := soap(t, f, "198.51.100.11", again, `<trt:GetProfiles/>`); code != 400 {
		t.Errorf("a replay with a line break in its nonce: %d", code)
	}
}

func TestONVIFOtherwise(t *testing.T) {
	f := onvifSetup(t)
	ok := token("viewer", "secret12", time.Now(), false)
	if code, body := soap(t, f, "198.51.100.5", ok, `<tds:SetSystemFactoryDefault/>`); code != 400 || !strings.Contains(body, "ActionNotSupported") {
		t.Errorf("an operation not offered: %d %s", code, body)
	}
	if code, _ := soap(t, f, "198.51.100.5", ok, `<trt:GetStreamUri><trt:ProfileToken>other</trt:ProfileToken></trt:GetStreamUri>`); code != 400 {
		t.Errorf("another profile: %d", code)
	}
	// The snapshot is the camera page's, offered only while that page is.
	if code, _ := soap(t, f, "198.51.100.5", ok, `<trt:GetSnapshotUri/>`); code != 400 {
		t.Errorf("a snapshot with camera web access off: %d", code)
	}
	if code, _ := soap(t, f, "198.51.100.5", "", `not xml`); code != 400 {
		t.Errorf("not XML: %d", code)
	}
	w := httptest.NewRecorder()
	f.onvif(w, httptest.NewRequest(http.MethodGet, devicePath, nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d", w.Code)
	}
}

// ONVIF says the rate the stream is sent at, not a number of its own.
func TestONVIFStatesTheStreamRate(t *testing.T) {
	want := fmt.Sprintf("<tt:FrameRateLimit>%d</tt:FrameRateLimit>", maxFPS)
	if p := profileXML("trt:Profile"); !strings.Contains(p, want) {
		t.Errorf("profile without %s:\n%s", want, p)
	}
	f := &Feature{}
	body, ok := f.answer("GetVideoSources", "", "192.0.2.10", true)
	if want := fmt.Sprintf("<tt:Framerate>%d</tt:Framerate>", maxFPS); !ok || !strings.Contains(body, want) {
		t.Errorf("video sources without %s:\n%s", want, body)
	}
}
