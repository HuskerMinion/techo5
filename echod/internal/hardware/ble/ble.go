// Package ble scans for Bluetooth Low Energy advertisements over the controller's HCI node.
package ble

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"syscall"
)

// Node is the controller's HCI character device.
const Node = "/dev/stpbt"

const (
	h4Command = 0x01

	cmdReset               = 0x0C03
	cmdLEAdvertisingParams = 0x2006
	cmdLEAdvertisingData   = 0x2008
	cmdLEScanResponseData  = 0x2009
	cmdLEAdvertisingEnable = 0x200A
	cmdLEScanParams        = 0x200B
	cmdLEScanEnable        = 0x200C
)

// How much of the time the radio listens, in units of 0.625 ms: 18.75 ms out of every 200 ms. Wifi
// and Bluetooth share one antenna here, so a window equal to the interval never gives it back.
const (
	scanInterval        = 320
	scanWindow          = 30
	advertisingInterval = 1600

	// offerInterval is Offer's: 100 ms, so a phone scanning for a few seconds finds it at once.
	offerInterval = 160
)

// Advertisement is one LE advertising report.
type Advertisement struct {
	Address     [6]byte
	AddressType uint8
	RSSI        int8
	Data        []byte
}

// Addr is the address as the integer Home Assistant takes, first byte of the printed address most
// significant (12:34:… is 0x1234…). Address holds it the other way round, least significant byte
// first, as the controller reports it; read in that order, every device would reach Home Assistant
// under its address reversed, as a device nothing else has ever seen.
func (a Advertisement) Addr() uint64 {
	var v uint64
	for i := len(a.Address) - 1; i >= 0; i-- {
		v = v<<8 | uint64(a.Address[i])
	}
	return v
}

// Radio is the controller. One node, one owner.
type Radio struct {
	// life is held for the whole of a Start, an Offer or a Stop. Stop lets go of mu while it waits for
	// the reader, and Running is already false then; without life, a Start or Offer in that second
	// opened a new fd that the Stop finishing afterwards took for its own.
	life sync.Mutex

	mu       sync.Mutex
	fd       int
	kernel   bool // fd is a raw socket on hci0, not the node
	open     bool
	scanning bool
	// offering is Offer's connectable advertisement, which the controller ends by itself when a central
	// connects and the reader turns on again when it disconnects. Set before the reader starts and
	// cleared after it has finished, so the reader reads it without mu.
	offering bool
	stop     context.CancelFunc
	// finished is both the reader's completion signal and its handoff of an unfinished H4 event.
	finished chan []byte
	// held carries an unfinished H4 event between synchronous startup commands.
	held []byte

	// reports is counted on the reader's goroutine, which holds nothing, and read by diagnostics on
	// theirs, so it is atomic rather than under mu. The bluez scanner counts its own the same way.
	reports atomic.Uint64
}

var (
	once  sync.Once
	radio *Radio
)

// Get is the radio.
func Get() *Radio {
	once.Do(func() { radio = &Radio{fd: -1} })
	return radio
}

// Scanning reports whether a scan is running.
func (r *Radio) Scanning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.scanning
}

// Running reports whether the controller is open for scanning or advertising.
func (r *Radio) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.open
}

// Reports is how many LE events have arrived.
func (r *Radio) Reports() uint64 { return r.reports.Load() }

// Start opens the controller until Stop. Reports arrive on the reader's goroutine when scan is true.
// Active asks for scan responses, which transmits rather than only listening. Advertisement is raw
// advertising data; nil disables advertising.
func (r *Radio) Start(scan, active bool, advertisement []byte, found func(Advertisement)) error {
	r.life.Lock()
	defer r.life.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.open {
		return nil
	}

	fd, err := r.openController()
	if err != nil {
		return err
	}
	r.fd = fd

	if err := r.begin(scan, active, advertisement); err != nil {
		_ = syscall.Close(fd)
		r.fd = -1
		r.held = nil
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.stop, r.finished, r.open = cancel, make(chan []byte, 1), true
	r.scanning = scan
	held := r.held
	r.held = nil

	go r.read(ctx, found, held, r.finished)
	return nil
}

// Offer advertises a GATT service that bluetoothd serves, for something nearby to connect to:
// connectable advertising (ADV_IND) with advertisement as its data and scanResponse as the scan
// response, both raw AD structures of at most 31 bytes. It is for a bluetoothd whose kernel is too old
// to advertise for it (no Add Advertising, before 4.1), so only through hci0, where the kernel's stack
// owns the controller and takes the connection to bluetoothd. A central that disconnects finds it
// advertising again. Stop ends it.
func (r *Radio) Offer(advertisement, scanResponse []byte) error {
	if len(advertisement) > 31 || len(scanResponse) > 31 {
		return errors.New("ble: advertising data exceeds 31 bytes")
	}
	r.life.Lock()
	defer r.life.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.open {
		return errors.New("ble: the radio is in use")
	}
	if !viaKernel() {
		return errors.New("ble: offering a service needs the kernel's Bluetooth stack on hci0")
	}
	fd, err := r.openController()
	if err != nil {
		return err
	}
	r.fd = fd
	if err := r.offer(advertisement, scanResponse); err != nil {
		_ = r.send("advertising disable", cmdLEAdvertisingEnable, []byte{0x00})
		_ = syscall.Close(fd)
		r.fd = -1
		r.held = nil
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	r.stop, r.finished, r.open, r.offering = cancel, make(chan []byte, 1), true, true
	held := r.held
	r.held = nil
	// Reports still arrive while bluetoothd scans for its own reasons; nobody here wants them.
	go r.read(ctx, func(Advertisement) {}, held, r.finished)
	return nil
}

// offer sets up Offer's advertising. Held with mu.
func (r *Radio) offer(advertisement, scanResponse []byte) error {
	// A daemon that restarted without its Stop may have left advertising on, and new parameters are
	// refused while it is.
	_ = r.send("advertising disable", cmdLEAdvertisingEnable, []byte{0x00})

	params := make([]byte, 15)
	binary.LittleEndian.PutUint16(params[0:], offerInterval)
	binary.LittleEndian.PutUint16(params[2:], offerInterval)
	params[4] = 0x00 // ADV_IND: connectable and scannable
	params[13] = 0x07
	if err := r.send("advertising parameters", cmdLEAdvertisingParams, params); err != nil {
		return err
	}
	if err := r.send("advertising data", cmdLEAdvertisingData, adData(advertisement)); err != nil {
		return err
	}
	if err := r.send("scan response data", cmdLEScanResponseData, adData(scanResponse)); err != nil {
		return err
	}
	return r.send("advertising enable", cmdLEAdvertisingEnable, []byte{0x01})
}

// adData is the parameter LE Set Advertising Data and LE Set Scan Response Data take: a length, then
// the data padded to 31 bytes.
func adData(b []byte) []byte {
	data := make([]byte, 32)
	data[0] = byte(len(b))
	copy(data[1:], b)
	return data
}

// Stop ends scanning and advertising and closes the node.
func (r *Radio) Stop() {
	r.life.Lock()
	defer r.life.Unlock()
	r.mu.Lock()
	if !r.open {
		r.mu.Unlock()
		return
	}
	stop, done, fd := r.stop, r.finished, r.fd
	r.open = false
	r.scanning = false
	r.mu.Unlock()

	stop()
	held := <-done
	r.offering = false

	held, _ = command(fd, held, cmdLEScanEnable, []byte{0x00, 0x00})
	_, _ = command(fd, held, cmdLEAdvertisingEnable, []byte{0x00})
	_ = syscall.Close(fd)

	r.mu.Lock()
	r.fd = -1
	r.mu.Unlock()
	slog.Info("ble stopped")
}

// openController opens the node, or hci0 when the kernel's stack owns the controller (kernel.go).
// Held with mu.
func (r *Radio) openController() (int, error) {
	if viaKernel() {
		fd, err := openKernel()
		r.kernel = err == nil
		return fd, err
	}
	r.kernel = false
	// A blocking open never returns: the driver powers the chip on inside it.
	fd, err := syscall.Open(Node, syscall.O_RDWR|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return -1, fmt.Errorf("ble: opening %s: %w", Node, err)
	}
	if err := clearNonBlock(fd); err != nil {
		_ = syscall.Close(fd)
		return -1, err
	}
	return fd, nil
}

// begin resets and configures the controller. Held with mu.
func (r *Radio) begin(scanning, active bool, advertisement []byte) error {
	if r.kernel {
		// bluetoothd has set the controller up and may be scanning for its own reasons: no reset under
		// it, and a scan of its own is stopped first, since new parameters are refused while one runs.
		// Advertising the same: a daemon that restarted without its Stop left it on, and the controller
		// then answers new advertising parameters with Command Disallowed for as long as it stays on.
		_ = r.send("scan disable", cmdLEScanEnable, []byte{0x00, 0x00})
		_ = r.send("advertising disable", cmdLEAdvertisingEnable, []byte{0x00})
	} else if err := r.send("reset", cmdReset, nil); err != nil {
		return err
	}
	// This MTK controller can scan and advertise together, but only when scanning is enabled first.
	// Enabling advertising first leaves the subsequent scan command waiting indefinitely.
	if scanning {
		scan := make([]byte, 7)
		if active {
			scan[0] = 0x01
		}
		binary.LittleEndian.PutUint16(scan[1:], scanInterval)
		binary.LittleEndian.PutUint16(scan[3:], scanWindow)

		if err := r.send("scan parameters", cmdLEScanParams, scan); err != nil {
			return err
		}
		if err := r.send("scan enable", cmdLEScanEnable, []byte{0x01, 0x00}); err != nil {
			return err
		}
	}
	if len(advertisement) != 0 {
		return r.advertise(advertisement)
	}
	return nil
}

func (r *Radio) advertise(advertisement []byte) error {
	if len(advertisement) > 31 {
		return errors.New("ble: advertising data exceeds 31 bytes")
	}
	params := make([]byte, 15)
	binary.LittleEndian.PutUint16(params[0:], advertisingInterval)
	binary.LittleEndian.PutUint16(params[2:], advertisingInterval)
	params[4] = 0x03 // ADV_NONCONN_IND
	params[13] = 0x07

	if err := r.send("advertising parameters", cmdLEAdvertisingParams, params); err != nil {
		return err
	}
	if err := r.send("advertising data", cmdLEAdvertisingData, adData(advertisement)); err != nil {
		return err
	}
	return r.send("advertising enable", cmdLEAdvertisingEnable, []byte{0x01})
}

func (r *Radio) send(name string, opcode uint16, params []byte) error {
	var err error
	r.held, err = command(r.fd, r.held, opcode, params)
	if err != nil {
		return fmt.Errorf("ble: %s: %w", name, err)
	}
	return nil
}

func (r *Radio) read(ctx context.Context, found func(Advertisement), held []byte, finished chan<- []byte) {
	defer func() { finished <- held }()

	// The driver refuses a read larger than its own buffer, and an HCI event is at most 258 bytes.
	buf := make([]byte, 512)

	for ctx.Err() == nil {
		n, err := syscall.Read(r.fd, buf)
		if err != nil {
			if errors.Is(err, syscall.EINTR) || errors.Is(err, syscall.EAGAIN) {
				continue
			}
			if ctx.Err() == nil {
				slog.Error("ble read failed", "err", err)
			}
			return
		}
		if n == 0 {
			continue
		}

		held, err = r.parse(append(held, buf[:n]...), found)
		if err != nil {
			slog.Error("ble event framing failed", "err", err)
			held = nil
			continue
		}
	}
}

// parse takes whole packets off the front and returns the remainder. A read carries as many as the
// controller had ready, and the last can be cut short.
func (r *Radio) parse(b []byte, found func(Advertisement)) ([]byte, error) {
	for {
		event, remainder, ok, err := nextEvent(b)
		if err != nil {
			return nil, err
		}
		if !ok {
			return append([]byte(nil), remainder...), nil
		}

		switch {
		case event[1] == evtLEMeta:
			r.reports.Add(1)
			reports(event[3:], found)
		case event[1] == evtDisconnectionComplete && r.offering:
			// The controller stopped advertising when the central connected. Its Command Complete
			// comes back to this reader, which has no use for it; a refusal (another link still up)
			// is left until the next disconnect.
			if err := write(r.fd, cmdLEAdvertisingEnable, []byte{0x01}); err != nil {
				slog.Warn("ble: advertising again after a disconnect", "err", err)
			}
		}
		b = remainder
	}
}

// reports walks an LE Meta event: event type, address type, address, data length, data, RSSI.
func reports(p []byte, found func(Advertisement)) {
	if len(p) < 2 || p[0] != leAdvertisingReport {
		return
	}

	at := 2
	for range int(p[1]) {
		if at+9 > len(p) {
			return
		}
		length := int(p[at+8])
		end := at + 9 + length
		if end >= len(p) {
			return
		}

		a := Advertisement{AddressType: p[at+1], RSSI: int8(p[end])}
		copy(a.Address[:], p[at+2:at+8])
		a.Data = append([]byte(nil), p[at+9:end]...)
		found(a)

		at = end + 1
	}
}

// write sends one HCI command without waiting for its answer.
func write(fd int, opcode uint16, params []byte) error {
	pkt := make([]byte, 4, 4+len(params))
	pkt[0] = h4Command
	binary.LittleEndian.PutUint16(pkt[1:], opcode)
	pkt[3] = byte(len(params))
	pkt = append(pkt, params...)
	_, err := syscall.Write(fd, pkt)
	return err
}

// command writes one HCI command and waits for its Command Complete.
func command(fd int, held []byte, opcode uint16, params []byte) ([]byte, error) {
	if err := write(fd, opcode, params); err != nil {
		return held, err
	}

	buf := make([]byte, 512)
	quiet := 0
	for {
		n, err := syscall.Read(fd, buf)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			// The hci0 socket's reads time out each second; five of them is a controller not answering.
			if errors.Is(err, syscall.EAGAIN) && quiet < 5 {
				quiet++
				continue
			}
			return held, err
		}
		if n == 0 {
			continue
		}

		held = append(held, buf[:n]...)
		var complete bool
		held, complete, err = commandResult(held, opcode)
		if err != nil {
			return held, err
		}
		if complete {
			return held, nil
		}
	}
}

func clearNonBlock(fd int) error {
	flags, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_GETFL, 0)
	if errno != 0 {
		return fmt.Errorf("ble: reading descriptor flags: %w", errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), syscall.F_SETFL, flags&^syscall.O_NONBLOCK); errno != 0 {
		return fmt.Errorf("ble: clearing O_NONBLOCK: %w", errno)
	}
	return nil
}
