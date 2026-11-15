//go:build !dot

package person

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// fixture is the model as testdata has it: the file the release publishes, which the daemon downloads.
func fixture(tb testing.TB) []byte {
	tb.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "person_detect.tflite"))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// The model in testdata is the one pinned, so the tests run what a device downloads and the two
// cannot drift apart.
func TestTheFixtureIsThePinnedModel(t *testing.T) {
	b := fixture(t)
	sum := sha256.Sum256(b)
	if got := hex.EncodeToString(sum[:]); got != modelFile.SHA256 || int64(len(b)) != modelFile.Size {
		t.Fatalf("testdata/person_detect.tflite is %s, %d bytes; pinned.go says %s, %d bytes",
			got, len(b), modelFile.SHA256, modelFile.Size)
	}
	if !strings.HasPrefix(modelFile.URL, "https://github.com/HuskerMinion/techo5/releases/download/") ||
		!strings.HasSuffix(modelFile.URL, "/techo5-person-detect.tflite") {
		t.Fatalf("pinned from %s", modelFile.URL)
	}
}

// useModelPath has the model kept in a folder of the test's own, and downloaded by get.
func useModelPath(t *testing.T, get func(update.Binary, string) error) *int {
	t.Helper()
	wasPath, wasDownload := modelPath, download
	t.Cleanup(func() { modelPath, download = wasPath, wasDownload })
	modelPath = filepath.Join(t.TempDir(), "person", "person_detect.tflite")
	fetched := 0
	download = func(_ context.Context, b update.Binary, to string, _ func(float32)) error {
		fetched++
		if b != modelFile {
			t.Errorf("fetched %+v", b)
		}
		if to != modelPath {
			t.Errorf("fetched to %s", to)
		}
		return get(b, to)
	}
	return &fetched
}

// The model is downloaded once, kept, and read from where it is kept after.
func TestTheModelIsDownloadedOnceAndKept(t *testing.T) {
	model := fixture(t)
	fetched := useModelPath(t, func(_ update.Binary, to string) error { return os.WriteFile(to, model, 0o644) })
	f := &Feature{}
	yes := func() bool { return true }
	for range 2 {
		b, err := f.modelBytes(context.Background(), yes)
		if err != nil || !bytes.Equal(b, model) {
			t.Fatalf("%d bytes, %v", len(b), err)
		}
	}
	if *fetched != 1 {
		t.Fatalf("downloaded %d times", *fetched)
	}
	if _, err := load(model); err != nil {
		t.Fatal(err)
	}
}

// A kept model that is not the one pinned, cut short or changed, is removed and downloaded again.
func TestADamagedModelIsDownloadedAgain(t *testing.T) {
	model := fixture(t)
	fetched := useModelPath(t, func(_ update.Binary, to string) error { return os.WriteFile(to, model, 0o644) })
	for _, bad := range [][]byte{model[:1000], append([]byte{model[0] ^ 1}, model[1:]...)} {
		os.MkdirAll(filepath.Dir(modelPath), 0o755)
		if err := os.WriteFile(modelPath, bad, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := readModel(); !errors.Is(err, errDamaged) {
			t.Fatalf("a damaged model read with %v", err)
		}
		if _, err := os.Stat(modelPath); !os.IsNotExist(err) {
			t.Fatal("a damaged model was kept")
		}
		os.WriteFile(modelPath, bad, 0o644)
		b, err := (&Feature{}).modelBytes(context.Background(), func() bool { return true })
		if err != nil || !bytes.Equal(b, model) {
			t.Fatalf("%d bytes, %v", len(b), err)
		}
	}
	if *fetched != 2 {
		t.Fatalf("downloaded %d times", *fetched)
	}
}

// A download that fails is an error, for the supervisor to try again later, unless person detection
// was turned off meanwhile: then it is let go without one.
func TestADownloadThatFails(t *testing.T) {
	useModelPath(t, func(update.Binary, string) error { return errors.New("offline") })
	if b, err := (&Feature{}).modelBytes(context.Background(), func() bool { return true }); err == nil || b != nil {
		t.Fatalf("%d bytes, %v", len(b), err)
	}
	if b, err := (&Feature{}).modelBytes(context.Background(), func() bool { return false }); err != nil || b != nil {
		t.Fatalf("turned off: %d bytes, %v", len(b), err)
	}
}

// Nothing is downloaded while person detection is off.
func TestNothingIsDownloadedWhileOff(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	fetched := useModelPath(t, func(update.Binary, string) error { return errors.New("not wanted") })
	if wanted() {
		t.Fatal("wanted with person detection off")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	f := &Feature{wake: make(chan struct{}, 1)}
	if err := f.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if *fetched != 0 {
		t.Fatalf("downloaded %d times", *fetched)
	}
}

func TestModelLoadsAndScores(t *testing.T) {
	d, err := load(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	gray := make([]uint8, side*side)
	for i := range gray {
		gray[i] = uint8(i % 251)
	}
	p, err := d.score(gray)
	if err != nil {
		t.Fatal(err)
	}
	if p < 0 || p > 1 {
		t.Fatalf("a score of %v", p)
	}
	// The same picture, the same score: nothing is carried from one look to the next.
	q, _ := d.score(gray)
	if p != q {
		t.Fatalf("%v then %v", p, q)
	}
	if _, err := d.score(gray[:10]); err == nil {
		t.Fatal("a picture of the wrong size was scored")
	}
}

// TestModelTellsAPersonFromNone runs the model on TensorFlow Lite Micro's own two pictures (testdata), a
// person and no person (a giraffe among trees), 96 by 96 shades of gray as picture makes them.
func TestModelTellsAPersonFromNone(t *testing.T) {
	d, err := load(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		file   string
		person bool
	}{{"person.gray", true}, {"no_person.gray", false}} {
		gray, err := os.ReadFile(filepath.Join("testdata", c.file))
		if err != nil {
			t.Fatal(err)
		}
		p, err := d.score(gray)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %.3f", c.file, p)
		// threshold is 0.6: the person well over it, the giraffe well under.
		if c.person && p < 0.8 || !c.person && p > 0.4 {
			t.Errorf("%s scored %.3f", c.file, p)
		}
	}
}

func TestPictureIsTheMiddleSquareBrightened(t *testing.T) {
	w := side * 4 / 3
	g := make([]uint8, w*side)
	for y := range side {
		for x := range w {
			g[y*w+x] = uint8(x)
		}
	}
	out := square(g, w)
	if len(out) != side*side {
		t.Fatalf("%d pixels", len(out))
	}
	// The middle columns, not the whole width squeezed: the first is column (w-side)/2.
	if out[0] != gamma[(w-side)/2] || out[side-1] != gamma[(w-side)/2+side-1] {
		t.Fatalf("columns %d to %d, want %d to %d", out[0], out[side-1], gamma[(w-side)/2], gamma[(w-side)/2+side-1])
	}
	// Black and white stay, and the middle comes up: 18% gray is about half way in a photograph.
	if gamma[0] != 0 || gamma[255] != 255 || gamma[46] < 112 || gamma[46] > 124 {
		t.Fatalf("gamma 0, 46, 255: %d, %d, %d", gamma[0], gamma[46], gamma[255])
	}
	for i := 1; i < 256; i++ {
		if gamma[i] < gamma[i-1] {
			t.Fatalf("gamma falls at %d", i)
		}
	}
}

func TestConfidenceUnknownWhenNotLooking(t *testing.T) {
	f := Get()
	f.confidence.Set(42)
	f.forget()
	if !math.IsNaN(float64(f.confidence.Get())) {
		t.Fatalf("confidence %v, want none", f.confidence.Get())
	}
}

func TestJudge(t *testing.T) {
	var j judge
	at := time.Unix(1000, 0)
	j.add(0.9, at)
	j.add(0.9, at)
	if j.seen(at) {
		t.Fatal("seen before enough looks")
	}
	j.add(0.9, at)
	if !j.seen(at) || !j.seen(at.Add(hold-time.Second)) || j.seen(at.Add(hold)) {
		t.Fatal("a sighting not held for hold")
	}

	// One high score among low ones is not a person.
	var k judge
	for _, p := range []float32{0.2, 0.95, 0.3} {
		k.add(p, at)
	}
	if k.seen(at) {
		t.Fatal("one look in three made a person")
	}
	if m := k.mean(); math.Abs(float64(m)-(0.2+0.95+0.3)/3) > 1e-6 {
		t.Fatalf("mean %v", m)
	}
}

func BenchmarkScore(b *testing.B) {
	d, err := load(fixture(b))
	if err != nil {
		b.Fatal(err)
	}
	gray := make([]uint8, side*side)
	for b.Loop() {
		if _, err := d.score(gray); err != nil {
			b.Fatal(err)
		}
	}
}
