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
	"unicode"

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
	lim := newLimiter()
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		// Who asked is looked at before what was asked: a forged sender costs no parsing, and is not
		// answered more than a few times a second.
		if !answerable(from.IP) || !lim.allow(from.IP, time.Now()) {
			continue
		}
		var p probe
		if xml.Unmarshal(buf[:n], &p) != nil || p.Body.Probe == nil || !wantedTypes(p.Body.Probe.Types) {
			continue
		}
		answerProbe(from, p.Header.MessageID)
	}
}

// answerable is whether a Probe from ip is answered: from the local network only.
func answerable(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback()
}

// A Probe's sender can be forged, and the answer is larger than the Probe, so each sender is answered at
// most perSource times a second (a burst of burst), and all of them together at most total; no more than
// sources senders are kept track of.
const (
	perSource = 2
	burst     = 4
	total     = 20
	sources   = 256
)

type bucket struct {
	tokens float64
	last   time.Time
}

// limiter is token buckets, one per sender and one for everyone. It is used by one goroutine only.
type limiter struct {
	all  bucket
	from map[string]*bucket
}

func newLimiter() *limiter {
	return &limiter{all: bucket{tokens: total}, from: map[string]*bucket{}}
}

// take is whether b has a token left, refilled at rate a second up to most since it was last asked.
func (b *bucket) take(now time.Time, rate, most float64) bool {
	if !b.last.IsZero() {
		b.tokens = min(most, b.tokens+now.Sub(b.last).Seconds()*rate)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// allow is whether a Probe from ip is answered at now.
func (l *limiter) allow(ip net.IP, now time.Time) bool {
	key := ip.String()
	b := l.from[key]
	if b == nil {
		if len(l.from) >= sources {
			l.evict(now)
		}
		b = &bucket{tokens: burst}
		l.from[key] = b
	}
	if !b.take(now, perSource, burst) {
		return false
	}
	return l.all.take(now, total, total)
}

// evict makes room for a new sender: the ones whose bucket has refilled are forgotten (they start full
// again anyway), and if none has, the one heard from longest ago.
func (l *limiter) evict(now time.Time) {
	var oldest string
	for k, b := range l.from {
		if now.Sub(b.last) >= burst*time.Second/perSource {
			delete(l.from, k)
		} else if oldest == "" || b.last.Before(l.from[oldest].last) {
			oldest = k
		}
	}
	if len(l.from) >= sources {
		delete(l.from, oldest)
	}
}

// answerProbe answers a Probe, from the address the device has toward whoever sent it.
func answerProbe(to *net.UDPAddr, relates string) {
	c, err := net.DialUDP("udp4", nil, to)
	if err != nil {
		return
	}
	defer c.Close()
	host := c.LocalAddr().(*net.UDPAddr).IP.String()
	_, _ = c.Write([]byte(probeMatch(randomUUID(), relates, host, scopes())))
}

// probeMatch is the ProbeMatches message, its own id messageID, answering relates, with the device
// service at host.
func probeMatch(messageID, relates, host string, scopes []string) string {
	return xml.Header + `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"` +
		` xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"` +
		` xmlns:dn="http://www.onvif.org/ver10/network/wsdl" xmlns:tds="http://www.onvif.org/ver10/device/wsdl">` +
		`<s:Header><a:MessageID>urn:uuid:` + messageID + `</a:MessageID><a:RelatesTo>` + html.EscapeString(relates) + `</a:RelatesTo>` +
		`<a:To>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:To>` +
		`<a:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches</a:Action></s:Header>` +
		`<s:Body><d:ProbeMatches><d:ProbeMatch><a:EndpointReference><a:Address>` + endpoint() + `</a:Address></a:EndpointReference>` +
		`<d:Types>dn:NetworkVideoTransmitter tds:Device</d:Types><d:Scopes>` + html.EscapeString(strings.Join(scopes, " ")) + `</d:Scopes>` +
		`<d:XAddrs>http://` + net.JoinHostPort(host, strconv.Itoa(web.Port)) + devicePath + `</d:XAddrs>` +
		`<d:MetadataVersion>1</d:MetadataVersion></d:ProbeMatch></d:ProbeMatches></s:Body></s:Envelope>`
}

// scopes say what the device is, for discovery and GetScopes: Home Assistant names a discovered camera
// from name/ and keys it on mac/.
func scopes() []string {
	return scopeList(config.Get().Device.Name, layout.Model, mac())
}

func scopeList(name, model, mac string) []string {
	out := []string{
		"onvif://www.onvif.org/type/video_encoder",
		"onvif://www.onvif.org/Profile/Streaming",
		"onvif://www.onvif.org/name/" + scopeWord(name),
		"onvif://www.onvif.org/hardware/" + scopeWord(model),
	}
	if mac != "" {
		out = append(out, "onvif://www.onvif.org/mac/"+mac)
	}
	return out
}

// scopeWord is a name as one segment of a scope: spaces, as cameras write them, and anything else a
// scope or a list of them would trip on become "_", so "Kitchen Echo" is "Kitchen_Echo", not
// "Kitchen%20Echo". Letters in other scripts are kept, escaped.
func scopeWord(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("-._~", r) {
			return r
		}
		return '_'
	}, strings.TrimSpace(s))
	return url.PathEscape(s)
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
