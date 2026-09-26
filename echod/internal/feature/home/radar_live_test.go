package home

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Builds the rain map from the real services and writes its frames, to look at:
//
//	RADAR_LIVE=<dir> RADAR_AT=lat,lon[,nws|rainviewer] go test -run TestRadarLive ./internal/feature/home/
//
// Skipped unless RADAR_LIVE is set, so the ordinary tests never touch the network.
func TestRadarLive(t *testing.T) {
	dir := os.Getenv("RADAR_LIVE")
	if dir == "" {
		t.Skip("RADAR_LIVE not set")
	}
	at := strings.Split(os.Getenv("RADAR_AT"), ",")
	if len(at) < 2 {
		t.Fatal("RADAR_AT=lat,lon[,source]")
	}
	lat, _ := strconv.ParseFloat(at[0], 64)
	lon, _ := strconv.ParseFloat(at[1], 64)
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if len(at) > 2 {
		_ = config.Set().Home().RadarSource(at[2])
	}
	defer func(d string) { mapDir = d }(mapDir)
	mapDir = t.TempDir()
	f := &Feature{}
	if err := f.buildRadarAt(lat, lon); err != nil {
		t.Fatal(err)
	}
	v := f.radar.view
	t.Logf("%d frames, %d towns, credit %q", len(v.Frames), len(v.Places), v.Credit)
	if b, err := json.Marshal(v.Places); err == nil {
		_ = os.WriteFile(filepath.Join(dir, "places.json"), b, 0o644)
	}
	for i, fr := range v.Frames {
		out, err := os.Create(filepath.Join(dir, fmt.Sprintf("frame-%d.png", i)))
		if err != nil {
			t.Fatal(err)
		}
		_ = png.Encode(out, fr.Image)
		out.Close()
	}
}
