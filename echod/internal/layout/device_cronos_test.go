//go:build !dot && !spot

package layout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The command lines are the bootloaders' own, cut down to what matters, from a 2nd gen Show 5
// running TECHO5, a 1st gen running LineageOS, and a Show 8 running LineageOS (read 2026-09-22).
func TestShowBoard(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "amazon-gating")
	here := t.TempDir()
	for _, c := range []struct {
		name, cmdline, gating, want string
	}{
		{"2nd gen", "console=tty0 androidboot.hardware=mt8163 lcm=1-st7701s_wsvga_dsi_vdo_cronos_st_truly fps=5893", gone, boardCronos},
		{"1st gen", "console=tty0 androidboot.product=checkers lcm=1-st7701s_wsvga_dsi_vdo_checkers_st_kd_hsd fps=5893", here, boardCheckers},
		{"Show 8", "console=tty0 androidboot.hardware=mt8163 lcm=1-jd936x_wxga_dsi_vdo_crown_st_kd_hsd", here, boardCrown},
		{"the panel decides over the driver", "lcm=1-st7701s_wsvga_dsi_vdo_cronos_st_truly", here, boardCronos},
		{"a Show 8 is not read as a 1st gen on the driver alone", "lcm=1-jd936x_wxga_dsi_vdo_crown_st_kd_hsd", here, boardCrown},
		{"no panel, the mute driver decides", "console=tty0", here, boardCheckers},
		{"nothing to go on", "", gone, boardCronos},
	} {
		if got := showBoard(c.cmdline, c.gating); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	if _, err := os.Stat(gone); err == nil {
		t.Fatal("test setup: the missing driver exists")
	}
}

// crown and cronos begin with the same three letters, and the panel match is a substring: neither
// name may be found inside the other, or a Show 8 reads as a Show 5 and loses half its microphones.
func TestTheShowNamesDoNotContainEachOther(t *testing.T) {
	for _, a := range []string{boardCronos, boardCheckers, boardCrown} {
		for _, b := range []string{boardCronos, boardCheckers, boardCrown} {
			if a != b && strings.Contains(a, b) {
				t.Errorf("%q contains %q", a, b)
			}
		}
	}
}
