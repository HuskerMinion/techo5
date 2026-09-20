//go:build !dot && !spot

package layout

import (
	"os"
	"path/filepath"
	"testing"
)

// The command lines are the bootloaders' own, cut down to what matters, from a 2nd gen running
// TECHO5 and a 1st gen running LineageOS.
func TestShowBoard(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "amazon-gating")
	here := t.TempDir()
	for _, c := range []struct {
		name, cmdline, gating, want string
	}{
		{"2nd gen", "console=tty0 androidboot.hardware=mt8163 lcm=1-st7701s_wsvga_dsi_vdo_cronos_st_truly fps=5893", gone, boardCronos},
		{"1st gen", "console=tty0 androidboot.product=checkers lcm=1-st7701s_wsvga_dsi_vdo_checkers_st_kd_hsd fps=5893", here, boardCheckers},
		{"the panel decides over the driver", "lcm=1-st7701s_wsvga_dsi_vdo_cronos_st_truly", here, boardCronos},
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
