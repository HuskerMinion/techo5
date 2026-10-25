package camstream

import (
	"encoding/xml"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
)

// Discovery tells anyone who asks the device's name, model and MAC address, so a device starts with it
// off, and its switch is offered with the stream's.
func TestDiscoveryIsOffUntilTurnedOn(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	if config.Get().CamStream.Discovery {
		t.Fatal("discovery is on in a fresh configuration")
	}
	f := build()
	f.available = func() bool { return true }
	var ids []string
	for _, e := range f.Entities() {
		if b, ok := e.(interface{ Object() string }); ok {
			ids = append(ids, b.Object())
		}
	}
	if !strings.Contains(strings.Join(ids, " "), "camera_discovery") {
		t.Errorf("entities %v: no discovery switch", ids)
	}
	if err := config.Set().CamStream().Discovery(true); err != nil {
		t.Fatal(err)
	}
	if !config.Get().CamStream.Discovery {
		t.Error("the switch was not kept")
	}
}

// A Probe is answered for a video transmitter, a device, or anything; not for another kind.
func TestDiscoveryAnswersCamerasOnly(t *testing.T) {
	for types, want := range map[string]bool{
		"":                           true,
		"dn:NetworkVideoTransmitter": true,
		"tds:Device":                 true,
		"NetworkVideoTransmitter":    true,
		"dp0:NetworkVideoDisplay":    false,
		"wsdp:Device dn:Unrelated":   true,
		"pub:Computer pub:Printer":   false,
	} {
		if got := wantedTypes(types); got != want {
			t.Errorf("wantedTypes(%q) = %v, want %v", types, got, want)
		}
	}
}

// A name is one word in a scope, as cameras write it: Home Assistant shows it as it is.
func TestScopeNamesAreOneWord(t *testing.T) {
	for in, want := range map[string]string{
		"Kitchen Echo":  "Kitchen_Echo",
		" Den/Office ":  "Den_Office",
		"Echo Show 5":   "Echo_Show_5",
		"a%b?c#d":       "a_b_c_d",
		"Caf\u00e9":     "Caf%C3%A9",
		"echo-show_8.1": "echo-show_8.1",
	} {
		if got := scopeWord(in); got != want {
			t.Errorf("scopeWord(%q) = %q, want %q", in, got, want)
		}
	}
}

// The answer to a Probe says the device is a camera, what it is called, and where its device service is.
func TestProbeMatchSaysWhatAndWhere(t *testing.T) {
	msg := probeMatch("1b4e28ba-2fa1-41d2-883f-0016d3cca427", "urn:uuid:probe-1", "192.0.2.10",
		scopeList("Kitchen Echo", "Echo Show 5", "00:00:5e:00:53:01"))
	var m struct {
		RelatesTo string `xml:"Header>RelatesTo"`
		Match     struct {
			Types  string `xml:"Types"`
			Scopes string `xml:"Scopes"`
			XAddrs string `xml:"XAddrs"`
		} `xml:"Body>ProbeMatches>ProbeMatch"`
	}
	if err := xml.Unmarshal([]byte(msg), &m); err != nil {
		t.Fatalf("%v: %s", err, msg)
	}
	if m.RelatesTo != "urn:uuid:probe-1" {
		t.Errorf("RelatesTo %q", m.RelatesTo)
	}
	if m.Match.Types != "dn:NetworkVideoTransmitter tds:Device" {
		t.Errorf("Types %q", m.Match.Types)
	}
	sc := strings.Fields(m.Match.Scopes)
	for _, want := range []string{"onvif://www.onvif.org/name/Kitchen_Echo", "onvif://www.onvif.org/hardware/Echo_Show_5",
		"onvif://www.onvif.org/mac/00:00:5e:00:53:01", "onvif://www.onvif.org/type/video_encoder"} {
		if !strings.Contains(" "+strings.Join(sc, " ")+" ", " "+want+" ") {
			t.Errorf("scopes %q: no %s", sc, want)
		}
	}
	if want := "http://192.0.2.10:" + strconv.Itoa(web.Port) + devicePath; m.Match.XAddrs != want {
		t.Errorf("XAddrs %q, want %q", m.Match.XAddrs, want)
	}
}

// Only the local network is answered.
func TestDiscoveryAnswersLocalSendersOnly(t *testing.T) {
	for ip, want := range map[string]bool{
		"192.168.1.20": true,
		"10.0.0.7":     true,
		"172.16.4.4":   true,
		"169.254.9.9":  true,
		"127.0.0.1":    true,
		"203.0.113.5":  false,
		"8.8.8.8":      false,
		"224.0.0.1":    false,
	} {
		if got := answerable(net.ParseIP(ip)); got != want {
			t.Errorf("answerable(%s) = %v, want %v", ip, got, want)
		}
	}
}

// A sender, real or forged, is answered a few times a second at most, and all of them together not
// much more; the senders kept track of stay few however many there are.
func TestDiscoveryLimitsAnswers(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a, b := net.ParseIP("192.168.1.20"), net.ParseIP("192.168.1.21")

	l := newLimiter()
	n := 0
	for range 10 {
		if l.allow(a, t0) {
			n++
		}
	}
	if n != burst {
		t.Errorf("%d answers at once to one sender, want %d", n, burst)
	}
	if !l.allow(b, t0) {
		t.Error("another sender was not answered")
	}
	n = 0
	for range 10 {
		if l.allow(a, t0.Add(time.Second)) {
			n++
		}
	}
	if n != perSource {
		t.Errorf("%d answers a second later, want %d", n, perSource)
	}

	l = newLimiter()
	n = 0
	for i := range 100 {
		if l.allow(net.IPv4(10, 0, byte(i/250), byte(i%250+1)), t0) {
			n++
		}
	}
	if n != total {
		t.Errorf("%d answers at once to 100 senders, want %d", n, total)
	}

	l = newLimiter()
	for i := range 5000 {
		l.allow(net.ParseIP(fmt.Sprintf("10.%d.%d.%d", i>>16&255, i>>8&255, i&255)), t0.Add(time.Duration(i)*time.Millisecond))
		if len(l.from) > sources {
			t.Fatalf("%d senders kept, at most %d", len(l.from), sources)
		}
	}
}

// GetHostname is the name the device has on the network: one DNS label.
func TestHostnameIsALabel(t *testing.T) {
	if got := hostname("Kitchen Echo", ""); got != "kitchen-echo" {
		t.Errorf("hostname = %q", got)
	}
	if got := hostname(strings.Repeat("Room ", 30), ""); len(got) > 63 || strings.HasSuffix(got, "-") {
		t.Errorf("hostname = %q", got)
	}
}
