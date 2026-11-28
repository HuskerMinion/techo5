//go:build linux

package ble

import (
	"bytes"
	"syscall"
	"testing"
	"time"
)

// pausedRadio is a running, scanning radio whose fd is one end of a socket pair, and the other end,
// where what it writes to the controller arrives.
func pausedRadio(t *testing.T, scanning bool) (*Radio, int) {
	t.Helper()
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_SEQPACKET, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Close(fds[0]); _ = syscall.Close(fds[1]) })
	if err := syscall.SetNonblock(fds[1], true); err != nil {
		t.Fatal(err)
	}
	return &Radio{fd: fds[0], open: true, scanning: scanning, active: true}, fds[1]
}

// written is every command the radio wrote, in order, as H4 packets.
func written(fd int) [][]byte {
	var out [][]byte
	buf := make([]byte, 512)
	for {
		n, err := syscall.Read(fd, buf)
		if err != nil || n <= 0 {
			return out
		}
		out = append(out, append([]byte(nil), buf[:n]...))
	}
}

func packet(opcode uint16, params []byte) []byte {
	return append([]byte{h4Command, byte(opcode), byte(opcode >> 8), byte(len(params))}, params...)
}

// A connection bluetoothd makes needs the controller free of this socket's scan, and gets the scan
// back, with its own parameters, once it is made. Nested pauses resume only at the last.
func TestPauseStopsTheScanUntilTheLastResume(t *testing.T) {
	r, peer := pausedRadio(t, true)

	outer := r.Pause()
	inner := r.Pause()
	got := written(peer)
	if len(got) != 1 || !bytes.Equal(got[0], packet(cmdLEScanEnable, []byte{0x00, 0x00})) {
		t.Fatalf("pausing wrote %x, want one scan disable", got)
	}

	inner()
	if got := written(peer); len(got) != 0 {
		t.Fatalf("an inner resume wrote %x", got)
	}
	outer()
	outer() // a resume called twice counts once
	want := [][]byte{
		packet(cmdLEScanEnable, []byte{0x00, 0x00}),
		packet(cmdLEScanParams, scanParams(true)),
		packet(cmdLEScanEnable, []byte{0x01, 0x00}),
	}
	got = written(peer)
	if len(got) != len(want) {
		t.Fatalf("resuming wrote %x, want %x", got, want)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("resume command %d = %x, want %x", i, got[i], want[i])
		}
	}
	if r.paused != 0 {
		t.Fatalf("paused = %d after every resume", r.paused)
	}
}

// A radio that is not scanning (stopped, or only sending the iBeacon) has no scan to stop or start.
func TestPauseLeavesARadioThatIsNotScanning(t *testing.T) {
	r, peer := pausedRadio(t, false)
	r.Pause()()
	if got := written(peer); len(got) != 0 {
		t.Fatalf("a radio not scanning wrote %x", got)
	}
}

// The kernel turning the scan off (to connect a device) is seen on the socket, and the scan is taken
// back once the connection is made, while no Pause is out.
func TestScanTakenBackFromTheKernel(t *testing.T) {
	was := retakeAfter
	retakeAfter = 20 * time.Millisecond
	t.Cleanup(func() { retakeAfter = was })
	r, peer := pausedRadio(t, true)

	// LE Create Connection, then scan off: nothing is written while the connection is being made.
	if _, err := r.parse(append(packet(cmdLECreateConnection, make([]byte, 25)), packet(cmdLEScanEnable, []byte{0x00, 0x00})...), func(Advertisement) {}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if got := written(peer); len(got) != 0 {
		t.Fatalf("wrote %x while the kernel was connecting", got)
	}
	// LE Connection Complete: the scan comes back with the radio's own parameters.
	complete := append([]byte{h4Event, evtLEMeta, 19, leConnectionComplete}, make([]byte, 18)...)
	if _, err := r.parse(complete, func(Advertisement) {}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	got := written(peer)
	if len(got) != 3 || !bytes.Equal(got[1], packet(cmdLEScanParams, scanParams(true))) || !bytes.Equal(got[2], packet(cmdLEScanEnable, []byte{0x01, 0x00})) {
		t.Fatalf("taking the scan back wrote %x", got)
	}

	// Under a Pause it is left to the resume; a scan the kernel turns on is no reason to act.
	resume := r.Pause()
	written(peer)
	if _, err := r.parse(append(packet(cmdLEScanEnable, []byte{0x01, 0x00}), packet(cmdLEScanEnable, []byte{0x00, 0x00})...), func(Advertisement) {}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if got := written(peer); len(got) != 0 {
		t.Fatalf("wrote %x under a Pause", got)
	}
	resume()
	if got := written(peer); len(got) != 3 {
		t.Fatalf("resume wrote %x", got)
	}
}

// Commands from other sockets are passed over where only events are wanted (startup commands).
func TestNextEventPassesOverCommands(t *testing.T) {
	report := []byte{h4Event, evtLEMeta, 2, leAdvertisingReport, 0}
	b := append(packet(cmdLEScanEnable, []byte{0x00, 0x00}), report...)
	event, rest, ok, err := nextEvent(b)
	if err != nil || !ok || !bytes.Equal(event, report) || len(rest) != 0 {
		t.Fatalf("event %x rest %x ok %v err %v", event, rest, ok, err)
	}
	_, rest, ok, err = nextEvent(packet(cmdLEScanEnable, []byte{0x00, 0x00})[:3])
	if err != nil || ok || len(rest) != 3 {
		t.Fatalf("a partial command: rest %x ok %v err %v", rest, ok, err)
	}
}
