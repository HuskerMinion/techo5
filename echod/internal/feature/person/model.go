//go:build !dot

package person

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

// The model is not in the daemon: it is downloaded the first time person detection is turned on, and
// kept. Not in layout.ModelDir, where every .tflite is offered as a wake word.
var (
	modelPath = filepath.Join(layout.StateDir, "person", "person_detect.tflite")
	// download is update.Download. A variable, for tests.
	download = update.Download
)

// errDamaged is a model file that is not the one pinned: cut short, or changed.
var errDamaged = errors.New("not the size or SHA-256 pinned")

// readModel is the model as it is kept, if it is whole: the size and SHA-256 pinned. One that is not
// is removed, so the next try fetches it again.
func readModel() ([]byte, error) {
	b, err := os.ReadFile(modelPath)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if int64(len(b)) != modelFile.Size || hex.EncodeToString(sum[:]) != modelFile.SHA256 {
		os.Remove(modelPath)
		return nil, errDamaged
	}
	return b, nil
}

// modelBytes is the model, downloaded first if it is not here yet. It is nil with no error when the
// download was let go because person detection is not wanted any more (or the daemon is stopping);
// an error is a download that failed, for Run to hand to the supervisor, which tries again later.
func (f *Feature) modelBytes(ctx context.Context, stillWanted func() bool) ([]byte, error) {
	b, err := readModel()
	if err == nil {
		return b, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		slog.Warn("person detection: the model on file is damaged; downloading it again", "err", err)
	}
	say := !f.fetchSaid
	f.fetchSaid = true
	if err := fetchModel(ctx, stillWanted, say); err != nil {
		if ctx.Err() != nil || !stillWanted() {
			return nil, nil
		}
		return nil, err
	}
	b, err = readModel()
	if err != nil {
		return nil, fmt.Errorf("person detection: the model downloaded: %w", err)
	}
	slog.Info("person detection: the model is ready")
	return b, nil
}

// fetchModel downloads the model to modelPath, checked as it arrives, and lets it go if person detection
// is turned off meanwhile. A download let go keeps what it got, so the next carries on from there. say
// is whether to log that it is downloading: once, not on every try of a device that is offline.
func fetchModel(ctx context.Context, stillWanted func() bool, say bool) error {
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
		return fmt.Errorf("person detection: %w", err)
	}
	if say {
		slog.Info("person detection: downloading the model", "bytes", modelFile.Size)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		tick := time.NewTicker(5 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if !stillWanted() {
					cancel()
					return
				}
			}
		}
	}()
	if err := download(ctx, modelFile, modelPath, nil); err != nil {
		return fmt.Errorf("person detection: downloading the model: %w", err)
	}
	return nil
}
