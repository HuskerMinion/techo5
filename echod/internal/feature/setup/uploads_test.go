package setup

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/buttons"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wake"
)

// letInPage is a page open with one browser let in, and its cookie.
func letInPage(t *testing.T) (*Feature, *http.Cookie) {
	t.Helper()
	f := build()
	f.Open()
	c := ask(t, f)
	f.button(buttons.Event{Name: buttons.Action, Kind: buttons.Tap})
	return f, c
}

// multipartPost is the page script's request: its fields in the order given, a file for any whose
// name is in files, sent under filename.
func multipartPost(path string, fields [][2]string, files map[string]bool, filename string, c *http.Cookie) *http.Request {
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	for _, kv := range fields {
		if files[kv[0]] {
			part, _ := mw.CreateFormFile(kv[0], filename)
			_, _ = part.Write([]byte(kv[1]))
			continue
		}
		_ = mw.WriteField(kv[0], kv[1])
	}
	_ = mw.Close()
	r := httptest.NewRequest(http.MethodPost, path, &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	if c != nil {
		r.AddCookie(c)
	}
	return r
}

// wav is a 16-bit WAVE file of ms of a steady level.
func wav(rate, channels, ms int) string {
	frames := rate * ms / 1000
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+frames*channels*2))
	b.WriteString("WAVEfmt ")
	for _, v := range []any{uint32(16), uint16(1), uint16(channels), uint32(rate), uint32(rate * channels * 2), uint16(channels * 2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(frames*channels*2))
	b.Write(bytes.Repeat([]byte{0x40, 0x1f}, frames*channels))
	return b.String()
}

// refused is a row's upload sent back to the tab with the reason, which says this when it is not "".
func refused(w *httptest.ResponseRecorder, says string) bool {
	loc, err := url.Parse(w.Header().Get("Location"))
	if w.Code != http.StatusSeeOther || err != nil {
		return false
	}
	q := loc.Query()
	return q.Get("tab") == "sound" && q.Get("problem") != "" && strings.Contains(q.Get("problem"), says)
}

// ownSoundsHere points the sounds folder at the test's own.
func ownSoundsHere(t *testing.T) string {
	t.Helper()
	was := speaker.SoundsDir
	speaker.SoundsDir = t.TempDir()
	t.Cleanup(func() { speaker.SoundsDir = was })
	return speaker.SoundsDir
}

// A sound is kept only from a browser let in, with the page's token ahead of it, for a sound on the
// list, and only when the device can play it; the name the browser gave the file is never a path.
func TestASoundIsSentFromThePage(t *testing.T) {
	dir := ownSoundsHere(t)
	f, c := letInPage(t)
	send := func(r *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		f.serve(w, r)
		return w
	}
	files := map[string]bool{"file": true}
	up := func(token, sound, body string, c *http.Cookie) *http.Request {
		var fields [][2]string
		if token != "" {
			fields = append(fields, [2]string{"token", token})
		}
		fields = append(fields, [2]string{"sound", sound}, [2]string{"file", body})
		return multipartPost("/setup/sound", fields, files, "../../../etc/evil.wav", c)
	}
	good := wav(48000, 2, 200)

	for what, w := range map[string]*httptest.ResponseRecorder{
		"without the cookie": send(up(c.Value, "failure", good, nil)),
		"with another token": send(up("wrong", "failure", good, c)),
		"with no token":      send(up("", "failure", good, c)),
	} {
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", what, w.Code)
		}
	}
	for _, name := range []string{"../failure", "failure.wav", "/tmp/x", "", "alarm_clock"} {
		if w := send(up(c.Value, name, good, c)); !refused(w, "") {
			t.Errorf("sound %q: %d %s", name, w.Code, w.Header().Get("Location"))
		}
	}
	if w := send(up(c.Value, "failure", wav(44100, 2, 200), c)); !refused(w, "48 kHz") {
		t.Errorf("a file at another rate: %d %s", w.Code, w.Header().Get("Location"))
	}
	if w := send(up(c.Value, "failure", good+strings.Repeat("x", speaker.MaxOwnUpload), c)); !refused(w, "") {
		t.Errorf("a file over the limit: %d %s", w.Code, w.Header().Get("Location"))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("kept %d files from requests that were refused", len(entries))
	}

	if w := send(up(c.Value, "failure", good, c)); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/setup?saved=1&tab=sound" {
		t.Fatalf("a good sound: %d %s", w.Code, w.Header().Get("Location"))
	}
	if b, err := os.ReadFile(filepath.Join(dir, "failure.wav")); err != nil || string(b) != good {
		t.Errorf("kept as failure.wav: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "etc")); err == nil {
		t.Error("the browser's file name became a path")
	}

	w := httptest.NewRecorder()
	soundsSection(w, "tok")
	body := w.Body.String()
	for _, want := range []string{"Sounds of your own", `value="failure"`, "/setup/sound", "16-bit, 48 kHz", "Use the device's own", `enctype="multipart/form-data"`, ">Upload</button>"} {
		if !strings.Contains(body, want) {
			t.Errorf("the page has no %q", want)
		}
	}

	if p := removeSound(form(url.Values{"sound": {"../failure"}})); p == "" {
		t.Error("a name not on the list was removed")
	}
	if p := playSound(form(url.Values{"sound": {"nothing"}})); p == "" {
		t.Error("a name not on the list was played")
	}
	if p := removeSound(form(url.Values{"sound": {"failure"}})); p != "" {
		t.Error(p)
	}
	if _, err := os.Stat(filepath.Join(dir, "failure.wav")); err == nil {
		t.Error("the sound is still there after removing it")
	}
}

// ownModels gives the page a wake word library of the test's own, with the image's model in it.
func ownModels(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "okay_nabu.tflite"), []byte("the image's model"), 0o644); err != nil {
		t.Fatal(err)
	}
	lib := wake.NewLibrary(dir)
	was := models
	models = func() *wake.Library { return lib }
	t.Cleanup(func() { models = was })
	return dir
}

func stopModel(t *testing.T) string {
	t.Helper()
	path, err := assets.Stop(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A wake word is kept only from a browser let in, with the page's token ahead of it, once the engine
// can run it; Home Assistant is made to ask again, and only what the page sent can be removed, and
// not while the device is listening for it.
func TestAWakeWordIsSentFromThePage(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	dir := ownModels(t)
	f, c := letInPage(t)
	var reconnects atomic.Int32
	was := whenIdle
	whenIdle = &idleReconnect{busy: func() bool { return false }, emit: func() { reconnects.Add(1) }, look: time.Millisecond}
	t.Cleanup(func() { whenIdle = was })
	// settled waits for the reconnect asked for to have happened.
	settled := func(want int32) bool {
		for range 1000 {
			if reconnects.Load() >= want {
				break
			}
			time.Sleep(time.Millisecond)
		}
		return reconnects.Load() == want
	}
	send := func(r *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		f.serve(w, r)
		return w
	}
	files := map[string]bool{"manifest": true, "model": true}
	up := func(token, manifest, model string, c *http.Cookie) *http.Request {
		var fields [][2]string
		if token != "" {
			fields = append(fields, [2]string{"token", token})
		}
		fields = append(fields, [2]string{"manifest", manifest}, [2]string{"model", model})
		return multipartPost("/setup/wakeword", fields, files, "../../x", c)
	}
	model := stopModel(t)
	manifest := `{"type":"micro","wake_word":"Hey <b>Computer</b>","micro":{"sliding_window_size":5,"feature_step_size":10}}`

	for what, w := range map[string]*httptest.ResponseRecorder{
		"without the cookie": send(up(c.Value, manifest, model, nil)),
		"with another token": send(up("wrong", manifest, model, c)),
		"with no token":      send(up("", manifest, model, c)),
	} {
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: %d", what, w.Code)
		}
	}
	if w := send(up(c.Value, manifest, "not a model", c)); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "TensorFlow Lite") {
		t.Errorf("not a model: %d %q", w.Code, w.Body.String())
	}
	if w := send(up(c.Value, manifest, model+strings.Repeat("x", wake.MaxOwnModel), c)); w.Code != http.StatusBadRequest {
		t.Errorf("a model over the limit: %d", w.Code)
	}
	// Past what the whole request may be, it stops partway through a file, and that is the files being
	// too big, not a file that did not arrive.
	if w := send(up(c.Value, strings.Repeat(" ", 100<<10)+manifest, model+strings.Repeat("x", wake.MaxOwnModel), c)); w.Code != http.StatusRequestEntityTooLarge ||
		!strings.Contains(w.Body.String(), "too big") || !strings.Contains(w.Body.String(), fmt.Sprintf("%d KB", wake.MaxOwnModel>>10)) {
		t.Errorf("a request over the limit, stopped in the .tflite: %d %q", w.Code, w.Body.String())
	}
	if w := send(up(c.Value, strings.Repeat(" ", 2*wake.MaxOwnModel)+manifest, model, c)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("a request over the limit, stopped in the .json: %d %q", w.Code, w.Body.String())
	}
	if w := send(up(c.Value, `{"type":"micro","wake_word":"Okay Nabu","micro":{}}`, model, c)); w.Code != http.StatusBadRequest {
		t.Errorf("a model over the image's own: %d", w.Code)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || !settled(0) {
		t.Fatalf("kept %d files, reconnected %d times, from requests that were refused", len(entries)-1, reconnects.Load())
	}

	w := send(up(c.Value, manifest, model, c))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "hey_b_computer_b") {
		t.Fatalf("a good model: %d %q", w.Code, w.Body.String())
	}
	if !settled(1) {
		t.Errorf("reconnected %d times, want once so Home Assistant sees it", reconnects.Load())
	}
	if _, err := os.Stat(filepath.Join(dir, "hey_b_computer_b.tflite")); err != nil {
		t.Error(err)
	}

	rec := httptest.NewRecorder()
	wakeWordsSection(rec, "tok")
	body := rec.Body.String()
	if strings.Contains(body, "<b>Computer") || !strings.Contains(body, "Hey &lt;b&gt;Computer&lt;/b&gt;") {
		t.Error("the phrase was not escaped")
	}
	if strings.Count(body, ">Remove</button>") != 1 {
		t.Error("Remove is offered for a model the page did not send, or not for the one it did")
	}

	if p := removeWakeWord(form(url.Values{"id": {"okay_nabu"}})); p == "" {
		t.Error("the image's model was removed")
	}
	if p := removeWakeWord(form(url.Values{"id": {"../okay_nabu"}})); p == "" {
		t.Error("a path was removed")
	}
	if err := config.Set().Wake(0).ID("hey_b_computer_b"); err != nil {
		t.Fatal(err)
	}
	if p := removeWakeWord(form(url.Values{"id": {"hey_b_computer_b"}})); !strings.Contains(p, "listening") {
		t.Errorf("removing the one in use: %q", p)
	}
	if err := config.Set().Wake(0).ID("okay_nabu"); err != nil {
		t.Fatal(err)
	}
	if p := removeWakeWord(form(url.Values{"id": {"hey_b_computer_b"}})); p != "" {
		t.Error(p)
	}
	if _, err := os.Stat(filepath.Join(dir, "okay_nabu.tflite")); err != nil {
		t.Errorf("the image's model is gone: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 || !settled(2) {
		t.Errorf("%d files left, %d reconnects", len(entries), reconnects.Load())
	}
}

// A sound stopped partway by the most a request may be is a file too big, not one that did not
// arrive whole.
func TestASoundStoppedAtTheLimitIsTooBig(t *testing.T) {
	ownSoundsHere(t)
	var b bytes.Buffer
	mw := multipart.NewWriter(&b)
	_ = mw.WriteField("sound", "failure")
	part, _ := mw.CreateFormFile("file", "x.wav")
	_, _ = part.Write([]byte(wav(48000, 2, 2000)))
	_ = mw.Close()

	body := http.MaxBytesReader(httptest.NewRecorder(), io.NopCloser(&b), 64<<10)
	problem := receiveSound(multipart.NewReader(body, mw.Boundary()))
	if !strings.Contains(problem, "too big") || !strings.Contains(problem, fmt.Sprintf("%d KB", speaker.MaxOwnUpload>>10)) {
		t.Errorf("problem is %q", problem)
	}
}

// What sound uploads a power cut stopped left behind is swept at the first Run only: Run again, as the
// supervisor does after a failure, would delete an upload the open page is writing.
func TestUnfinishedSoundUploadsAreSweptOnce(t *testing.T) {
	dir := ownSoundsHere(t)
	sweptUploads = sync.Once{}
	t.Cleanup(func() { sweptUploads = sync.Once{} })
	half := filepath.Join(dir, ".upload-1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := build()

	if err := os.WriteFile(half, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(half); !os.IsNotExist(err) {
		t.Errorf("the first Run left what a power cut stopped: %v", err)
	}

	if err := os.WriteFile(half, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(half); err != nil {
		t.Errorf("Run again deleted an upload under way: %v", err)
	}
}
