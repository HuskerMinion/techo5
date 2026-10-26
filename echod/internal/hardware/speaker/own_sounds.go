package speaker

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Recordings of the owner's own, as the setup page keeps them: the same files, in the same folder, as
// the ones copied on over SSH (clips.go), so the two ways never disagree about what plays.

// ownable are the clips a recording of the owner's own can stand in for, in the order the setup page
// lists them.
var ownable = []*Clip{ClipWake, ClipFailure, ClipCanceled, ClipTimer, ClipAlarm, ClipMuteOn, ClipMuteOff}

// MaxOwnUpload is the most a recording sent from the setup page may be: ten seconds of 16-bit stereo
// at Rate, and room for the header and the extra chunks a converter writes. Anything longer cannot be
// a sound the device would play, so it is turned away before it has all arrived.
const MaxOwnUpload = longestOwn*2*2 + 64<<10

// previewLevel is how loud the setup page tries a sound: a quarter of where the feedback sounds play,
// on top of the device's volume. Somebody choosing a sound at a phone wants to hear it, not to be
// startled by it.
const previewLevel = toneLevel / 4

// OwnSounds are the names of the sounds a recording can replace, as their files are named without
// the .wav.
func OwnSounds() []string {
	out := make([]string, len(ownable))
	for i, c := range ownable {
		out[i] = c.file
	}
	return out
}

// ownClip is the clip of that name, or nil. Only these names ever become a path: the name a browser
// sends is looked up here, never joined to the folder as it came.
func ownClip(name string) *Clip {
	for _, c := range ownable {
		if c.file == name {
			return c
		}
	}
	return nil
}

// OwnState is whether there is a recording of the owner's own for the sound, and if one is there but
// does not play, why not.
func OwnState(name string) (has bool, problem string) {
	c := ownClip(name)
	if c == nil {
		return false, ""
	}
	from := c.ownRecording()
	if from == (recording{}) {
		return false, ""
	}
	if c.Own() {
		return true, ""
	}
	if _, err := readOwn(from.path); err != nil {
		return true, err.Error()
	}
	return true, ""
}

// SaveOwn keeps r as the recording for the sound, once it has been checked the way the player checks
// it. It is written beside the old one and then moved over it, so the sound never plays half a file
// and a refused file leaves the one before it in place.
func SaveOwn(name string, r io.Reader) error {
	c := ownClip(name)
	if c == nil {
		return fmt.Errorf("%q is not one of the sounds", name)
	}
	b, err := io.ReadAll(io.LimitReader(r, MaxOwnUpload+1))
	if err != nil {
		return errors.New("the sound did not arrive whole")
	}
	if len(b) > MaxOwnUpload {
		return fmt.Errorf("that file is bigger than ten seconds of sound; the most is %d KB", MaxOwnUpload>>10)
	}
	if _, err := checkOwn(b); err != nil {
		return fmt.Errorf("the device cannot play that file: %v. It needs a WAVE file, 16-bit, 48 kHz, mono or stereo, at most 10 seconds", err)
	}
	if err := os.MkdirAll(SoundsDir, 0o755); err != nil {
		return err
	}
	// The temporary name does not end in .wav, so nothing takes it for a sound while it is written.
	tmp, err := os.CreateTemp(SoundsDir, uploadPrefix+"*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // gone already once it has been moved into place
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), c.ownPath()); err != nil {
		return err
	}
	slog.Info("a sound of one's own was kept", "sound", name, "bytes", len(b))
	return nil
}

// uploadPrefix starts the name of a recording being written (SaveOwn).
const uploadPrefix = ".upload-"

// SweepUploads deletes what uploads left in SoundsDir under their temporary names: a power cut in the
// middle of one leaves up to a recording's worth behind, and nothing else would ever take it away.
// Called once as the daemon starts, before the setup page can be sending one.
func SweepUploads() {
	entries, _ := os.ReadDir(SoundsDir)
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), uploadPrefix) {
			if err := os.Remove(filepath.Join(SoundsDir, e.Name())); err == nil {
				slog.Info("an unfinished sound upload was deleted", "file", e.Name())
			}
		}
	}
}

// RemoveOwn takes the owner's recording away, so the stock sound plays again. One that is not there
// is already removed.
func RemoveOwn(name string) error {
	c := ownClip(name)
	if c == nil {
		return fmt.Errorf("%q is not one of the sounds", name)
	}
	if err := os.Remove(c.ownPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	slog.Info("a sound of one's own was removed", "sound", name)
	return nil
}

// PreviewOwn plays the sound as it would play now, the owner's recording or the stock one, quietly,
// alongside whatever is playing.
func PreviewOwn(name string) error {
	c := ownClip(name)
	if c == nil {
		return fmt.Errorf("%q is not one of the sounds", name)
	}
	notes := []Note{c.Note()}
	switch c {
	case ClipFailure:
		notes = FailureSound()
	case ClipCanceled:
		notes = CancelSound()
	case ClipAlarm:
		notes = AlarmSound(OwnAlarmSound)
	}
	Sound().Interject(func(p *Player) { p.Chime(previewLevel, notes...) })
	return nil
}
