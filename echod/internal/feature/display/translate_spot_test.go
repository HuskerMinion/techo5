//go:build spot

package display

import (
	"path/filepath"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestSpotSilentRingDoesNotDependOnEnglishLabel(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	t.Cleanup(func() { _ = config.Set().Screen().Language("") })
	if err := config.Set().Screen().Language("ja"); err != nil {
		t.Fatal(err)
	}
	for _, volume := range []int{0, 5} {
		if err := config.Set().Alarms().RingVolume(volume); err != nil {
			t.Fatal(err)
		}
		rows := adaptRows([]settingRow{ringVolumeRow()}, sheetView{})
		want := "音楽の音量とは別"
		if volume == 0 {
			want = "音を鳴らさない"
		}
		if len(rows) != 1 || rows[0].sub != want {
			t.Fatalf("volume %d: %+v", volume, rows)
		}
	}
}
