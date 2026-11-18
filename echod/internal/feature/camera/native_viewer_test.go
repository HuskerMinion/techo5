package camera

import "testing"

// The last full-size stream to close lets go of the cached frame and its JPEG.
func TestNativeViewersLetGoOfTheFrame(t *testing.T) {
	a, b := nativeViewer(), nativeViewer()
	nativeEncoded.mu.Lock()
	nativeEncoded.jpg = []byte{0xff, 0xd8}
	nativeEncoded.mu.Unlock()
	a()
	nativeEncoded.mu.Lock()
	kept := nativeEncoded.jpg != nil
	nativeEncoded.mu.Unlock()
	if !kept {
		t.Error("let go while a stream is still open")
	}
	b()
	nativeEncoded.mu.Lock()
	defer nativeEncoded.mu.Unlock()
	if nativeEncoded.jpg != nil || nativeEncoded.frame != nil || nativeEncoded.viewers != 0 {
		t.Errorf("kept after the last stream closed: %d viewers", nativeEncoded.viewers)
	}
}
