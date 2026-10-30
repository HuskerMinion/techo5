// Package improv is the Improv Wi-Fi protocol over Bluetooth LE, without the radio: the frames a phone
// writes to the device and the ones the device answers with, and what each byte means.
//
// Improv (improv-wifi.com) is how a device with no screen and no network is told which network to
// join: a phone or a browser finds it over Bluetooth, sends a name and a passphrase, and is told the
// address to open once it is on. Home Assistant finds a device offering it on its own.
//
// Every frame is [command][length][data...][checksum], the checksum the low byte of the sum of the
// bytes before it. Data in a result is a run of strings, each a length byte and that many bytes.
package improv

import (
	"errors"
	"fmt"
)

// The service and its characteristics. All of them are cleartext: Improv has no pairing of its own,
// which is why the device offers it only for a few minutes, and only when asked or offline.
const (
	ServiceUUID      = "00467768-6228-2272-4663-277478268000"
	StateUUID        = "00467768-6228-2272-4663-277478268001" // read, notify: one State byte
	ErrorUUID        = "00467768-6228-2272-4663-277478268002" // read, notify: one Error byte
	RPCCommandUUID   = "00467768-6228-2272-4663-277478268003" // write: a command frame
	RPCResultUUID    = "00467768-6228-2272-4663-277478268004" // read, notify: a result frame
	CapabilitiesUUID = "00467768-6228-2272-4663-277478268005" // read: one Capabilities byte

	// ServiceDataUUID is the 16-bit UUID 0x4677 the advertisement's service data goes under.
	ServiceDataUUID = "00004677-0000-1000-8000-00805f9b34fb"
)

// State is where provisioning is.
type State byte

const (
	AuthorizationRequired State = 0x01 // waiting for someone at the device to allow it
	Authorized            State = 0x02 // ready for a network
	Provisioning          State = 0x03 // joining the network it was sent
	Provisioned           State = 0x04 // on it
)

func (s State) String() string {
	switch s {
	case AuthorizationRequired:
		return "authorization required"
	case Authorized:
		return "authorized"
	case Provisioning:
		return "provisioning"
	case Provisioned:
		return "provisioned"
	}
	return fmt.Sprintf("state 0x%02x", byte(s))
}

// Error is what went wrong with the last command, ErrNone once a new one arrives.
type Error byte

const (
	ErrNone            Error = 0x00
	ErrInvalidRPC      Error = 0x01 // a frame that does not parse, or whose checksum is wrong
	ErrUnknownCommand  Error = 0x02
	ErrUnableToConnect Error = 0x03 // the network was not joined
	ErrNotAuthorized   Error = 0x04 // a network sent before the device allowed it
	ErrUnknown         Error = 0xFF
)

// Command is what a frame asks for.
type Command byte

const (
	CmdWiFiSettings Command = 0x01 // [ssid len][ssid][passphrase len][passphrase]
	CmdIdentify     Command = 0x02 // no data: make the device noticeable
	CmdDeviceInfo   Command = 0x03 // no data: firmware, version, hardware, name
	CmdScan         Command = 0x04 // no data: one result per network heard, then an empty one
)

// Capabilities is the capabilities characteristic, a bit per optional command.
type Capabilities byte

const (
	CanIdentify   Capabilities = 0x01
	CanDeviceInfo Capabilities = 0x02
	CanScan       Capabilities = 0x04
)

// ServiceData is the six bytes the advertisement carries under ServiceDataUUID, so a phone knows the
// state before it connects.
func ServiceData(s State, c Capabilities) []byte {
	return []byte{byte(s), byte(c), 0, 0, 0, 0}
}

// Frame is one command.
type Frame struct {
	Command Command
	Data    []byte
}

// ErrFrame is a frame that cannot be read: too short, a length that does not fit, or a bad checksum.
var ErrFrame = errors.New("improv: malformed frame")

// Parse reads one command frame. Trailing bytes after the checksum are refused rather than ignored: a
// write is one frame, and anything else is a client this does not understand.
func Parse(b []byte) (Frame, error) {
	if len(b) < 3 {
		return Frame{}, ErrFrame
	}
	n := int(b[1])
	if len(b) != 2+n+1 {
		return Frame{}, ErrFrame
	}
	if checksum(b[:2+n]) != b[2+n] {
		return Frame{}, ErrFrame
	}
	return Frame{Command: Command(b[0]), Data: append([]byte(nil), b[2:2+n]...)}, nil
}

// WiFi is a network sent to be joined.
type WiFi struct {
	SSID       string
	Passphrase string // empty for an open network
}

// ParseWiFi reads the data of a CmdWiFiSettings frame.
func ParseWiFi(data []byte) (WiFi, error) {
	ssid, rest, ok := str(data)
	if !ok || ssid == "" {
		return WiFi{}, ErrFrame
	}
	pass, rest, ok := str(rest)
	if !ok || len(rest) != 0 {
		return WiFi{}, ErrFrame
	}
	return WiFi{SSID: ssid, Passphrase: pass}, nil
}

// Result is the frame answering cmd with strings. A result frame has room for 255 bytes of data, so
// strings that do not fit are cut at the last one that does.
func Result(cmd Command, strs ...string) []byte {
	data := make([]byte, 0, 64)
	for _, s := range strs {
		if len(s) > 255 {
			s = s[:255]
		}
		if len(data)+1+len(s) > 255 {
			break
		}
		data = append(data, byte(len(s)))
		data = append(data, s...)
	}
	out := append([]byte{byte(cmd), byte(len(data))}, data...)
	return append(out, checksum(out))
}

// Bytes is the frame on the wire, for tests and clients.
func (f Frame) Bytes() []byte {
	out := append([]byte{byte(f.Command), byte(len(f.Data))}, f.Data...)
	return append(out, checksum(out))
}

// WiFiFrame builds the CmdWiFiSettings frame for w, for tests and clients.
func WiFiFrame(w WiFi) []byte {
	data := append([]byte{byte(len(w.SSID))}, w.SSID...)
	data = append(data, byte(len(w.Passphrase)))
	data = append(data, w.Passphrase...)
	return Frame{Command: CmdWiFiSettings, Data: data}.Bytes()
}

func str(b []byte) (s string, rest []byte, ok bool) {
	if len(b) < 1 || len(b) < 1+int(b[0]) {
		return "", nil, false
	}
	n := int(b[0])
	return string(b[1 : 1+n]), b[1+n:], true
}

func checksum(b []byte) byte {
	var sum byte
	for _, c := range b {
		sum += c
	}
	return sum
}
