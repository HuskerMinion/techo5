package wake

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/zserge/microwakeword"
	"golang.org/x/text/unicode/norm"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safename"
	"github.com/HuskerMinion/techo5/echod/internal/lib/tflite"
	"github.com/HuskerMinion/techo5/echod/internal/lib/unseen"
)

// Wake words of the owner's own, sent from the setup page: a microWakeWord model and the .json that
// describes it, kept in the model directory beside the ones the image carries and the ones Home
// Assistant handed over, and offered to Home Assistant with them.
//
// Both files come from a browser, so neither is trusted. The id they are kept under is made here from
// the phrase, never from a file name; the manifest is read for the few fields the engine uses and
// written again rather than kept as sent; and the model has to load and run in the engine before it is
// put where the engine will find it. Only a model sent this way can be removed from the page: the
// image's own and Home Assistant's are not the page's to delete.

const (
	// MaxOwnModel is the most a model sent from the page may be. The published microWakeWord models
	// are 50 to 60 KB; this leaves room for a bigger one trained at home and none for anything else.
	MaxOwnModel = 1 << 20

	// MaxOwnManifest is the most its .json may be. A real one is a few hundred bytes.
	MaxOwnManifest = 16 << 10

	// MaxOwnModels is how many the page keeps. Each is a choice in a list somebody scrolls through,
	// and all of them are on a small disk.
	MaxOwnModels = 20

	// maxPhrase is the longest phrase taken, in characters: what Home Assistant shows in its picker.
	maxPhrase = 60

	// maxID is the longest id made from a phrase.
	maxID = 40

	// featureDims is the width of a microWakeWord model's input, [1, frames, 40]: one feature frame of
	// the audio frontend per row.
	featureDims = 40
)

// ownManifest is what the page reads of a sent .json. The model's own name in it is left out on
// purpose: the file is kept under the id, whatever the manifest called it.
type ownManifest struct {
	Type             string   `json:"type"`
	WakeWord         string   `json:"wake_word"`
	TrainedLanguages []string `json:"trained_languages"`
	Micro            *struct {
		SlidingWindowSize int `json:"sliding_window_size"`
		FeatureStepSize   int `json:"feature_step_size"`
	} `json:"micro"`
}

// CheckOwn reads a manifest and a model sent from the page and reports the id, phrase and languages
// they would be kept as, or what is wrong with them. Nothing is written.
func CheckOwn(manifestJSON, model []byte) (Model, error) {
	if len(manifestJSON) > MaxOwnManifest {
		return Model{}, fmt.Errorf("the .json is bigger than a wake word's description is; the most is %d KB", MaxOwnManifest>>10)
	}
	if len(model) > MaxOwnModel {
		return Model{}, fmt.Errorf("the .tflite is bigger than a microWakeWord model is; the most is %d KB", MaxOwnModel>>10)
	}

	var man ownManifest
	dec := json.NewDecoder(bytes.NewReader(manifestJSON))
	if err := dec.Decode(&man); err != nil {
		return Model{}, fmt.Errorf("the .json could not be read: %v", err)
	}
	if man.Type != "micro" {
		return Model{}, errors.New(`the .json is not a microWakeWord model's: it has to say "type": "micro"`)
	}
	if man.Micro == nil {
		return Model{}, errors.New(`the .json has no "micro" section, which says how the model is run`)
	}

	phrase := strings.TrimSpace(man.WakeWord)
	switch {
	case phrase == "":
		return Model{}, errors.New(`the .json has no "wake_word", the phrase to say`)
	case !utf8.ValidString(phrase) || utf8.RuneCountInString(phrase) > maxPhrase:
		return Model{}, fmt.Errorf("the wake word's phrase has to be at most %d characters", maxPhrase)
	case strings.IndexFunc(phrase, unicode.IsControl) >= 0:
		return Model{}, errors.New("the wake word's phrase has a control character in it")
	case strings.IndexFunc(phrase, unseen.Rune) >= 0:
		// Home Assistant shows the phrase in its picker, where a character that turns the text's
		// direction or takes no room makes it read as something other than what it is.
		return Model{}, errors.New("the wake word's phrase has a text-direction or invisible character in it")
	}

	langs := man.TrainedLanguages
	if len(langs) == 0 {
		langs = []string{"en"}
	}
	if len(langs) > 20 {
		return Model{}, errors.New("the .json lists more languages than a model is trained on")
	}
	for _, l := range langs {
		if !languageTag(l) {
			return Model{}, fmt.Errorf("%q in trained_languages is not a language tag", l)
		}
	}

	step, window := man.Micro.FeatureStepSize, man.Micro.SlidingWindowSize
	if step == 0 {
		step = FeatureStep
	}
	if window == 0 {
		window = WindowSize
	}
	if step != 10 && step != 20 {
		return Model{}, fmt.Errorf("feature_step_size %d is not one the engine runs: 10 or 20", step)
	}
	if window < 1 || window > 100 {
		return Model{}, fmt.Errorf("sliding_window_size %d is out of range: 1 to 100", window)
	}

	if _, err := microInput(model); err != nil {
		return Model{}, err
	}

	return Model{
		ID:        OwnID(phrase),
		Phrase:    phrase,
		Languages: slices.Clone(langs),
		Kind:      KindMicroWakeWord,
		Uploaded:  true,
		Config: microwakeword.Config{
			SlidingWindowSize: window,
			FeaturesStepMs:    step,
		},
	}, nil
}

// The limits of a microWakeWord model, past which a file is refused before the engine reads it. The
// engine (github.com/zserge/microwakeword) allocates every tensor, in both subgraphs, at the size the
// file states, and runs whatever operators the file lists: it was written for the published models,
// not for a file made to make it fall over. The published models have two subgraphs, under 100
// operators, no dimension over a few hundred and well under a megabyte of tensors all told; these
// leave room for a bigger one trained at home and none for a file that would run the daemon out of
// memory, which is fatal rather than a panic anything can recover. The elements and bytes are what
// bound the memory; a single dimension may be long (a trainer's wider window makes one of 4736,
// #267) as long as the tensor stays within them.
const (
	microMaxSubgraphs = 2
	microMaxRank      = 8
	microMaxDim       = 1 << 16
	microMaxElements  = 1 << 20
	microMaxBytes     = 4 << 20
	microMaxOperators = 256

	// MaxMicroFile is the most a microWakeWord model on disk may be, from wherever it came.
	MaxMicroFile = 4 << 20
)

// microOps is every operator the engine runs: every one the published models use, and ADD,
// AVERAGE_POOL_2D and MAX_POOL_2D, which models trained with residual connections or pooling use
// (#190). Anything else the engine would refuse at the first inference; refusing it here says so at
// once.
var microOps = map[tflite.Op]bool{
	tflite.OpCallOnce: true, tflite.OpVarHandle: true, tflite.OpReadVariable: true,
	tflite.OpAssignVariable: true, tflite.OpReshape: true, tflite.OpConcatenation: true,
	tflite.OpStridedSlice: true, tflite.OpConv2D: true, tflite.OpDepthwiseConv: true,
	tflite.OpFullyConnected: true, tflite.OpLogistic: true, tflite.OpQuantize: true,
	tflite.OpSplitV: true, tflite.OpAdd: true, tflite.OpAveragePool2D: true, tflite.OpMaxPool2D: true,
}

// microInput checks that a model reads as a microWakeWord model the engine can be trusted with, and
// reports how many feature frames it takes at a time. The engine's own loader takes the file on
// trust, so everything it would size or index from is checked here before it ever sees the file.
func microInput(model []byte) (frames int, err error) {
	if len(model) < 8 || string(model[4:8]) != "TFL3" {
		return 0, errors.New("the .tflite is not a TensorFlow Lite model")
	}
	m, err := tflite.Parse(model)
	if err != nil {
		return 0, errors.New("the .tflite is damaged: it could not be read")
	}
	if len(m.Subgraphs) == 0 {
		return 0, errors.New("the .tflite has nothing in it to run")
	}
	g := m.Subgraphs[0]
	if len(g.Inputs) != 1 || g.Inputs[0] < 0 || g.Inputs[0] >= len(g.Tensors) || len(g.Outputs) == 0 {
		return 0, errors.New("the .tflite is not a microWakeWord model: its inputs are not one")
	}
	shape := g.Tensors[g.Inputs[0]].Shape
	if len(shape) == 3 && shape[2] == 96 {
		return 0, errors.New("the .tflite is an openWakeWord model; this takes microWakeWord models")
	}
	if len(shape) != 3 || shape[0] != 1 || shape[1] < 1 || shape[1] > 100 || shape[2] != featureDims {
		return 0, fmt.Errorf("the .tflite is not a microWakeWord model: its input is %v, not [1, frames, %d]", shape, featureDims)
	}
	if err := microGraph(m, len(model)); err != nil {
		return 0, fmt.Errorf("the .tflite is not a microWakeWord model the engine can run: %v", err)
	}
	return shape[1], nil
}

// microGraph walks every subgraph, tensor, operator and buffer of a parsed model and holds them to
// the limits above. size is the file's length.
func microGraph(m *tflite.Model, size int) error {
	if len(m.Subgraphs) > microMaxSubgraphs {
		return fmt.Errorf("it has %d subgraphs; the most is %d", len(m.Subgraphs), microMaxSubgraphs)
	}

	var data int
	for i, n := range m.BufferSizes() {
		if n < 0 || n > size-data {
			return fmt.Errorf("buffer %d holds more than the file does", i)
		}
		data += n
	}

	var bytes, ops int
	for gi, g := range m.Subgraphs {
		for i, t := range g.Tensors {
			if len(t.Shape) > microMaxRank {
				return fmt.Errorf("tensor %d has %d dimensions", i, len(t.Shape))
			}
			n := 1
			for _, d := range t.Shape {
				if d < 0 || d > microMaxDim {
					return fmt.Errorf("tensor %d has a dimension of %d; the most is %d", i, d, microMaxDim)
				}
				// Checked before multiplying, so it cannot overflow an int on the 32-bit boards.
				if d > 0 && n > microMaxElements/d {
					return fmt.Errorf("tensor %d has more than %d elements", i, microMaxElements)
				}
				n *= d
				if n > microMaxElements {
					return fmt.Errorf("tensor %d has more than %d elements", i, microMaxElements)
				}
			}
			bytes += max(n, 1) * elemSize(t.Type)
			if bytes > microMaxBytes {
				return fmt.Errorf("its tensors take more than %d MB", microMaxBytes>>20)
			}
		}

		for _, io := range [][]int{g.Inputs, g.Outputs} {
			if err := tensorRefs(io, len(g.Tensors)); err != nil {
				return err
			}
		}
		for i, o := range g.Ops {
			ops++
			if ops > microMaxOperators {
				return fmt.Errorf("it has more than %d operators", microMaxOperators)
			}
			if !microOps[o.Op] {
				// The operation first, by name: its place in the graph (i) means nothing to the
				// person adding the model (#190).
				return fmt.Errorf("it uses %s, which this device's microWakeWord engine does not run "+
					"(operator %d); some models made for ESPHome need it", o.Op, i)
			}
			// CALL_ONCE runs another subgraph to set up the first. Anywhere else, or pointed back at
			// the first, it would call itself without end.
			if o.Op == tflite.OpCallOnce && (gi != 0 || o.InitSubgraph() <= 0 || o.InitSubgraph() >= len(m.Subgraphs)) {
				return fmt.Errorf("operator %d calls subgraph %d from subgraph %d", i, o.InitSubgraph(), gi)
			}
			for _, io := range [][]int{o.Inputs, o.Outputs} {
				if err := tensorRefs(io, len(g.Tensors)); err != nil {
					return fmt.Errorf("operator %d: %v", i, err)
				}
			}
		}
	}
	return nil
}

// tensorRefs checks a list of tensor indices against the table they index; -1 is an input left out.
func tensorRefs(refs []int, n int) error {
	for _, r := range refs {
		if r < -1 || r >= n {
			return fmt.Errorf("tensor %d is not in a table of %d", r, n)
		}
	}
	return nil
}

// elemSize is what the engine allocates per element of a tensor of type t.
func elemSize(t tflite.TensorType) int {
	switch t {
	case tflite.Int64:
		return 8
	case tflite.Float32, tflite.Int32:
		return 4
	case 1, 7: // float16, int16
		return 2
	}
	return 1
}

// CheckMicro holds a microWakeWord model on disk to the same checks a model sent from the setup page
// is held to, wherever it came from: the image, Home Assistant or the page. It runs before the engine
// loads anything, so a model that would take the daemon down is refused rather than loaded.
func CheckMicro(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	model, err := io.ReadAll(io.LimitReader(f, MaxMicroFile+1))
	if err != nil {
		return err
	}
	if len(model) > MaxMicroFile {
		return fmt.Errorf("the .tflite is bigger than %d MB", MaxMicroFile>>20)
	}
	_, err = microInput(model)
	return err
}

// runs loads the model at path in the engine and feeds it one window of silence, which is what the
// engine will do with it a moment after it is chosen. A model the engine cannot load or run is turned
// away now, with a reason, rather than leaving a slot deaf later with only the log to say why.
func runs(path string, cfg microwakeword.Config, frames int) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("the engine could not run the .tflite: %v", r)
		}
	}()
	cfg.ModelPath = path
	det, err := microwakeword.NewDetector(cfg)
	if err != nil {
		return fmt.Errorf("the engine could not load the .tflite: %v", err)
	}
	silence := make([][]int8, frames)
	for i := range silence {
		silence[i] = make([]int8, featureDims)
	}
	det.ProcessFeatures(silence)
	// A model the engine loads but cannot run (an operator's check fails) would score nothing ever after.
	if err := det.Err(); err != nil {
		return fmt.Errorf("the engine could not run the .tflite: %v", err)
	}
	return nil
}

// languageTag is a plausible BCP-47 tag: letters, digits and hyphens, short.
func languageTag(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for _, r := range s {
		if !(r == '-' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return true
}

// OwnID is the id a model with this phrase is kept under: the phrase in lower case, with anything that
// is not a letter or digit as an underscore, as the published models are named ("Hey Jarvis" is
// hey_jarvis). An accented letter is the plain one ("Ça va" is ca_va), so the id is plain ASCII and
// still reads as the phrase. A phrase with no such letters at all, in another script say, is kept as
// own_ and a number made from the phrase, so it still has a name and the same phrase always has the
// same one.
func OwnID(phrase string) string {
	var b strings.Builder
	under := false
	// Decomposed, an accented letter is the plain letter and its accents, which are dropped; NFKD also
	// makes plain letters of ligatures and full-width forms.
	for _, r := range norm.NFKD.String(strings.ToLower(phrase)) {
		if unicode.Is(unicode.Mn, r) {
			continue
		}
		plain := string(r)
		if f, ok := folds[r]; ok {
			plain = f
		}
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || folds[r] != "" {
			b.WriteString(plain)
			under = false
			continue
		}
		if !under && b.Len() > 0 {
			b.WriteByte('_')
			under = true
		}
	}
	id := strings.TrimRight(b.String(), "_")
	if len(id) > maxID {
		id = strings.TrimRight(id[:maxID], "_")
	}
	if id == "" {
		var h uint32 = 2166136261 // FNV-1a
		for _, c := range []byte(phrase) {
			h = (h ^ uint32(c)) * 16777619
		}
		id = fmt.Sprintf("own_%08x", h)
	}
	return id
}

// folds are the Latin letters that do not decompose into a plain one and accents, as they are usually
// written in plain letters.
var folds = map[rune]string{
	'ß': "ss", 'æ': "ae", 'œ': "oe", 'ø': "o", 'đ': "d", 'ð': "d", 'þ': "th", 'ł': "l", 'ı': "i",
}

// AddOwn keeps a model sent from the page and its manifest, and reports the model as the device now
// has it. The same phrase sent again replaces the one sent before, unless a slot is listening for it
// (RemoveOwn says how that is known): the engine has the old one loaded and would go on running it. A
// phrase whose id is already taken by a model the page did not send is refused, so the image's own
// models and Home Assistant's are never overwritten from here.
func (l *Library) AddOwn(manifestJSON, model []byte, listening func(id string) bool) (Model, error) {
	m, err := CheckOwn(manifestJSON, model)
	if err != nil {
		return Model{}, err
	}
	frames, _ := microInput(model) // checked above
	if !safename.OK(m.ID) {
		return Model{}, fmt.Errorf("%q is not a name a model can be kept under", m.ID) // cannot happen: OwnID makes plain names
	}

	l.muOwn.Lock()
	defer l.muOwn.Unlock()

	var own int
	var replacing bool
	for _, have := range l.Ours() {
		if have.ID == m.ID && !have.Uploaded {
			return Model{}, fmt.Errorf("the device already has a wake word called %s that did not come from this page; change the phrase in the .json", m.ID)
		}
		if have.ID == m.ID {
			replacing = true
		}
		if have.Uploaded && have.ID != m.ID {
			own++
		}
	}
	if replacing && l.inUse(m.ID, listening) {
		return Model{}, errors.New("the device is listening for the wake word this replaces; choose another first, then send it again")
	}
	if own >= MaxOwnModels {
		return Model{}, fmt.Errorf("the device already keeps %d wake words sent from this page; remove one first", own)
	}

	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return Model{}, err
	}

	// The model is written under a name the directory listing does not take for a model, and tried in
	// the engine there, before anything is put in place.
	tmp, err := writeTemp(l.dir, model)
	if err != nil {
		return Model{}, err
	}
	defer os.Remove(tmp) // gone already once it has been moved into place
	if err := runs(tmp, m.Config, frames); err != nil {
		return Model{}, err
	}

	man := manifest{
		Type:             "micro",
		WakeWord:         m.Phrase,
		Model:            m.ID + ".tflite",
		TrainedLanguages: m.Languages,
		Uploaded:         true,
	}
	man.Micro.SlidingWindowSize = m.Config.SlidingWindowSize
	man.Micro.FeatureStepSize = m.Config.FeaturesStepMs
	config, err := json.MarshalIndent(man, "", "  ")
	if err != nil {
		return Model{}, err
	}

	ctmp, err := writeTemp(l.dir, config)
	if err != nil {
		return Model{}, err
	}
	defer os.Remove(ctmp)

	// The model goes first and the manifest after it, the reverse of a removal (remove): a manifest is
	// what says the page sent a model, and one put in place for a model that then failed to follow would
	// say it of whatever is under that id. The model being replaced is kept until the manifest is in, and
	// put back if it does not go in, so a failure leaves the one sent before as it was.
	path := modelPath(l.dir, m.ID)
	var old []byte
	if replacing {
		if old, err = os.ReadFile(path); err != nil {
			return Model{}, err
		}
	}
	if err := rename(tmp, path); err != nil {
		return Model{}, err
	}
	if err := rename(ctmp, filepath.Join(l.dir, m.ID+".json")); err != nil {
		if restore := putBack(l.dir, path, old); restore != nil {
			slog.Error("putting back the wake word a failed upload replaced failed", "id", m.ID, "err", restore)
		}
		return Model{}, err
	}

	l.Reload()
	slog.Info("wake word sent from the setup page kept", "id", m.ID, "phrase", m.Phrase, "bytes", len(model))
	got, _ := Find(l.Ours(), m.ID)
	return got, nil
}

// ErrListening is what changing a model a slot is listening for fails with.
var ErrListening = errors.New("the device is listening for that wake word; choose another first")

// rename is os.Rename; a test has one that fails.
var rename = os.Rename

// putBack restores the model at path to what it was, old, or takes it away if there was none.
func putBack(dir, path string, old []byte) error {
	if old == nil {
		return os.Remove(path)
	}
	tmp, err := writeTemp(dir, old)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	return os.Rename(tmp, path)
}

// RemoveOwn deletes a model the page sent. Anything else is refused: the image's own come back at the
// next start anyway, and Home Assistant's are cache that Purge already looks after. So is one a slot is
// listening for, by listening (the saved selection) or by a selection under way (Ensure): the engine
// has it loaded, and the device would go on listening for a word it no longer lists. The check is made
// under muOwn, which a selection takes too, so one cannot slip in between the check and the delete.
func (l *Library) RemoveOwn(id string, listening func(id string) bool) error {
	if !safename.OK(id) {
		return errors.New("that is not one of the wake words sent from this page")
	}
	l.muOwn.Lock()
	defer l.muOwn.Unlock()

	m, ok := Find(l.Ours(), id)
	if !ok || !m.Uploaded {
		return errors.New("that is not one of the wake words sent from this page")
	}
	if l.inUse(id, listening) {
		return ErrListening
	}
	if err := remove(l.dir, m); err != nil {
		return err
	}
	l.Reload()
	slog.Info("wake word sent from the setup page removed", "id", m.ID, "phrase", m.Phrase)
	return nil
}

// inUse is whether a slot is listening for the model, or a selection under way asks for it. Held with
// muOwn.
func (l *Library) inUse(id string, listening func(id string) bool) bool {
	return slices.Contains(l.selecting, id) || listening != nil && listening(id)
}

// uploadPrefix starts the name of a model or manifest being written (writeTemp).
const uploadPrefix = ".upload-"

// sweepUploads deletes what uploads left in dir under their temporary names: a power cut in the middle
// of one leaves up to a model's worth behind, and nothing else would ever take it away. Called as the
// library is made, before anything can be sending one.
func sweepUploads(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), uploadPrefix) {
			if err := os.Remove(filepath.Join(dir, e.Name())); err == nil {
				slog.Info("an unfinished wake word upload was deleted", "file", e.Name())
			}
		}
	}
}

// writeTemp writes b to a new file in dir under a name that does not end in .tflite or .json, so
// nothing lists it while it is written, and hands back its path.
func writeTemp(dir string, b []byte) (string, error) {
	f, err := os.CreateTemp(dir, uploadPrefix+"*")
	if err != nil {
		return "", err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", err
	}
	if err := f.Close(); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	if err := os.Chmod(f.Name(), 0o644); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}
