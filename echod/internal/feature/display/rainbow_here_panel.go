//go:build panel && !dot

package display

// hasRainbow: not on a tablet. Its glow changes the whole edge of every frame, and the app is sent each
// changed frame whole, many megabytes a second at Rainbow's pace.
const hasRainbow = false
