//go:build linux

package asp

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"testing"
	"time"
)

// The Go test executable doubles as a real, independently launched child.
// It exercises production FD3 exec, READY, pipes, cancellation, deadlines and
// ownership without a C compiler or prebuilt helper. The C wire integration
// suite is separate; this is not a substitute for C conformance.
func TestMain(m *testing.M) {
	if os.Getenv("ASP_TEST_CHILD") == "1" {
		os.Exit(runIPCChild())
	}
	os.Exit(m.Run())
}

func selfExecutable(t *testing.T) string {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func runIPCChild() int {
	mode := os.Getenv("ASP_TEST_BEHAVIOR")
	if mode == "start-wait" {
		time.Sleep(30 * time.Second)
		return 0
	}
	var header [HeaderSize]byte
	if _, err := io.ReadFull(os.Stdin, header[:]); err != nil {
		return 2
	}
	size := binary.LittleEndian.Uint32(header[8:12])
	if string(header[:4]) != Magic || size < HeaderSize || size > MaxDescriptorSize {
		return 3
	}
	// Read the exact physical geometry descriptor, rather than assuming a frame size.
	wire := make([]byte, int(size)-HeaderSize)
	if _, err := io.ReadFull(os.Stdin, wire); err != nil {
		return 4
	}
	if mode == "bad-ready" {
		_, _ = os.Stdout.Write([]byte{0x11})
		return 2
	}
	if _, err := os.Stdout.Write([]byte{ReadyByte}); err != nil {
		return 5
	}
	if mode == "exit" {
		return 2
	}
	if mode == "short" {
		_, _ = os.Stdout.Write(make([]byte, 10))
		return 2
	}
	if mode == "sleep" {
		time.Sleep(30 * time.Second)
		return 0
	}
	payload := make([]byte, int(binary.LittleEndian.Uint32(header[12:16])))
	if mode == "stagger" {
		if _, err := io.ReadFull(os.Stdin, payload); err != nil {
			return 6
		}
		time.Sleep(3 * time.Second)
		_, _ = os.Stdout.Write(make([]byte, 320))
		time.Sleep(3 * time.Second)
		_, _ = os.Stdout.Write(make([]byte, 320))
		return 0
	}
	for n := 0; ; n++ {
		if _, err := io.ReadFull(os.Stdin, payload); err != nil {
			return 0
		}
		frame := bytes.Repeat([]byte{byte(n + 1)}, OutputBytes)
		if n == 0 {
			clear(frame[:128])
		}
		if _, err := os.Stdout.Write(frame); err != nil {
			return 0
		}
	}
}
