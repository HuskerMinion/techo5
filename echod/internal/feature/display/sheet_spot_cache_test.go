//go:build spot

package display

import "testing"

// The round settings screen's faces let go of an older generation's too.
func TestSpotFacesOfAnOlderGenerationAreLetGo(t *testing.T) {
	spotFaceCache.mu.Lock()
	spotFaceCache.faces = nil
	spotFaceCache.mu.Unlock()
	name := screenFont()
	spotFaces(fontKey{name, 2001, 100})
	spotFaces(fontKey{name, 2002, 100})
	spotFaceCache.mu.Lock()
	defer spotFaceCache.mu.Unlock()
	if _, kept := spotFaceCache.faces[fontKey{name, 2001, 100}]; kept || len(spotFaceCache.faces) != 1 {
		t.Errorf("the cache holds %d fonts' faces, the older generation's among them: %v", len(spotFaceCache.faces), kept)
	}
}
