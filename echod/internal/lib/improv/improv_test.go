package improv

import (
	"bytes"
	"errors"
	"testing"
)

func TestParseWiFiRoundTrip(t *testing.T) {
	for _, w := range []WiFi{
		{SSID: "Heimnetz", Passphrase: "geheim123"},
		{SSID: "Café", Passphrase: ""},
		{SSID: "x", Passphrase: "12345678901234567890123456789012345678901234567890123456789012"},
	} {
		f, err := Parse(WiFiFrame(w))
		if err != nil {
			t.Fatalf("%q: parse: %v", w.SSID, err)
		}
		if f.Command != CmdWiFiSettings {
			t.Fatalf("%q: command %#x", w.SSID, f.Command)
		}
		got, err := ParseWiFi(f.Data)
		if err != nil {
			t.Fatalf("%q: wifi: %v", w.SSID, err)
		}
		if got != w {
			t.Fatalf("got %+v, want %+v", got, w)
		}
	}
}

// The frame the spec's own example builds: ssid "MyWirelessAP", passphrase "mysecurepassword".
func TestKnownFrame(t *testing.T) {
	b := WiFiFrame(WiFi{SSID: "MyWirelessAP", Passphrase: "mysecurepassword"})
	if b[0] != 0x01 || b[1] != byte(1+12+1+16) {
		t.Fatalf("header % x", b[:2])
	}
	var sum byte
	for _, c := range b[:len(b)-1] {
		sum += c
	}
	if b[len(b)-1] != sum {
		t.Fatalf("checksum %#x, want %#x", b[len(b)-1], sum)
	}
}

func TestParseRefuses(t *testing.T) {
	good := Frame{Command: CmdIdentify}.Bytes()
	bad := append([]byte(nil), good...)
	bad[len(bad)-1]++
	for name, b := range map[string][]byte{
		"empty":     nil,
		"short":     {0x02, 0x00},
		"checksum":  bad,
		"too long":  append(append([]byte(nil), good...), 0),
		"truncated": {0x01, 0x05, 0x01, 'a', 0x07},
	} {
		if _, err := Parse(b); !errors.Is(err, ErrFrame) {
			t.Errorf("%s: err %v", name, err)
		}
	}
}

func TestParseWiFiRefuses(t *testing.T) {
	for name, d := range map[string][]byte{
		"no ssid":       {0x00, 0x00},
		"ssid too long": {0x05, 'a', 'b'},
		"no passphrase": {0x01, 'a'},
		"trailing":      {0x01, 'a', 0x00, 'x'},
	} {
		if _, err := ParseWiFi(d); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestResult(t *testing.T) {
	got := Result(CmdWiFiSettings, "http://192.168.1.20:8080")
	want := []byte{0x01, 25, 24}
	want = append(want, "http://192.168.1.20:8080"...)
	var sum byte
	for _, c := range want {
		sum += c
	}
	want = append(want, sum)
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x\nwant % x", got, want)
	}

	empty := Result(CmdScan)
	if !bytes.Equal(empty, []byte{0x04, 0x00, 0x04}) {
		t.Fatalf("empty result % x", empty)
	}
}

func TestResultFits(t *testing.T) {
	long := string(bytes.Repeat([]byte("a"), 200))
	b := Result(CmdDeviceInfo, long, long)
	if b[1] != 201 {
		t.Fatalf("data length %d, want only the first string", b[1])
	}
	if len(b) != 2+201+1 {
		t.Fatalf("frame length %d", len(b))
	}
}

func TestServiceData(t *testing.T) {
	if !bytes.Equal(ServiceData(Authorized, CanIdentify|CanScan), []byte{0x02, 0x05, 0, 0, 0, 0}) {
		t.Fatal("service data")
	}
}
