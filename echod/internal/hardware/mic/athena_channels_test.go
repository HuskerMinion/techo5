//go:build !dot && !spot && !panel

package mic

import "testing"

// The converter's channel enables when the capture opens: the software mute switches them off in any
// process, only the daemon switches them on, and a process that is not the daemon leaves them be (a
// CLI tool would otherwise switch them on behind a software mute the daemon holds).
func TestAthenaChannelValue(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cut, here bool
		v         uint32
		write     bool
	}{
		{"daemon, live: on", false, true, 1, true},
		{"daemon, software mute: off", true, true, 0, true},
		{"another process, its copy says muted: off", true, false, 0, true},
		{"another process, its copy says live: left alone", false, false, 0, false},
	} {
		v, write := athenaChannelValue(1, tc.cut, tc.here)
		if v != tc.v || write != tc.write {
			t.Errorf("%s: got %d write=%v, want %d write=%v", tc.name, v, write, tc.v, tc.write)
		}
	}
}
