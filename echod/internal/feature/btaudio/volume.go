//go:build linux

package btaudio

import (
	"log/slog"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluealsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Bluetooth sets the volume (#245): on by default, a phone or a computer playing to the device has one
// volume, the device's (oneVolume). Some sources do not send their slider that way: a Linux computer
// (PipeWire, PulseAudio) often reports full level once and then scales its audio itself, which took
// the device to 100% and left it there, for Music Assistant after it too. Off, the device keeps its
// own volume: bluez-alsa scales the source's audio by the source's own level (SoftVolume), a change
// of that level never sets the device's volume (phoneSlider), and the device's volume is never handed
// to the source (tellPhone). Turned either way while a source plays, the stream playing here is
// switched over at once (applyVolumeMode), and any look after that would put it right as well, as it
// does a stream bluez-alsa lost the mode of (a bluez-alsa restart).

// pcmSetter is the part of bluez-alsa a stream's volume mode is set through; a test stands in for it.
type pcmSetter interface {
	SetSoftVolume(p bluealsa.PCM, on bool) error
	SetLevel(p bluealsa.PCM, level int) error
}

// volumeNow is the device's volume step; a test stands in for it.
var volumeNow = func() int { return media.Get().Volume() }

// bluetoothSetsVolume is whether a phone's volume is the device's now.
func bluetoothSetsVolume() bool { return !config.Get().Speaker.NoBluetoothVolume }

// sliderSets is whether a received stream's level, read now, sets the device's volume: only while
// Bluetooth sets the volume is on, and then when it is the phone's slider moved (sliderMove).
func sliderSets(shared bool, level, last int, known, ours bool) bool {
	return shared && sliderMove(level, last, known, ours)
}

// volMode is the volume mode put on a stream, and when.
type volMode struct {
	shared bool
	at     time.Time
}

// modeSettle is how long after a mode is put on a stream bluez-alsa is given to say it took it: until
// then a look may still have the stream from before.
const modeSettle = 5 * time.Second

// modeStale is whether a stream's volume mode has to be put on again at now: it never was (applied
// false), it was put on the other way (m) from what the setting says now (shared), or bluez-alsa's
// own state for it (soft, whether it scales the audio) says otherwise, once it has had time to say.
func modeStale(m volMode, applied, shared, soft bool, now time.Time) bool {
	if !applied || m.shared != shared {
		return true
	}
	return soft == shared && now.Sub(m.at) >= modeSettle
}

// putMode puts on p the volume mode the setting says as it is put on, one at a time, so a switch
// turned quickly back and forth leaves the stream as the switch ended up, not as a turn in between.
func (f *Feature) putMode(al pcmSetter, p bluealsa.PCM) {
	f.modeMu.Lock()
	defer f.modeMu.Unlock()
	f.volumeMode(al, p, bluetoothSetsVolume(), volumeNow())
}

// volumeMode sets p's volume mode. Shared, bluez-alsa leaves the audio as it comes and the phone is
// handed the device's volume step as its level; not shared, bluez-alsa scales the audio by the
// phone's own level, and nothing is handed to the phone. The level then known is the last one, so
// only a later change counts as the slider moving.
func (f *Feature) volumeMode(al pcmSetter, p bluealsa.PCM, shared bool, step int) {
	path := string(p.Path)
	// Put on again the same way (bluez-alsa did not keep it), a failure has been said once already.
	f.mu.Lock()
	m, applied := f.volModes[path]
	f.mu.Unlock()
	warn := slog.Warn
	if applied && m.shared == shared {
		warn = slog.Debug
	}
	if err := al.SetSoftVolume(p, !shared); err != nil {
		if shared {
			warn("bluetooth: leaving a phone's audio unscaled", "err", err)
		} else {
			warn("bluetooth: having a phone's level scale its own audio", "err", err)
		}
	}
	level := p.Level()
	if shared {
		level = levelFor(step)
		if err := al.SetLevel(p, level); err != nil {
			warn("bluetooth: handing a phone the volume", "err", err)
		}
	}
	f.mu.Lock()
	f.levels[path] = level
	if shared {
		f.ours[path] = time.Now().Add(echoFor)
	} else {
		delete(f.ours, path)
	}
	f.volModes[path] = volMode{shared: shared, at: time.Now()}
	f.mu.Unlock()
}

// receivingPCMs is the streams of the phone that has the speaker, playing or paused, and the bluez-alsa
// client they are set through; none when no phone has it.
func (f *Feature) receivingPCMs() (*bluealsa.Client, []bluealsa.PCM) {
	f.mu.Lock()
	al, a := f.alsa, f.adapter
	f.mu.Unlock()
	if al == nil || a == nil {
		return nil, nil
	}
	cur := media.Get().Receiving()
	if cur == "" {
		return nil, nil
	}
	names := map[string]string{}
	for _, d := range a.Devices() {
		names[string(d.Path)] = media.BluetoothPrefix + displayName(d)
	}
	var out []bluealsa.PCM
	for _, p := range al.PCMs() {
		if p.Receivable() && names[string(p.Device)] == cur {
			out = append(out, p)
		}
	}
	return al, out
}

// applyVolumeMode puts the stream playing here into the mode the setting says now, as the switch turns.
func (f *Feature) applyVolumeMode() {
	al, pcms := f.receivingPCMs()
	for _, p := range pcms {
		f.putMode(al, p)
	}
}

// newSetsVolumeSwitch is the setting's switch for Home Assistant.
func newSetsVolumeSwitch() *esphome.Switch {
	return &esphome.Switch{Base: esphome.Base{
		ObjectID: "bluetooth_sets_volume", Name: "Bluetooth sets the volume", Icon: "mdi:volume-source",
		Category: esphome.CategoryConfig,
	}}
}

// SetsVolume reports the setting: true unless turned off.
func (f *Feature) SetsVolume() bool { return bluetoothSetsVolume() }

// AudioHere reports whether this device plays what a phone sends over Bluetooth: not on Android, whose
// own stack has the radio (init).
func (f *Feature) AudioHere() bool { return !layout.OnAndroid() }

// SetSetsVolume turns Bluetooth sets the volume on or off, as Home Assistant's switch would, for the
// setup page, and switches the stream playing here over.
func (f *Feature) SetSetsVolume(on bool) error {
	if err := config.Set().Speaker().NoBluetoothVolume(!on); err != nil {
		slog.Error("saving a setting failed", "setting", f.setsVolume.ObjectID, "err", err)
		f.setsVolume.Set(f.SetsVolume())
		return err
	}
	f.setsVolume.Set(on)
	slog.Info("setting changed", "setting", f.setsVolume.ObjectID, "using", on)
	safe.Go("bluetooth volume setting", f.applyVolumeMode)
	return nil
}

// Restore shows the setting as the device was left.
func (f *Feature) Restore(c config.Config) { f.setsVolume.Set(!c.Speaker.NoBluetoothVolume) }
