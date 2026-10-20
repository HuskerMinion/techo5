package camstream

import (
	"context"
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// WS-Discovery: how ONVIF clients find cameras without being told an address (Home Assistant's ONVIF
// integration among them). A client sends a Probe to the multicast group; while the stream is served and
// the discovery switch is on (camstream.go; off by default), the device answers it directly with what it
// is and where its device service is (onvif.go). The answer carries the device's name, model and MAC
// address, to anyone on the network who asks: the reason the switch starts off.

const discoveryAddr = "239.255.255.250:3702"

type probe struct {
	Header struct {
		MessageID string `xml:"MessageID"`
	} `xml:"Header"`
	Body struct {
		Probe *struct {
			Types string `xml:"Types"`
		} `xml:"Probe"`
	} `xml:"Body"`
}

// wanted is whether a Probe's Types (none: anything) take in a video transmitter or a device.
func wantedTypes(types string) bool {
	f := strings.Fields(types)
	if len(f) == 0 {
		return true
	}
	for _, t := range f {
		_, local, found := strings.Cut(t, ":")
		if !found {
			local = t
		}
		if local == "NetworkVideoTransmitter" || local == "Device" {
			return true
		}
	}
	return false
}

// discover answers Probes until ctx ends.
func (f *Feature) discover(ctx context.Context) {
	group, err := net.ResolveUDPAddr("udp4", discoveryAddr)
	if err != nil {
		return
	}
	var conn *net.UDPConn
	for wait := 2 * time.Second; ; wait = min(wait*2, time.Minute) {
		if conn, err = net.ListenMulticastUDP("udp4", nil, group); err == nil {
			break
		}
		slog.Warn("camera streaming: listening for discovery failed", "err", err, "again in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	buf := make([]byte, 8192)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		var p probe
		if xml.Unmarshal(buf[:n], &p) != nil || p.Body.Probe == nil || !wantedTypes(p.Body.Probe.Types) {
			continue
		}
		if ip := from.IP; !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsLoopback() {
			continue
		}
		probeMatches(from, p.Header.MessageID)
	}
}

// probeMatches answers a Probe, from the address the device has toward whoever sent it.
func probeMatches(to *net.UDPAddr, relates string) {
	c, err := net.DialUDP("udp4", nil, to)
	if err != nil {
		return
	}
	defer c.Close()
	host := c.LocalAddr().(*net.UDPAddr).IP.String()
	msg := xml.Header + `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"` +
		` xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"` +
		` xmlns:dn="http://www.onvif.org/ver10/network/wsdl" xmlns:tds="http://www.onvif.org/ver10/device/wsdl">` +
		`<s:Header><a:MessageID>urn:uuid:` + randomUUID() + `</a:MessageID><a:RelatesTo>` + html.EscapeString(relates) + `</a:RelatesTo>` +
		`<a:To>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:To>` +
		`<a:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches</a:Action></s:Header>` +
		`<s:Body><d:ProbeMatches><d:ProbeMatch><a:EndpointReference><a:Address>` + endpoint() + `</a:Address></a:EndpointReference>` +
		`<d:Types>dn:NetworkVideoTransmitter tds:Device</d:Types><d:Scopes>` + html.EscapeString(strings.Join(scopes(), " ")) + `</d:Scopes>` +
		`<d:XAddrs>http://` + net.JoinHostPort(host, strconv.Itoa(web.Port)) + devicePath + `</d:XAddrs>` +
		`<d:MetadataVersion>1</d:MetadataVersion></d:ProbeMatch></d:ProbeMatches></s:Body></s:Envelope>`
	_, _ = c.Write([]byte(msg))
}

// scopes say what the device is, for discovery and GetScopes: Home Assistant names a discovered camera
// from name/ and keys it on mac/.
func scopes() []string {
	q := func(s string) string { return strings.ReplaceAll(url.PathEscape(s), "%2F", "-") }
	out := []string{
		"onvif://www.onvif.org/type/video_encoder",
		"onvif://www.onvif.org/Profile/Streaming",
		"onvif://www.onvif.org/name/" + q(config.Get().Device.Name),
		"onvif://www.onvif.org/hardware/" + q(layout.Model),
	}
	if m := mac(); m != "" {
		out = append(out, "onvif://www.onvif.org/mac/"+m)
	}
	return out
}

// mac is the device's Wi-Fi address, as the factory recorded it.
func mac() string {
	m, _ := layout.FactoryMAC()
	return m
}

// endpoint is the device's lasting WS-Discovery address, made from its MAC so it stays the same.
var endpoint = sync.OnceValue(func() string {
	m := strings.ReplaceAll(mac(), ":", "")
	if len(m) != 12 {
		m = fmt.Sprintf("%x", randomBytes(6))
	}
	return "urn:uuid:7ec4e050-0000-4000-8000-" + m
})

func randomUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
