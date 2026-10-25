//go:build (!dot && !spot && !panel) || donut

package mic

import (
	"log/slog"

	"github.com/HuskerMinion/techo5/echod/internal/lib/alsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/i2c"
)

// Microphone bias on athena.
//
// The TLV320ADC5140 driver in Amazon's kernel never powers the converter's own MICBIAS: its DAPM has
// only a MIC1 input, an ADC and Capture, no MICBIAS widget and no control, and adc5140_dai_prepare
// sets PWR_CFG (0x75) bits 0x60 (ADC and PLL) and leaves bit 7 (MICBIAS_PDZ) off. Fire OS has no other
// way to the chip: nothing in its HAL or vendor files talks to it over I2C. So under Fire OS, where the
// microphones work, the converter's MICBIAS is off too.
//
// What Fire OS does turn on is the PMIC's microphone path. Its PcmMerge capture provider, the one that
// opens I2S2ADC2 and DL1_AWB_Record, calls enablePmicInputDevice(true) once both have started, and that
// is AudioALSAHardwareResourceManager::startInputDevice for the built-in microphone: the mic type path
// ("Mic1TypeACCMode") and then "builtin_Mic_SingleMic" from audio_device_tas5805m.xml. On MediaTek's
// MT6358 codec driver those switches power the PMIC's ADC path and with it its MICBIAS outputs. That
// is the best reading of where athena's analog microphones get their bias: a theory until the unit
// shows a room floor with it on.
var athenaPmicMicOn = []struct{ name, item string }{
	{"Audio_MIC1_Mode_Select", "ACCMODE"},
	{"Audio_MicSource1_Setting", "ADC1"},
	{"Audio_ADC_1_Switch", "On"},
	{"Audio_ADC_2_Switch", "On"},
	{"Audio_Preamp1_Switch", "IN_ADC1"},
	{"Audio_Preamp2_Switch", "IN_ADC3"},
}

// athenaPmicMicOff is the same file's "builtin_Mic_SingleMic" turnoff, for when the capture is let go.
var athenaPmicMicOff = []struct{ name, item string }{
	{"Audio_Preamp1_Switch", "OPEN"},
	{"Audio_ADC_1_Switch", "Off"},
	{"Audio_ADC_2_Switch", "Off"},
}

// athenaPmicMic applies the PMIC microphone path on or off, each control looked up alone.
func athenaPmicMic(on bool) {
	seq := athenaPmicMicOff
	if on {
		seq = athenaPmicMicOn
	}
	m, err := alsa.OpenMixer(Card)
	if err != nil {
		slog.Error("opening the mixer failed", "err", err)
		return
	}
	defer m.Close()
	failed := 0
	for _, c := range seq {
		if !athenaWriteEnum(m, c.name, c.item) {
			failed++
		}
	}
	slog.Info("athena: PMIC microphone path", "on", on, "controls", len(seq), "failed", failed)
}

// athenaWriteEnum picks an enumerated control's item by name, looked up alone.
func athenaWriteEnum(m *alsa.Mixer, name, item string) bool {
	c, err := m.Lookup(name)
	if err != nil {
		slog.Warn("athena: mixer control not on this unit", "control", name)
		return false
	}
	for i, have := range c.Items {
		if have == item {
			if err := m.Set(c, uint32(i)); err != nil {
				slog.Error("athena: mixer write failed", "control", name, "err", err)
				return false
			}
			return true
		}
	}
	slog.Warn("athena: mixer control has no such item", "control", name, "item", item, "items", c.Items)
	return false
}

// The converter's own MICBIAS, over I2C behind the driver's back. Off: Fire OS never turns it on (see
// above), and where the MICBIAS pin goes on this board is not known. athenaADCMicBias is the switch for
// trying it once the PMIC path has been ruled out; athena-audio-check ADCBIAS=1 tries it without a
// rebuild.
const (
	athenaADCMicBias = false

	athenaADCBus  = 4
	athenaADCAddr = 0x4c
	adcPwrCfg     = 0x75
	adcMicBiasPDZ = 0x80
)

// athenaADCBiasOn sets PWR_CFG's MICBIAS bit, read-modify-write, and reads it back. It runs after the
// stream has started, since adc5140_dai_prepare rewrites the register's other bits on every start.
func athenaADCBiasOn() {
	if !athenaADCMicBias {
		return
	}
	b, err := i2c.Open(athenaADCBus, athenaADCAddr, true)
	if err != nil {
		slog.Error("athena: opening the converter on I2C failed", "err", err)
		return
	}
	defer b.Close()
	before, err := b.ReadReg(adcPwrCfg)
	if err != nil {
		slog.Error("athena: reading PWR_CFG failed", "err", err)
		return
	}
	if err := b.WriteReg(adcPwrCfg, before|adcMicBiasPDZ); err != nil {
		slog.Error("athena: writing PWR_CFG failed", "err", err)
		return
	}
	after, err := b.ReadReg(adcPwrCfg)
	slog.Info("athena: converter MICBIAS", "before", before, "after", after, "err", err)
}
