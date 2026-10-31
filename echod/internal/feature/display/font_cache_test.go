//go:build !dot

package display

import "testing"

// Faces made with the owner's own fonts as they were before are let go once fonts of a newer generation
// are in use: a font of one's own removed or replaced is not kept open by the settings screen's faces.
func TestFacesOfAnOlderGenerationAreLetGo(t *testing.T) {
	sheetFaceCache.mu.Lock()
	sheetFaceCache.faces = nil
	sheetFaceCache.mu.Unlock()
	name := screenFont()
	facesFor(fontKey{name, 1001, 100})
	facesFor(fontKey{name, 1002, 100})
	sheetFaceCache.mu.Lock()
	defer sheetFaceCache.mu.Unlock()
	if _, kept := sheetFaceCache.faces[fontKey{name, 1001, 100}]; kept || len(sheetFaceCache.faces) != 1 {
		t.Errorf("the cache holds %d fonts' faces, the older generation's among them: %v", len(sheetFaceCache.faces), kept)
	}
}

// Within one generation, several fonts' faces are kept (a few at most), and the cap still holds.
func TestRoomForKeepsOneGenerationsFonts(t *testing.T) {
	m := map[fontKey]int{{"a", 7, 100}: 1, {"b", 7, 100}: 2}
	m = roomFor(m, fontKey{"c", 7, 100})
	if len(m) != 2 {
		t.Errorf("faces of the same generation were dropped: %v", m)
	}
	m[fontKey{"c", 7, 100}], m[fontKey{"d", 7, 100}] = 3, 4
	if m = roomFor(m, fontKey{"e", 7, 100}); len(m) != 0 {
		t.Errorf("the cap of %d fonts did not start the cache again: %v", keptFonts, m)
	}
}
