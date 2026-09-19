package mic

import "testing"

// Every ADC is pointed at its differential input and unmuted. The 1st gen Echo Show 5 comes up with
// the converter's channel mutes on, which silences the microphones however the rest is routed.
func TestInputControls(t *testing.T) {
	for _, adc := range adcs {
		c := inputControls(adc)
		for name, want := range map[string]uint32{
			"ADC_" + adc + " Left Mute":                                     0,
			"ADC_" + adc + " Right Mute":                                    0,
			"ADC_" + adc + " Left Ip Select ADC_" + adc + " DIF1_L switch":  1,
			"ADC_" + adc + " Right Ip Select ADC_" + adc + " DIF1_R switch": 1,
		} {
			got, ok := c[name]
			if !ok {
				t.Errorf("%s is not set", name)
				continue
			}
			if got != want {
				t.Errorf("%s = %d, want %d", name, got, want)
			}
		}
	}
}
