package timer

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/led"
)

// parseColor reads the color a silent timer is drawn in, as an automation would write it: "#ff8c00",
// "ff8c00", "#f80", or "255, 140, 0" (what Home Assistant's color selector gives). Empty is the zero
// color, which the ring draws as countdownColor.
func parseColor(s string) (led.Color, error) {
	in := s
	s = strings.TrimSpace(s)
	if s == "" {
		return led.Color{}, nil
	}
	if parts := strings.Split(strings.Trim(s, "[]()"), ","); len(parts) == 3 {
		var rgb [3]byte
		for i, p := range parts {
			v, err := strconv.ParseUint(strings.TrimSpace(p), 10, 8)
			if err != nil {
				return led.Color{}, fmt.Errorf("timers: %q is not a color", in)
			}
			rgb[i] = byte(v)
		}
		return led.Color{R: rgb[0], G: rgb[1], B: rgb[2]}, nil
	}
	h := strings.TrimPrefix(s, "#")
	if len(h) == 3 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if len(h) != 6 || err != nil {
		return led.Color{}, fmt.Errorf("timers: %q is not a color; write it as #ff8c00 or 255, 140, 0", in)
	}
	return led.Color{R: byte(v >> 16), G: byte(v >> 8), B: byte(v)}, nil
}
