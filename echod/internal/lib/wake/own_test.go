package wake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/detect/assets"
)

// realModel is a microWakeWord model the engine loads: the stop word the daemon carries.
func realModel(t *testing.T) []byte {
	t.Helper()
	path, err := assets.Stop(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const goodManifest = `{"type":"micro","wake_word":"Hey Computer","author":"someone","model":"../../outside.tflite",
 "trained_languages":["en","en-GB"],"version":2,
 "micro":{"probability_cutoff":0.97,"sliding_window_size":5,"feature_step_size":10,"tensor_arena_size":30000}}`

// A model sent from the setup page is kept under an id made from its phrase, beside the others, and
// is offered with them at once. The name its manifest gives the model is not used for anything.
func TestAWakeWordFromTheSetupPage(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "models")
	l := NewLibrary(dir)

	m, err := l.AddOwn([]byte(goodManifest), realModel(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "hey_computer" || m.Phrase != "Hey Computer" || !m.Uploaded || m.Kind != KindMicroWakeWord {
		t.Errorf("kept as %+v", m)
	}
	if m.Path != filepath.Join(dir, "hey_computer.tflite") {
		t.Errorf("the model is at %s", m.Path)
	}
	if _, err := os.Stat(filepath.Join(root, "outside.tflite")); err == nil {
		t.Error("the manifest's own model name was used as a path")
	}

	var man map[string]any
	b, _ := os.ReadFile(filepath.Join(dir, "hey_computer.json"))
	if err := json.Unmarshal(b, &man); err != nil || man["techo5_uploaded"] != true || man["model"] != "hey_computer.tflite" {
		t.Errorf("manifest written as %s (%v)", b, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("the model directory holds %d files, want the model and its manifest", len(entries))
	}

	words, _ := l.Advertise()
	if len(words) != 1 || words[0].ID != "hey_computer" || words[0].Phrase != "Hey Computer" {
		t.Errorf("advertised %+v", words)
	}

	// The same phrase again replaces it; a purge of the cache leaves it, since nothing can be
	// downloaded to put it back.
	if _, err := l.AddOwn([]byte(goodManifest), realModel(t), nil); err != nil {
		t.Errorf("sending it again: %v", err)
	}
	if gone, _ := l.Purge(nil); gone != 0 {
		t.Errorf("a purge deleted %d models sent from the page", gone)
	}

	if err := l.RemoveOwn("hey_computer", nil); err != nil {
		t.Fatal(err)
	}
	if len(l.Ours()) != 0 {
		t.Errorf("still has %+v", l.Ours())
	}
	entries, _ = os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("left %d files behind", len(entries))
	}
}

// A .json that says the model is a stop word keeps saying so in the manifest the page writes.
func TestAStopWordFromTheSetupPage(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "models")
	l := NewLibrary(dir)
	m, err := l.AddOwn([]byte(`{"type":"micro","wake_word":"Stopp","trained_languages":["de"],"stop_word":true,"micro":{}}`), realModel(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !m.StopWord {
		t.Errorf("kept as %+v", m)
	}
	if got := load(dir, m.ID); !got.StopWord || got.Languages[0] != "de" {
		t.Errorf("read back as %+v", got)
	}
	m, err = l.AddOwn([]byte(goodManifest), realModel(t), nil)
	if err != nil || m.StopWord {
		t.Errorf("a wake word kept as %+v (%v)", m, err)
	}
}

// What the engine could not use, or that is not a microWakeWord model at all, is refused with a
// reason, and nothing is left on disk.
func TestAWakeWordThatWillNotRunIsRefused(t *testing.T) {
	model := realModel(t)
	withMicro := func(m string) string {
		return `{"type":"micro","wake_word":"Hey Computer","micro":` + m + `}`
	}
	for what, c := range map[string]struct {
		manifest string
		model    []byte
		says     string
	}{
		"not JSON":              {"wake_word: hey", model, "could not be read"},
		"no type":               {`{"wake_word":"Hey","micro":{}}`, model, `"type": "micro"`},
		"an openWakeWord type":  {`{"type":"openWakeWord","wake_word":"Hey","micro":{}}`, model, `"type": "micro"`},
		"no micro section":      {`{"type":"micro","wake_word":"Hey"}`, model, `"micro"`},
		"no phrase":             {`{"type":"micro","wake_word":"  ","micro":{}}`, model, "wake_word"},
		"a control character":   {`{"type":"micro","wake_word":"Hey\u0007there","micro":{}}`, model, "control"},
		"a direction override":  {`{"type":"micro","wake_word":"Hey \u202eretupmoc","micro":{}}`, model, "text-direction"},
		"a zero-width space":    {`{"type":"micro","wake_word":"Hey\u200bComputer","micro":{}}`, model, "invisible"},
		"a long phrase":         {`{"type":"micro","wake_word":"` + strings.Repeat("a", 61) + `","micro":{}}`, model, "at most"},
		"a bad language":        {`{"type":"micro","wake_word":"Hey","trained_languages":["en; rm -rf"],"micro":{}}`, model, "language tag"},
		"a step it cannot run":  {withMicro(`{"feature_step_size":15}`), model, "feature_step_size"},
		"a window out of range": {withMicro(`{"sliding_window_size":1000}`), model, "sliding_window_size"},
		"a manifest too big":    {withMicro(`{}`) + strings.Repeat(" ", MaxOwnManifest), model, "bigger"},
		"a model too big":       {withMicro(`{}`), append(bytes.Clone(model), make([]byte, MaxOwnModel)...), "bigger"},
		"not a model":           {withMicro(`{}`), []byte("this is a note, not a model, honestly"), "not a TensorFlow Lite"},
		"a model that lies":     {withMicro(`{}`), badInputModel(), "microWakeWord"},
		"a truncated model":     {withMicro(`{}`), model[:200], ""},
	} {
		dir := filepath.Join(t.TempDir(), "models")
		l := NewLibrary(dir)
		_, err := l.AddOwn([]byte(c.manifest), c.model, nil)
		if err == nil {
			t.Errorf("%s was kept", what)
			continue
		}
		if !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", what, err)
		}
		if entries, _ := os.ReadDir(dir); len(entries) != 0 {
			t.Errorf("%s left %d files behind", what, len(entries))
		}
	}
}

// The page never overwrites or removes a model it did not send: the image's own, Home Assistant's, or
// anything named by a path.
func TestOnlyWhatThePageSentIsThePagesToChange(t *testing.T) {
	dir := t.TempDir()
	put(t, dir, "okay_nabu", `{"wake_word":"Okay Nabu"}`, 100)
	l := NewLibrary(dir)

	_, err := l.AddOwn([]byte(`{"type":"micro","wake_word":"Okay, Nabu!","micro":{}}`), realModel(t), nil)
	if err == nil || !strings.Contains(err.Error(), "did not come from this page") {
		t.Errorf("a phrase that would replace the image's model: %v", err)
	}
	for _, id := range []string{"okay_nabu", "", "..", "../okay_nabu", "x/../../okay_nabu", `..\okay_nabu`, "nothing_here"} {
		if err := l.RemoveOwn(id, nil); err == nil {
			t.Errorf("%q was removed", id)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "okay_nabu.tflite")); err != nil {
		t.Errorf("the image's model is gone: %v", err)
	}

	// A phrase that looks like a path is only ever a name.
	m, err := l.AddOwn([]byte(`{"type":"micro","wake_word":"../../etc/passwd","micro":{}}`), realModel(t), nil)
	if err != nil || m.ID != "etc_passwd" || filepath.Dir(m.Path) != dir {
		t.Errorf("kept as %q at %s (%v)", m.ID, m.Path, err)
	}
}

// Home Assistant's config cannot make a model it hands over look like one the page sent, which would
// keep it from the cache purge and let the page delete it.
func TestAnAdoptedModelIsNeverThePages(t *testing.T) {
	dir := t.TempDir()
	_, offer := served(t, `{"type":"micro","wake_word":"x","model":"test_model.tflite","techo5_uploaded":true,"micro":{}}`, []byte("model"))
	m, err := Adopt(context.Background(), dir, offer)
	if err != nil {
		t.Fatal(err)
	}
	if m.Uploaded {
		t.Error("an adopted model was taken as sent from the page")
	}
}

func TestThePageKeepsOnlySoMany(t *testing.T) {
	dir := t.TempDir()
	for i := range MaxOwnModels {
		put(t, dir, fmt.Sprintf("word_%d", i), `{"wake_word":"Word","techo5_uploaded":true}`, 10)
	}
	l := NewLibrary(dir)
	if _, err := l.AddOwn([]byte(goodManifest), realModel(t), nil); err == nil || !strings.Contains(err.Error(), "remove one first") {
		t.Errorf("one more than the most: %v", err)
	}
}

func TestOwnIDIsAPlainName(t *testing.T) {
	for phrase, want := range map[string]string{
		"Hey Jarvis":                "hey_jarvis",
		"  Okay,   Computer!  ":     "okay_computer",
		"../../etc/passwd":          "etc_passwd",
		`C:\Windows`:                "c_windows",
		"Ça va":                     "ca_va",
		"Hé, Jarvis":                "he_jarvis",
		"Straße Øst":                "strasse_ost",
		"Ｈｅｙ ﬁx":                    "hey_fix",
		"Łódź":                      "lodz",
		strings.Repeat("abcd ", 10): "abcd_abcd_abcd_abcd_abcd_abcd_abcd_abcd",
	} {
		if got := OwnID(phrase); got != want {
			t.Errorf("%q: %q, want %q", phrase, got, want)
		}
	}
	// A phrase with no Latin letters still gets a name, and always the same one.
	a, b := OwnID("안녕 컴퓨터"), OwnID("안녕 컴퓨터")
	if a != b || !strings.HasPrefix(a, "own_") || strings.ContainsAny(a, `/\.:`) {
		t.Errorf("%q, %q", a, b)
	}
}

// What an upload a power cut stopped left in the model directory is deleted as the library is made,
// which is at the daemon's start, and the models beside it are not touched.
func TestAnUnfinishedUploadIsSweptAtStart(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{".upload-123", ".upload-456", "okay_nabu.tflite", "okay_nabu.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	NewLibrary(dir)
	entries, _ := os.ReadDir(dir)
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if strings.Join(left, " ") != "okay_nabu.json okay_nabu.tflite" {
		t.Errorf("left %v", left)
	}
}

// A model a slot is listening for is not removed, whether the slot has it as saved or a selection that
// asks for it is under way: the check and the delete are one step under the library's lock, which a
// selection takes too, so neither can come between them.
func TestAModelBeingListenedForIsNotRemoved(t *testing.T) {
	l := NewLibrary(t.TempDir())
	if _, err := l.AddOwn([]byte(goodManifest), realModel(t), nil); err != nil {
		t.Fatal(err)
	}

	saved := func(id string) bool { return id == "hey_computer" }
	if err := l.RemoveOwn("hey_computer", saved); !errors.Is(err, ErrListening) {
		t.Errorf("removed while a slot listens for it: %v", err)
	}

	l.Ensure([]string{"okay_nabu", "hey_computer"})
	if err := l.RemoveOwn("hey_computer", nil); !errors.Is(err, ErrListening) {
		t.Errorf("removed while a selection asks for it: %v", err)
	}

	l.Ensure([]string{"okay_nabu"})
	if err := l.RemoveOwn("hey_computer", nil); err != nil {
		t.Errorf("once nothing asks for it: %v", err)
	}
}

// A model a selection asked for and the engine refused is in no slot, so once the selection is over it
// can be removed and sent again. It used to count as in use until the next selection.
func TestAModelTheEngineRefusedIsNotInUse(t *testing.T) {
	l := NewLibrary(t.TempDir())
	if _, err := l.AddOwn([]byte(goodManifest), realModel(t), nil); err != nil {
		t.Fatal(err)
	}

	l.Ensure([]string{"okay_nabu", "hey_computer"})
	l.Loaded([]string{"okay_nabu"})
	if _, err := l.AddOwn([]byte(goodManifest), realModel(t), nil); err != nil {
		t.Errorf("refused by the engine, it cannot be sent again: %v", err)
	}
	if err := l.RemoveOwn("hey_computer", nil); err != nil {
		t.Errorf("refused by the engine, it cannot be removed: %v", err)
	}

	// One the engine took stays in use.
	if _, err := l.AddOwn([]byte(goodManifest), realModel(t), nil); err != nil {
		t.Fatal(err)
	}
	l.Ensure([]string{"hey_computer"})
	l.Loaded([]string{"hey_computer"})
	if err := l.RemoveOwn("hey_computer", nil); !errors.Is(err, ErrListening) {
		t.Errorf("removed while the engine has it loaded: %v", err)
	}
}

// Sending the same phrase again replaces the model, except while a slot is listening for it: the engine
// has the old one loaded, and would go on running it under the new one's name.
func TestAModelBeingListenedForIsNotReplaced(t *testing.T) {
	l := NewLibrary(t.TempDir())
	if _, err := l.AddOwn([]byte(goodManifest), realModel(t), nil); err != nil {
		t.Fatal(err)
	}
	saved := func(id string) bool { return id == "hey_computer" }
	if _, err := l.AddOwn([]byte(goodManifest), realModel(t), saved); err == nil || !strings.Contains(err.Error(), "listening") {
		t.Errorf("replaced while a slot listens for it: %v", err)
	}
	// A new phrase is not a replacement, so a slot listening for something else does not matter.
	other := strings.Replace(goodManifest, "Hey Computer", "Hey Device", 1)
	if _, err := l.AddOwn([]byte(other), realModel(t), saved); err != nil {
		t.Errorf("a new phrase: %v", err)
	}
}

// The model goes in before its manifest, and a replacement whose manifest will not go in puts the
// model before it back: what was sent before stays as it was, model and manifest.
func TestAFailedReplacementLeavesTheOneBefore(t *testing.T) {
	dir := t.TempDir()
	l := NewLibrary(dir)
	first := realModel(t)
	if _, err := l.AddOwn([]byte(goodManifest), first, nil); err != nil {
		t.Fatal(err)
	}
	manifestWas, _ := os.ReadFile(filepath.Join(dir, "hey_computer.json"))

	var order []string
	was := rename
	rename = func(from, to string) error {
		order = append(order, filepath.Ext(to))
		if filepath.Ext(to) == ".json" {
			return errors.New("disk full")
		}
		return was(from, to)
	}
	t.Cleanup(func() { rename = was })

	second := append(bytes.Clone(first), make([]byte, 64)...) // still a model, a different file
	changed := strings.Replace(goodManifest, `"sliding_window_size":5`, `"sliding_window_size":7`, 1)
	if _, err := l.AddOwn([]byte(changed), second, nil); err == nil {
		t.Fatal("a replacement whose manifest did not go in was reported kept")
	}
	if strings.Join(order, " ") != ".tflite .json" {
		t.Errorf("moved into place in the order %v, want the model first", order)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "hey_computer.tflite")); !bytes.Equal(b, first) {
		t.Error("the model sent before was not put back")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "hey_computer.json")); !bytes.Equal(b, manifestWas) {
		t.Error("the manifest sent before changed")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("the directory holds %d files, want the model and its manifest", len(entries))
	}

	// A new model whose manifest does not go in leaves nothing behind at all.
	other := strings.Replace(goodManifest, "Hey Computer", "Hey Device", 1)
	if _, err := l.AddOwn([]byte(other), first, nil); err == nil {
		t.Fatal("a model whose manifest did not go in was reported kept")
	}
	if _, err := os.Stat(filepath.Join(dir, "hey_device.tflite")); err == nil {
		t.Error("a model with no manifest was left behind")
	}
}
