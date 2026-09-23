//go:build linux

package main

// A Broadcom controller on a UART (the Echo Spot's BCM43569A2 on /dev/ttyMT1) comes up at 115200
// baud running its ROM, without the RAM patch and with no address. Before the kernel stack gets it,
// btbridge does what Android's libbt-vendor (Broadcom) does at power-on:
//
//  1. power the radio through its rfkill switch
//  2. HCI_Reset at 115200
//  3. Download_Minidriver (0xFC2E), then every record of the .hcd patch file (each one a whole HCI
//     command, Write_RAM 0xFC4C, the last Launch_RAM 0xFC4E), a Command Complete for each
//  4. the controller restarts into the patched firmware, back at 115200: HCI_Reset again
//  5. Update_UART_Baud_Rate (0xFC18) and the tty to the same speed
//  6. Write_BD_ADDR (0xFC01) with the factory address
//
// After that the tty is an H4 stream like any other and the bridge copies it to /dev/vhci.

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	opReset       = 0x0c03
	opMinidriver  = 0xfc2e
	opLaunchRAM   = 0xfc4e
	opUpdateBaud  = 0xfc18
	opWriteBdaddr = 0xfc01
)

// openUART opens a tty raw, 8N1 with RTS/CTS, at baud.
func openUART(path string, baud int) (int, error) {
	fd, err := syscall.Open(path, syscall.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		return -1, err
	}
	if err := setBaud(fd, baud); err != nil {
		syscall.Close(fd)
		return -1, err
	}
	return fd, nil
}

var bauds = map[int]uint32{
	115200:  syscall.B115200,
	230400:  syscall.B230400,
	460800:  syscall.B460800,
	921600:  syscall.B921600,
	1000000: syscall.B1000000,
	1500000: syscall.B1500000,
	2000000: syscall.B2000000,
	3000000: syscall.B3000000,
	4000000: syscall.B4000000,
}

// setBaud puts the tty in raw mode at baud, flushing what is queued.
func setBaud(fd, baud int) error {
	b, ok := bauds[baud]
	if !ok {
		return fmt.Errorf("unsupported baud rate %d", baud)
	}
	var t syscall.Termios
	if err := ioctl(fd, syscall.TCGETS, unsafe.Pointer(&t)); err != nil {
		return fmt.Errorf("TCGETS: %w", err)
	}
	t.Iflag = 0
	t.Oflag = 0
	t.Lflag = 0
	t.Cflag = b | syscall.CS8 | syscall.CLOCAL | syscall.CREAD | 0x80000000 // CRTSCTS
	t.Ispeed, t.Ospeed = b, b
	t.Cc[syscall.VMIN] = 1
	t.Cc[syscall.VTIME] = 0
	if err := ioctl(fd, syscall.TCSETS, unsafe.Pointer(&t)); err != nil {
		return fmt.Errorf("TCSETS: %w", err)
	}
	// TCFLSH (0x540B) with TCIOFLUSH (2): drop what either direction has queued.
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), 0x540b, 2); e != 0 {
		return e
	}
	return nil
}

func ioctl(fd int, req uintptr, arg unsafe.Pointer) error {
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, uintptr(fd), req, uintptr(arg)); e != 0 {
		return e
	}
	return nil
}

// rfkillPower switches every rfkill device of type bluetooth on (state 1), and reports whether
// there was one.
func rfkillPower() bool {
	found := false
	dirs, _ := filepath.Glob("/sys/class/rfkill/rfkill*")
	for _, d := range dirs {
		typ, _ := os.ReadFile(filepath.Join(d, "type"))
		if strings.TrimSpace(string(typ)) != "bluetooth" {
			continue
		}
		found = true
		if err := os.WriteFile(filepath.Join(d, "state"), []byte("1"), 0); err != nil {
			// Newer kernels take "soft" instead.
			_ = os.WriteFile(filepath.Join(d, "soft"), []byte("0"), 0)
		}
	}
	return found
}

// brcmInit brings a Broadcom UART controller from power-on to a patched, fast, addressed H4 device.
func brcmInit(path, hcd string, baud int, bdaddr string) (int, error) {
	if rfkillPower() {
		fmt.Fprintln(os.Stderr, "btbridge: radio powered through rfkill")
		time.Sleep(300 * time.Millisecond)
	}
	fd, err := openUART(path, 115200)
	if err != nil {
		return -1, fmt.Errorf("open %s: %w", path, err)
	}
	fail := func(err error) (int, error) {
		syscall.Close(fd)
		return -1, err
	}
	c := &hciConn{fd: fd}
	if _, err := c.command(opReset, nil, 3*time.Second); err != nil {
		return fail(fmt.Errorf("reset: %w", err))
	}
	if hcd != "" {
		patch, err := os.ReadFile(hcd)
		if err != nil {
			return fail(err)
		}
		start := time.Now()
		if err := c.patchram(patch); err != nil {
			return fail(fmt.Errorf("patch %s: %w", filepath.Base(hcd), err))
		}
		fmt.Fprintf(os.Stderr, "btbridge: firmware patch %s loaded in %v\n", filepath.Base(hcd), time.Since(start).Round(time.Millisecond))
		// The controller restarts into the patch at its default speed.
		time.Sleep(250 * time.Millisecond)
		if err := setBaud(fd, 115200); err != nil {
			return fail(err)
		}
		c.framer = h4Framer{}
		if _, err := c.command(opReset, nil, 3*time.Second); err != nil {
			return fail(fmt.Errorf("reset after the patch: %w", err))
		}
	}
	if baud != 115200 {
		p := make([]byte, 6)
		binary.LittleEndian.PutUint32(p[2:], uint32(baud))
		if _, err := c.command(opUpdateBaud, p, 2*time.Second); err != nil {
			return fail(fmt.Errorf("baud %d: %w", baud, err))
		}
		time.Sleep(100 * time.Millisecond)
		if err := setBaud(fd, baud); err != nil {
			return fail(err)
		}
		c.framer = h4Framer{}
		if _, err := c.command(opReset, nil, 3*time.Second); err != nil {
			return fail(fmt.Errorf("reset at %d baud: %w", baud, err))
		}
		fmt.Fprintf(os.Stderr, "btbridge: UART at %d baud\n", baud)
	}
	if bdaddr != "" {
		a, err := addressBytes(bdaddr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "btbridge: address %q: %v (keeping the controller's own)\n", bdaddr, err)
		} else if _, err := c.command(opWriteBdaddr, a, 2*time.Second); err != nil {
			fmt.Fprintf(os.Stderr, "btbridge: set address %s: %v (keeping the controller's own)\n", bdaddr, err)
		} else {
			fmt.Fprintf(os.Stderr, "btbridge: controller address set to %s\n", bdaddr)
		}
	}
	return fd, nil
}

// patchram sends the minidriver command and then each record of an .hcd file.
func (c *hciConn) patchram(patch []byte) error {
	if _, err := c.command(opMinidriver, nil, 3*time.Second); err != nil {
		return fmt.Errorf("download minidriver: %w", err)
	}
	time.Sleep(50 * time.Millisecond)
	n := 0
	for len(patch) > 0 {
		if len(patch) < 3 || len(patch) < 3+int(patch[2]) {
			return fmt.Errorf("record %d is cut short", n)
		}
		op := binary.LittleEndian.Uint16(patch)
		params := patch[3 : 3+int(patch[2])]
		patch = patch[3+int(patch[2]):]
		if _, err := c.command(op, params, 3*time.Second); err != nil {
			if op == opLaunchRAM {
				// Some ROMs restart into the patch without answering Launch_RAM; the reset after it
				// tells whether the controller came back.
				fmt.Fprintf(os.Stderr, "btbridge: launch: %v (going on)\n", err)
				break
			}
			return fmt.Errorf("record %d (%#04x): %w", n, op, err)
		}
		n++
		if op == opLaunchRAM {
			break
		}
	}
	return nil
}

// addressBytes turns 12 hex digits (most significant first) into the little-endian six bytes HCI
// wants.
func addressBytes(hexaddr string) ([]byte, error) {
	hexaddr = strings.ReplaceAll(hexaddr, ":", "")
	if len(hexaddr) != 12 {
		return nil, fmt.Errorf("want 12 hex digits")
	}
	out := make([]byte, 6)
	for i := 0; i < 6; i++ {
		var v byte
		if _, err := fmt.Sscanf(hexaddr[2*i:2*i+2], "%02x", &v); err != nil {
			return nil, fmt.Errorf("bad address")
		}
		out[5-i] = v
	}
	return out, nil
}

// hciConn is a raw H4 stream used for commands before the bridge starts.
type hciConn struct {
	fd     int
	framer h4Framer
}

// command sends one HCI command and waits for its Command Complete (or a failing Command Status),
// returning the reply's parameters after the status byte.
func (c *hciConn) command(op uint16, params []byte, timeout time.Duration) ([]byte, error) {
	pkt := []byte{0x01, byte(op), byte(op >> 8), byte(len(params))}
	pkt = append(pkt, params...)
	if err := writeAll(c.fd, pkt); err != nil {
		return nil, err
	}
	buf := make([]byte, 1024)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !readable(c.fd, time.Until(deadline)) {
			break
		}
		n, err := syscall.Read(c.fd, buf)
		if err == syscall.EINTR || err == syscall.EAGAIN {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, p := range c.framer.feed(buf[:n]) {
			if len(p) < 3 || p[0] != h4Event {
				continue
			}
			switch {
			case p[1] == 0x0e && len(p) >= 7 && binary.LittleEndian.Uint16(p[4:]) == op:
				if p[6] != 0 {
					return nil, fmt.Errorf("status %#02x", p[6])
				}
				return append([]byte(nil), p[7:]...), nil
			case p[1] == 0x0f && len(p) >= 7 && binary.LittleEndian.Uint16(p[5:]) == op && p[3] != 0:
				return nil, fmt.Errorf("status %#02x", p[3])
			}
		}
	}
	return nil, fmt.Errorf("no reply to %#04x", op)
}

// readable waits up to d for fd to have something to read.
func readable(fd int, d time.Duration) bool {
	if d <= 0 {
		return false
	}
	return selectRead(fd, d)
}
