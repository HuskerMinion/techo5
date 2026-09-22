package mic

import (
	"fmt"
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
)

// The array's analog gain. Turning it up buys level, not signal to noise: the floor is the room and
// the microphones, so both rise together. It costs headroom, so the default is the vendor's 20 dB.
const (
	// One control per ADC, each carrying both of its channels.
	pgaControls = "ADC_%s MICPGA Volume Ctrl"

	// Steps of half a decibel. The mixer declares a maximum of 80 and does not enforce it: the
	// register is seven bits, writes above 127 wrap, and 119 is the converter's own top of 59.5 dB.
	// Measured, the gain above 80 is real and costs no signal to noise.
	pgaStepDB = 0.5
	pgaMax    = 119
)

// inputControls points one ADC at the differential input its microphones are wired to, and takes its
// two channel mutes off. The 2nd gen comes up with those mutes already off and the 1st gen (checkers)
// comes up with them on, which silences the microphones however the rest is routed (seen on a unit
// 2026-09-19: everything else below already matched). Writing them costs nothing where they are off.
func inputControls(adc string) map[string]uint32 {
	return map[string]uint32{
		fmt.Sprintf("ADC_%s DIF1_L Input Gain", adc):                          0,
		fmt.Sprintf("ADC_%s DIF1_R Input Gain", adc):                          0,
		fmt.Sprintf("ADC_%[1]s Left Ip Select ADC_%[1]s DIF1_L switch", adc):  1,
		fmt.Sprintf("ADC_%[1]s Right Ip Select ADC_%[1]s DIF1_R switch", adc): 1,
		fmt.Sprintf("ADC_%s Left Mute", adc):                                  0,
		fmt.Sprintf("ADC_%s Right Mute", adc):                                 0,
	}
}

// Rewire gives the capture device back, which the supervised service reopens a moment later: open()
// prepares the stream again and applies the routing and the gain to a converter that is listening.
//
// The 1st gen Echo Show 5 takes the microphone chip down with the mute latch, so it comes back in its
// reset state when the button releases the latch: muted, and with the stream it was handing over
// dead. Rewriting its controls was not enough on a unit (2026-09-20) — the stream has to be opened
// again — and on the 2nd gen, where nothing was lost, this costs one reopen.
func Rewire() {
	if !resetsOnMute() {
		return
	}
	// Say so before the close, so the read waiting in the kernel knows why it is about to fail and
	// asks the supervisor for a restart instead of reporting a crash.
	src := Get()
	src.rewiring.Store(true)
	if err := src.Close(); err != nil {
		src.rewiring.Store(false)
		slog.Warn("handing the capture device back failed", "err", err)
		return
	}
	slog.Info("capture: reopening after the mute came off")
}

// routeInputs applies inputControls to every ADC. Each control is written twice, the other way first:
// the driver keeps what it last wrote and skips a write that changes nothing, so after the chip has
// been through its reset — which the 1st gen Echo Show 5's mute latch does — asking for the value the
// driver already believes in would leave the hardware where the reset put it, muted (a unit stayed
// deaf after the button released the latch until this was written the long way, 2026-09-20).
func routeInputs() {
	m, err := alsa.OpenMixer(Card)
	if err != nil {
		slog.Error("opening the mixer failed", "err", err)
		return
	}
	defer m.Close()

	for _, adc := range adcs {
		for name, v := range inputControls(adc) {
			// Every one of these is a switch or a one-bit gain, so the other way is 1 - v.
			_ = m.SetInt(name, 1-v)
			if err := m.SetInt(name, v); err != nil {
				slog.Error("routing the microphone input failed", "control", name, "err", err)
			}
		}
	}
}

// applyGain sets the analog gain on every ADC. A microphone that cannot be turned up is worth a log
// and nothing more: the array still works, quietly.
func applyGain(db int) {
	m, err := alsa.OpenMixer(Card)
	if err != nil {
		slog.Error("opening the mixer failed", "err", err)
		return
	}
	defer m.Close()

	steps := min(max(float64(db)/pgaStepDB, 0), pgaMax)
	for _, adc := range adcs {
		name := fmt.Sprintf(pgaControls, adc)
		// A step away first, for the same reason routeInputs writes the other way: a converter that
		// has been through its reset is not where the driver last left it.
		_ = m.SetInt(name, uint32(max(steps-1, 0)))
		if err := m.SetInt(name, uint32(steps)); err != nil {
			slog.Error("setting the microphone gain failed", "control", name, "err", err)
			return
		}
	}
	slog.Info("microphone gain", "db", db)
}

// SetGain changes the analog gain and remembers it.
func SetGain(db int) error {
	if err := config.Set().Microphone().Gain(db); err != nil {
		return err
	}
	applyGain(db)

	// The converter's own noise moves with this, and that is what the room is measured against.
	Get().leveler.atGain(db)
	return nil
}
