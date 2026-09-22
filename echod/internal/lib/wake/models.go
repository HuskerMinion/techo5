package wake

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/zserge/microwakeword"

	"github.com/HuskerMinion/techo5/echod/internal/lib/oww"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safename"
	"github.com/HuskerMinion/techo5/echod/internal/lib/tflite"
)

// DefaultModel is what a device that has never been configured listens for, by id. Something rather
// than nothing, because a satellite that hears no phrase at all until somebody finds the select reads
// as broken; this one because it is what the reference satellite ships selected, so a person who has
// used one already knows what to say.
const DefaultModel = "okay_nabu"

// Model is an installed wake word: the file to run and what to call it in Home Assistant.
// Defaults for a model that arrives without a manifest, which most do.
const (
	WindowSize  = 5
	FeatureStep = 10
)

// Kind is which engine runs a wake word.
type Kind string

const (
	KindOpenWakeWord  Kind = "openwakeword"
	KindMicroWakeWord Kind = "microwakeword"
)

type Model struct {
	// ID is the file's base name. Home Assistant selects by it.
	ID string

	// Phrase is what the user says, shown in Home Assistant's picker.
	Phrase string

	// Languages the model was trained on, as BCP-47 tags.
	Languages []string

	// Kind is which engine runs it. Both are plain .tflite files that arrive the same way, so the
	// model itself has to say, and it does: an openWakeWord wake word classifies a window of speech
	// embeddings and takes them as its input, while a microWakeWord model is self-contained and
	// consumes audio features.
	Kind Kind

	Path   string
	Config microwakeword.Config
}

// manifest is the JSON that ships beside a model, as Home Assistant serves it from
// custom_wake_words. For a model Home Assistant offers us, the id, phrase and languages come from
// the offer itself and are written here when it is adopted; this is also what lets a model copied
// on by hand carry its own label.
type manifest struct {
	WakeWord         string   `json:"wake_word"`
	Model            string   `json:"model"`
	TrainedLanguages []string `json:"trained_languages"`

	Micro struct {
		SlidingWindowSize int `json:"sliding_window_size"`
		FeatureStepSize   int `json:"feature_step_size"`
	} `json:"micro"`
}

// Installed lists the models in dir, one per .tflite, reading a sidecar JSON of the same name when
// there is one. A model with no manifest still works: the defaults are the ones every published
// microWakeWord model uses, and the phrase comes from the file name.
// A directory that is not there yet is not an error: a device Home Assistant has never offered a wake
// word to has none, and adopting the first one creates it.
func Installed(dir string) ([]Model, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var out []Model
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".tflite" {
			continue
		}

		id := strings.TrimSuffix(e.Name(), ".tflite")
		out = append(out, load(dir, id))
	}
	return out, nil
}

// kindOf asks the model which engine runs it. An openWakeWord classifier takes a window of
// embeddings, [1, frames, 96]; anything else is treated as microWakeWord, which is the safe default
// because that engine reports a load failure while a wrong guess the other way would score noise.
func kindOf(path string) Kind {
	raw, err := os.ReadFile(path)
	if err != nil {
		return KindMicroWakeWord
	}
	m, err := tflite.Parse(raw)
	if err != nil {
		return KindMicroWakeWord
	}

	// Parse proves the file can be read, not that it says anything sensible: a model with no subgraph,
	// or one whose input names a tensor that is not in its table, reads cleanly and then indexes out of
	// range here. A file that cannot answer the question is the same as one that answers "not
	// openWakeWord" — the engine that loads it reports the failure properly a moment later.
	if len(m.Subgraphs) == 0 {
		return KindMicroWakeWord
	}
	g := m.Subgraphs[0]
	if len(g.Inputs) == 0 || g.Inputs[0] < 0 || g.Inputs[0] >= len(g.Tensors) {
		return KindMicroWakeWord
	}
	shape := g.Tensors[g.Inputs[0]].Shape
	if len(shape) == 3 && shape[2] == oww.EmbeddingDims {
		return KindOpenWakeWord
	}
	return KindMicroWakeWord
}

func load(dir, id string) Model {
	path := filepath.Join(dir, id+".tflite")
	m := Model{
		ID:        id,
		Phrase:    phrase(id),
		Languages: []string{"en"},
		Kind:      kindOf(path),
		Path:      path,
		Config: microwakeword.Config{
			SlidingWindowSize: WindowSize,
			FeaturesStepMs:    FeatureStep,
		},
	}
	m.Config.ModelPath = m.Path

	data, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return m
	}

	var man manifest
	if err := json.Unmarshal(data, &man); err != nil {
		return m
	}

	if man.WakeWord != "" {
		m.Phrase = man.WakeWord
	}
	if len(man.TrainedLanguages) > 0 {
		m.Languages = man.TrainedLanguages
	}
	// The name in the manifest is the one part of it that becomes a path. A manifest is JSON fetched
	// from Home Assistant, or dropped in the directory by hand, and this path is what Purge deletes
	// and what the engine loads — so a manifest that names something outside the model directory is
	// ignored and the model keeps the file named after its id, which is the file that is there.
	if man.Model != "" {
		named, err := safename.Join(dir, man.Model)
		if err != nil {
			slog.Error("a wake word's manifest names a file outside the model directory; ignoring it",
				"id", id, "model", man.Model)
		} else {
			m.Path = named
			m.Config.ModelPath = m.Path
		}
	}

	// Window and step describe the model. probability_cutoff is deliberately ignored: the
	// threshold is the user's, set from Home Assistant.
	if man.Micro.SlidingWindowSize > 0 {
		m.Config.SlidingWindowSize = man.Micro.SlidingWindowSize
	}
	if man.Micro.FeatureStepSize > 0 {
		m.Config.FeaturesStepMs = man.Micro.FeatureStepSize
	}
	return m
}

// phrase turns hey_jarvis into "Hey Jarvis", for a model that arrived without a manifest.
func phrase(id string) string {
	words := strings.FieldsFunc(id, func(r rune) bool { return r == '_' || r == '-' })
	for i, w := range words {
		words[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(words, " ")
}

// Pick returns the model with the given id, or the first installed one when the id is empty or no
// longer present — a stale selection should not leave the device deaf.
func Pick(models []Model, id string) Model {
	for _, m := range models {
		if m.ID == id {
			return m
		}
	}
	return models[0]
}

// Find returns the model with the given id, and whether there is one. Unlike Pick it does not
// substitute: a slot the user set to nothing has to stay at nothing.
func Find(models []Model, id string) (Model, bool) {
	for _, m := range models {
		if m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// OfKind is the models one engine runs.
func OfKind(models []Model, k Kind) []Model {
	var out []Model
	for _, m := range models {
		if m.Kind == k {
			out = append(out, m)
		}
	}
	return out
}
