//go:build !dot

package video

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Only a loopback address is the device's own controller: not its Wi-Fi address, a private one, a
// name, a zoned link-local, an address with its port still on, or nothing at all.
func TestOnlyLoopbackIsTheDevice(t *testing.T) {
	for _, from := range []string{"127.0.0.1", "127.8.9.10", "::1", "::ffff:127.0.0.1"} {
		if !FromDevice(from) {
			t.Errorf("%q is not taken for the device", from)
		}
	}
	for _, from := range []string{"", "192.168.1.30", "10.0.0.1", "0.0.0.0", "::", "fe80::1%lo", "localhost",
		"127.0.0.1.example.com", "127.0.0.1:8181", "[::1]"} {
		if FromDevice(from) {
			t.Errorf("%q is taken for the device", from)
		}
	}
}

// A DLNA controller on the device itself (an add-on on localhost) plays without asking and is not added
// to the allowed addresses; somebody on the network still asks, and the device's own video does not
// wait behind somebody else's question.
func TestADLNAVideoFromTheDeviceDoesNotAsk(t *testing.T) {
	f := fake(t, 100000, true)
	if err := config.Set().Video().DLNA(true); err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{"127.0.0.1", "::1"} {
		if _, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4", Origin: FromDLNA, From: from}); err != nil {
			t.Fatalf("%s: %v", from, err)
		}
		if st := f.State(); st.Phase == Asking || st.AskID != 0 {
			t.Errorf("%s was asked about: %+v", from, st)
		}
		until(t, f, Playing)
		f.Stop()
	}
	if allowed := config.Get().Video.Allowed; len(allowed) != 0 {
		t.Errorf("the device's own address was remembered: %+v", allowed)
	}

	if _, err := f.Play(Request{URL: "http://192.168.1.20/a.mp4", Origin: FromDLNA, From: "192.168.1.30"}); err != nil {
		t.Fatal(err)
	}
	if f.State().Phase != Asking {
		t.Fatalf("the network was not asked about: %s", f.State().Phase)
	}
	if _, err := f.Play(Request{URL: "http://192.168.1.20/b.mp4", Origin: FromDLNA, From: "127.0.0.1"}); err != nil {
		t.Fatalf("behind a question: %v", err)
	}
	until(t, f, Playing)
	if _, _, _, asking := f.Asking(); asking {
		t.Error("the question stayed up over the device's own video")
	}
}
