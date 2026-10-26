//go:build linux

package gpu

// SetAfterLiftCall runs f in SetLift between the helper's reply and the gain being kept, where a
// Release from the screen's goroutine can come in; nil takes it off.
func SetAfterLiftCall(f func()) { afterLiftCall = f }
