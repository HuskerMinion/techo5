package dlna

import (
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const didl = `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/">` +
	`<item id="1" parentID="0" restricted="1"><dc:title>Winding Road</dc:title><upnp:artist>The Made-Up Band</upnp:artist>` +
	`<upnp:album>Roads</upnp:album><upnp:albumArtURI>http://192.0.2.10/art.jpg</upnp:albumArtURI>` +
	`<res duration="0:03:25.500" protocolInfo="http-get:*:audio/flac:*">http://192.0.2.10/song.flac</res></item></DIDL-Lite>`

func soap(service, action, args string) (string, string) {
	typ := map[string]string{"AVTransport": avtType, "RenderingControl": rcType, "ConnectionManager": cmType}[service]
	body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body>` +
		`<u:` + action + ` xmlns:u="` + typ + `">` + args + `</u:` + action + `></s:Body></s:Envelope>`
	return body, `"` + typ + "#" + action + `"`
}

func call(t *testing.T, f *Feature, service, action, args string) (int, string) {
	t.Helper()
	body, header := soap(service, action, args)
	req := httptest.NewRequest(http.MethodPost, pathPrefix+service+"/control", strings.NewReader(body))
	req.Header.Set("SOAPACTION", header)
	req.Host = "192.0.2.20:8181" // the address the device announced
	w := httptest.NewRecorder()
	f.serve(w, req)
	return w.Code, w.Body.String()
}

// A song set by a controller is described back to it as it was given, its name, artist and length read
// from the description; the transport waits stopped for Play, and a seek is refused with UPnP's code.
func TestAControllerSetsASong(t *testing.T) {
	f := &Feature{}
	f.r.f = f
	code, body := call(t, f, "AVTransport", "SetAVTransportURI",
		`<InstanceID>0</InstanceID><CurrentURI>http://192.0.2.10/song.flac</CurrentURI><CurrentURIMetaData>`+esc(didl)+`</CurrentURIMetaData>`)
	if code != http.StatusOK {
		t.Fatalf("SetAVTransportURI: %d %s", code, body)
	}
	if f.r.cur.title != "Winding Road" || f.r.cur.artist != "The Made-Up Band" || f.r.cur.album != "Roads" ||
		f.r.cur.art != "http://192.0.2.10/art.jpg" || f.r.cur.dur != 205500*time.Millisecond {
		t.Errorf("song = %+v", f.r.cur)
	}
	_, body = call(t, f, "AVTransport", "GetMediaInfo", `<InstanceID>0</InstanceID>`)
	if !strings.Contains(body, "<CurrentURI>http://192.0.2.10/song.flac</CurrentURI>") || !strings.Contains(body, "<MediaDuration>0:03:25</MediaDuration>") {
		t.Errorf("GetMediaInfo: %s", body)
	}
	_, body = call(t, f, "AVTransport", "GetTransportInfo", `<InstanceID>0</InstanceID>`)
	if !strings.Contains(body, "<CurrentTransportState>STOPPED</CurrentTransportState>") {
		t.Errorf("GetTransportInfo: %s", body)
	}
	if code, body = call(t, f, "AVTransport", "Seek", `<InstanceID>0</InstanceID><Unit>REL_TIME</Unit><Target>0:01:00</Target>`); code != 500 || !strings.Contains(body, "<errorCode>710</errorCode>") {
		t.Errorf("Seek: %d %s", code, body)
	}
	if code, _ = call(t, f, "AVTransport", "SetAVTransportURI", `<InstanceID>0</InstanceID><CurrentURI>file:///etc/passwd</CurrentURI>`); code != 500 {
		t.Errorf("a file address was taken: %d", code)
	}
	if code, _ = call(t, f, "AVTransport", "Play", `<InstanceID>7</InstanceID>`); code != 500 {
		t.Errorf("a second instance was played: %d", code)
	}
}

// An action whose header names another one, or another service, is refused rather than run.
func TestAnActionMustMatchItsHeader(t *testing.T) {
	body, _ := soap("AVTransport", "Stop", `<InstanceID>0</InstanceID>`)
	if _, _, err := readAction(strings.NewReader(body), `"`+avtType+`#Play"`, avtType); err == nil {
		t.Error("Stop was read as Play")
	}
	if _, _, err := readAction(strings.NewReader(body), `"`+rcType+`#Stop"`, avtType); err == nil {
		t.Error("a RenderingControl header was taken for AVTransport")
	}
	name, args, err := readAction(strings.NewReader(body), `"`+avtType+`#Stop"`, avtType)
	if err != nil || name != "Stop" || args["InstanceID"] != "0" {
		t.Errorf("readAction = %q %v %v", name, args, err)
	}
}

// The descriptions are well-formed XML, and name the device and where its services are.
func TestTheDescriptionsAreXML(t *testing.T) {
	for _, doc := range []string{deviceXML(), scpd(avtActions, avtVars), scpd(rcActions, rcVars), scpd(cmActions, cmVars)} {
		d := xml.NewDecoder(strings.NewReader(doc))
		for {
			_, err := d.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("%v in %.80s", err, doc)
			}
		}
	}
	if dev := deviceXML(); !strings.Contains(dev, "<controlURL>/dlna/AVTransport/control</controlURL>") || !strings.Contains(dev, udn()) {
		t.Errorf("device.xml: %s", dev)
	}
}

// A search for the renderer, or for everything, is understood; anything else is not.
func TestSearches(t *testing.T) {
	msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 9\r\nST: " + deviceType + "\r\n\r\n"
	st, mx, ok := parseSearch([]byte(msg))
	if !ok || st != deviceType || mx != 5 {
		t.Errorf("parseSearch = %q %d %v", st, mx, ok)
	}
	if _, _, ok := parseSearch([]byte("NOTIFY * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\n\r\n")); ok {
		t.Error("a NOTIFY was taken for a search")
	}
}

// A subscription is called back only at the subscriber's own address.
func TestASubscriptionCallsBackOnlyTheSubscriber(t *testing.T) {
	var e events
	sub := func(cb string) int {
		req := httptest.NewRequest("SUBSCRIBE", pathPrefix+"AVTransport/event", nil)
		req.RemoteAddr = "192.0.2.20:5000"
		req.Header.Set("CALLBACK", "<"+cb+">")
		req.Header.Set("NT", "upnp:event")
		w := httptest.NewRecorder()
		e.handle(w, req, "AVTransport")
		return w.Code
	}
	if code := sub("http://192.0.2.99:8080/elsewhere"); code != http.StatusPreconditionFailed {
		t.Errorf("a callback to another machine was taken: %d", code)
	}
	if code := sub("http://192.0.2.20:8080/events"); code != http.StatusOK {
		t.Errorf("the subscriber's own callback was refused: %d", code)
	}
}

func TestTimes(t *testing.T) {
	if got := parseHMS("1:02:03.250"); got != time.Hour+2*time.Minute+3250*time.Millisecond {
		t.Errorf("parseHMS = %v", got)
	}
	if got := hms(3723 * time.Second); got != "1:02:03" {
		t.Errorf("hms = %q", got)
	}
	if parseHMS("bad") != 0 {
		t.Error("a bad time was read")
	}
}

// A request by a name rather than the address (a web page that pointed its own name at the device, DNS
// rebinding) is refused; by the address, with or without a port, it is served.
func TestOnlyTheAddressIsServed(t *testing.T) {
	f := &Feature{}
	f.r.f = f
	for host, want := range map[string]int{"evil.example:8181": http.StatusForbidden, "192.0.2.20:8181": http.StatusOK, "192.0.2.20": http.StatusOK, "[2001:db8::1]:8181": http.StatusOK, "[fe80::1%25wlan0]:8181": http.StatusOK, "": http.StatusOK} {
		req := httptest.NewRequest(http.MethodGet, pathPrefix+"device.xml", nil)
		req.Host = host
		w := httptest.NewRecorder()
		f.serve(w, req)
		if w.Code != want {
			t.Errorf("Host %s: %d, want %d", host, w.Code, want)
		}
	}
}

// The device never fetches from its own addresses on the network, which would reach it over loopback.
func TestNotFromItself(t *testing.T) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Skip(err)
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if err := refuseOwn("tcp", net.JoinHostPort(n.IP.String(), "80"), nil); err == nil {
				t.Errorf("its own address %s was allowed", n.IP)
			}
		}
	}
	if err := refuseOwn("tcp", "192.0.2.99:80", nil); err != nil {
		t.Errorf("a host on the network was refused: %v", err)
	}
}
