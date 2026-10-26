package camstream

import (
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// ONVIF, as little of it as lets a recorder that adds cameras by ONVIF add this one: the device and media
// services on the web port (8181), one profile, its stream (the RTSP above) and, with Camera web access on,
// its snapshot. The same login as the stream, given as a WS-Security UsernameToken digest. Found by
// WS-Discovery (discovery.go) or added by its address. No PTZ, events, imaging or settings: there is
// nothing to turn, and the device is set up from Home Assistant.

const (
	devicePath = "/onvif/device_service"
	mediaPath  = "/onvif/media_service"

	// maxBody is the most a request may carry: ONVIF requests are a few kilobytes.
	maxBody = 64 << 10

	// skew is how far a login's time may be from the device's; a login is taken once within it.
	skew = 5 * time.Minute

	// readWithin is how long a request has to arrive whole, so a slow one does not hold its connection
	// open before anybody has logged in.
	readWithin = 10 * time.Second

	// maxNonces is how many logins are remembered against being replayed; past it, the oldest are let go
	// early, which only shortens the window a replay is refused for.
	maxNonces = 1024
)

func init() {
	web.Handle(devicePath, "", Get().wanted, Get().onvif)
	web.Handle(mediaPath, "", Get().wanted, Get().onvif)
}

// preAuth are the operations ONVIF answers without a login: what a client asks before it can log in.
var preAuth = map[string]bool{
	"GetSystemDateAndTime": true, "GetCapabilities": true, "GetServices": true, "GetServiceCapabilities": true,
}

// envelope is what a request says: its operation, the parts of it read here, and its login.
type envelope struct {
	Header struct {
		Security struct {
			UsernameToken struct {
				Username string `xml:"Username"`
				Password struct {
					Type  string `xml:"Type,attr"`
					Value string `xml:",chardata"`
				} `xml:"Password"`
				Nonce   string `xml:"Nonce"`
				Created string `xml:"Created"`
			} `xml:"UsernameToken"`
		} `xml:"Security"`
	} `xml:"Header"`
	Body struct {
		Inner struct {
			XMLName      xml.Name
			ProfileToken string `xml:"ProfileToken"`
		} `xml:",any"`
	} `xml:"Body"`
}

func (f *Feature) onvif(w http.ResponseWriter, r *http.Request) {
	if web.AwayRequest(r) { // never one from away, over the tailnet
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		web.Refuse(w, "ONVIF takes POST", http.StatusMethodNotAllowed)
		return
	}
	// A few requests at a time: each one's body is read and its login checked before anything else, and
	// a recorder asks one thing at a time.
	select {
	case onvifBusy <- struct{}{}:
		defer func() { <-onvifBusy }()
	default:
		web.Refuse(w, "busy; try again", http.StatusServiceUnavailable)
		return
	}
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(readWithin))
	b, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(b) > maxBody {
		// What is left of the body is not waited for (web.Refuse), so a slow sender holds nothing.
		_ = http.NewResponseController(w).SetReadDeadline(time.Now())
		w.Header().Set("Connection", "close")
		fault(w, http.StatusBadRequest, "ter:InvalidArgs", "the request is too long or cut short")
		return
	}
	var env envelope
	if err := xml.Unmarshal(b, &env); err != nil {
		fault(w, http.StatusBadRequest, "ter:WellFormed", "the request is not XML")
		return
	}
	op := env.Body.Inner.XMLName.Local
	if !preAuth[op] {
		ok, done := f.guard.attempt(remoteHost(r))
		if !ok {
			w.Header().Set("Connection", "close")
			fault(w, http.StatusBadRequest, "ter:NotAuthorized", "too many wrong logins: try again later")
			return
		}
		good := false
		defer func() { done(!good) }() // counted however the handler ends
		now := time.Now()
		var nonce []byte
		good, nonce = tokenOK(env, now)
		good = good && f.nonces.fresh(string(nonce), now)
		if !good {
			w.Header().Set("Connection", "close")
			fault(w, http.StatusBadRequest, "ter:NotAuthorized", "the login is wrong")
			return
		}
	}
	body, ok := f.answer(op, env.Body.Inner.ProfileToken, requestHost(r), r.URL.Path == mediaPath)
	if !ok {
		fault(w, http.StatusBadRequest, "ter:ActionNotSupported", op+" is not offered")
		return
	}
	reply(w, http.StatusOK, body)
}

// answer is an operation's response body, or false for one not offered.
func (f *Feature) answer(op, profile, host string, media bool) (string, bool) {
	deviceURL := "http://" + net.JoinHostPort(host, strconv.Itoa(web.Port)) + devicePath
	mediaURL := "http://" + net.JoinHostPort(host, strconv.Itoa(web.Port)) + mediaPath
	streamURL := "rtsp://" + net.JoinHostPort(host, Port) + Path
	if profile != "" && profile != "main" && (op == "GetStreamUri" || op == "GetSnapshotUri" || op == "GetProfile") {
		return "", false
	}
	switch op {
	case "GetSystemDateAndTime":
		now := time.Now().UTC()
		return fmt.Sprintf(`<tds:GetSystemDateAndTimeResponse><tds:SystemDateAndTime>
<tt:DateTimeType>NTP</tt:DateTimeType><tt:DaylightSavings>false</tt:DaylightSavings>
<tt:UTCDateTime><tt:Time><tt:Hour>%d</tt:Hour><tt:Minute>%d</tt:Minute><tt:Second>%d</tt:Second></tt:Time>
<tt:Date><tt:Year>%d</tt:Year><tt:Month>%d</tt:Month><tt:Day>%d</tt:Day></tt:Date></tt:UTCDateTime>
</tds:SystemDateAndTime></tds:GetSystemDateAndTimeResponse>`,
			now.Hour(), now.Minute(), now.Second(), now.Year(), int(now.Month()), now.Day()), true
	case "GetCapabilities":
		return `<tds:GetCapabilitiesResponse><tds:Capabilities>
<tt:Device><tt:XAddr>` + deviceURL + `</tt:XAddr></tt:Device>
<tt:Media><tt:XAddr>` + mediaURL + `</tt:XAddr><tt:StreamingCapabilities><tt:RTPMulticast>false</tt:RTPMulticast>
<tt:RTP_TCP>true</tt:RTP_TCP><tt:RTP_RTSP_TCP>true</tt:RTP_RTSP_TCP></tt:StreamingCapabilities></tt:Media>
</tds:Capabilities></tds:GetCapabilitiesResponse>`, true
	case "GetServices":
		return `<tds:GetServicesResponse>
<tds:Service><tds:Namespace>http://www.onvif.org/ver10/device/wsdl</tds:Namespace><tds:XAddr>` + deviceURL + `</tds:XAddr>
<tds:Version><tt:Major>2</tt:Major><tt:Minor>0</tt:Minor></tds:Version></tds:Service>
<tds:Service><tds:Namespace>http://www.onvif.org/ver10/media/wsdl</tds:Namespace><tds:XAddr>` + mediaURL + `</tds:XAddr>
<tds:Version><tt:Major>2</tt:Major><tt:Minor>0</tt:Minor></tds:Version></tds:Service>
</tds:GetServicesResponse>`, true
	case "GetServiceCapabilities":
		if media {
			return `<trt:GetServiceCapabilitiesResponse><trt:Capabilities SnapshotUri="` +
				strconv.FormatBool(config.Get().Security.Camera) + `" Rotation="false">` +
				`<trt:ProfileCapabilities MaximumNumberOfProfiles="1"/>` +
				`<trt:StreamingCapabilities RTPMulticast="false" RTP_TCP="true" RTP_RTSP_TCP="true"/>` +
				`</trt:Capabilities></trt:GetServiceCapabilitiesResponse>`, true
		}
		return `<tds:GetServiceCapabilitiesResponse><tds:Capabilities><tds:Network/><tds:Security UsernameToken="true"/>
<tds:System/></tds:Capabilities></tds:GetServiceCapabilitiesResponse>`, true
	case "GetDeviceInformation":
		return `<tds:GetDeviceInformationResponse><tds:Manufacturer>TECHO5</tds:Manufacturer><tds:Model>` +
			html.EscapeString(layout.Model) + `</tds:Model><tds:FirmwareVersion>` + html.EscapeString(layout.Version) +
			`</tds:FirmwareVersion><tds:SerialNumber>` + serialNumber(config.Get().Device.Name) +
			`</tds:SerialNumber><tds:HardwareId>TECHO5</tds:HardwareId></tds:GetDeviceInformationResponse>`, true
	case "GetScopes":
		var b strings.Builder
		for _, sc := range scopes() {
			b.WriteString(scope(sc))
		}
		return `<tds:GetScopesResponse>` + b.String() + `</tds:GetScopesResponse>`, true
	case "GetHostname":
		// The name the device answers to on the network (mDNS, ESPHome), not its display name.
		return `<tds:GetHostnameResponse><tds:HostnameInformation><tt:FromDHCP>false</tt:FromDHCP><tt:Name>` +
			hostname(config.Get().Device.Name, mac()) + `</tt:Name></tds:HostnameInformation></tds:GetHostnameResponse>`, true
	case "GetNetworkInterfaces":
		// Home Assistant keys an ONVIF camera on the address here.
		addr := strings.Trim(host, "[]")
		prefix, fixed := ipv4Setting(addr)
		from := "FromDHCP"
		if fixed {
			from = "Manual"
		}
		return `<tds:GetNetworkInterfacesResponse><tds:NetworkInterfaces token="wlan0"><tt:Enabled>true</tt:Enabled>
<tt:Info><tt:Name>wlan0</tt:Name><tt:HwAddress>` + mac() + `</tt:HwAddress><tt:MTU>1500</tt:MTU></tt:Info>
<tt:IPv4><tt:Enabled>true</tt:Enabled><tt:Config><tt:` + from + `><tt:Address>` + html.EscapeString(addr) +
			`</tt:Address><tt:PrefixLength>` + strconv.Itoa(prefix) + `</tt:PrefixLength></tt:` + from + `><tt:DHCP>` +
			strconv.FormatBool(!fixed) + `</tt:DHCP></tt:Config></tt:IPv4>
</tds:NetworkInterfaces></tds:GetNetworkInterfacesResponse>`, true
	case "GetProfiles":
		return `<trt:GetProfilesResponse>` + profileXML("trt:Profiles") + `</trt:GetProfilesResponse>`, true
	case "GetProfile":
		return `<trt:GetProfileResponse>` + profileXML("trt:Profile") + `</trt:GetProfileResponse>`, true
	case "GetVideoSources":
		w, h := size()
		return fmt.Sprintf(`<trt:GetVideoSourcesResponse><trt:VideoSources token="camera"><tt:Framerate>%d</tt:Framerate>
<tt:Resolution><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:Resolution></trt:VideoSources>
</trt:GetVideoSourcesResponse>`, maxFPS, w, h), true
	case "GetStreamUri":
		return `<trt:GetStreamUriResponse><trt:MediaUri><tt:Uri>` + html.EscapeString(streamURL) + `</tt:Uri>
<tt:InvalidAfterConnect>false</tt:InvalidAfterConnect><tt:InvalidAfterReboot>false</tt:InvalidAfterReboot>
<tt:Timeout>PT0S</tt:Timeout></trt:MediaUri></trt:GetStreamUriResponse>`, true
	case "GetSnapshotUri":
		// The snapshot is the camera page's, there only with Camera web access on.
		if !config.Get().Security.Camera {
			return "", false
		}
		snap := "http://" + net.JoinHostPort(host, strconv.Itoa(web.Port)) + "/camera.jpg"
		return `<trt:GetSnapshotUriResponse><trt:MediaUri><tt:Uri>` + html.EscapeString(snap) + `</tt:Uri>
<tt:InvalidAfterConnect>false</tt:InvalidAfterConnect><tt:InvalidAfterReboot>false</tt:InvalidAfterReboot>
<tt:Timeout>PT0S</tt:Timeout></trt:MediaUri></trt:GetSnapshotUriResponse>`, true
	}
	return "", false
}

// hostname is the device's name on the network, as mDNS and ESPHome have it (layout.SlugOr): "Kitchen
// Echo" is kitchen-echo. One DNS label, so at most 63 characters.
func hostname(name, mac string) string {
	h := layout.SlugOr(name, mac)
	return strings.TrimRight(h[:min(len(h), 63)], "-")
}

// ipv4Setting is the prefix length of the device's address addr, as the interface that has it says (24
// when none does), and whether the address is a fixed one rather than DHCP's (lib/wifi).
func ipv4Setting(addr string) (prefix int, fixed bool) {
	prefix = 24
	if ip := net.ParseIP(addr); ip != nil {
		if as, err := net.InterfaceAddrs(); err == nil {
			for _, a := range as {
				if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
					prefix, _ = n.Mask.Size()
					break
				}
			}
		}
	}
	return prefix, wifi.AddressSupported() && wifi.LoadAddress().Fixed
}

func scope(s string) string {
	return `<tds:Scopes><tt:ScopeDef>Fixed</tt:ScopeDef><tt:ScopeItem>` + html.EscapeString(s) + `</tt:ScopeItem></tds:Scopes>`
}

// profileXML is the one profile: the camera, as JPEG.
func profileXML(tag string) string {
	w, h := size()
	return fmt.Sprintf(`<%s token="main" fixed="true"><tt:Name>main</tt:Name>
<tt:VideoSourceConfiguration token="camera"><tt:Name>camera</tt:Name><tt:UseCount>1</tt:UseCount>
<tt:SourceToken>camera</tt:SourceToken><tt:Bounds x="0" y="0" width="%d" height="%d"/></tt:VideoSourceConfiguration>
<tt:VideoEncoderConfiguration token="jpeg"><tt:Name>jpeg</tt:Name><tt:UseCount>1</tt:UseCount>
<tt:Encoding>JPEG</tt:Encoding><tt:Resolution><tt:Width>%d</tt:Width><tt:Height>%d</tt:Height></tt:Resolution>
<tt:Quality>%d</tt:Quality><tt:RateControl><tt:FrameRateLimit>%d</tt:FrameRateLimit><tt:EncodingInterval>1</tt:EncodingInterval>
<tt:BitrateLimit>0</tt:BitrateLimit></tt:RateControl><tt:SessionTimeout>PT60S</tt:SessionTimeout></tt:VideoEncoderConfiguration>
</%s>`, tag, w, h, w, h, quality, maxFPS, tag)
}

// tokenOK is whether a request's UsernameToken is the stream's login, as a digest made within skew of now,
// and the nonce it was made with, as the digest read it: that, not the text it was written as, is what a
// replay repeats. A password sent as plain text is refused: this is plain HTTP.
func tokenOK(env envelope, now time.Time) (ok bool, nonce []byte) {
	user, pass := config.Get().CamStream.Login()
	t := env.Header.Security.UsernameToken
	if pass == "" || t.Username != user || !strings.HasSuffix(t.Password.Type, "#PasswordDigest") {
		return false, nil
	}
	created, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(t.Created))
	if err != nil || created.Sub(now) > skew || now.Sub(created) > skew {
		return false, nil
	}
	nonce, err = base64.StdEncoding.DecodeString(strings.TrimSpace(t.Nonce))
	if err != nil || len(nonce) == 0 {
		return false, nil
	}
	signed := make([]byte, 0, len(nonce)+len(t.Created)+len(pass))
	signed = append(append(append(signed, nonce...), strings.TrimSpace(t.Created)...), pass...)
	sum := sha1.Sum(signed)
	want := base64.StdEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(want), []byte(strings.TrimSpace(t.Password.Value))) == 1, nonce
}

// nonces are the logins taken within skew, by nonce, so none is taken twice.
type nonces struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// fresh is whether a nonce has not been taken within skew, and takes it.
func (n *nonces) fresh(nonce string, now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.seen == nil {
		n.seen = map[string]time.Time{}
	}
	if at, ok := n.seen[nonce]; ok && now.Sub(at) < 2*skew {
		return false
	}
	if len(n.seen) >= maxNonces {
		for k, at := range n.seen {
			if now.Sub(at) >= 2*skew {
				delete(n.seen, k)
			}
		}
		for k := range n.seen {
			if len(n.seen) < maxNonces {
				break
			}
			delete(n.seen, k)
		}
	}
	n.seen[nonce] = now
	return true
}

func remoteHost(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

// requestHost is the device's address as the client reached it, for the addresses handed back.
func requestHost(r *http.Request) string {
	if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
		if h, _, err := net.SplitHostPort(a.String()); err == nil {
			return h
		}
	}
	h, _, err := net.SplitHostPort(r.Host)
	if err != nil {
		return r.Host
	}
	return h
}

const soapHead = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tds="http://www.onvif.org/ver10/device/wsdl" ` +
	`xmlns:trt="http://www.onvif.org/ver10/media/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema" ` +
	`xmlns:ter="http://www.onvif.org/ver10/error"><s:Body>`

func reply(w http.ResponseWriter, status int, body string) {
	var b bytes.Buffer
	b.WriteString(soapHead)
	b.WriteString(body)
	b.WriteString(`</s:Body></s:Envelope>`)
	w.Header().Set("Content-Type", "application/soap+xml; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(b.Bytes())
}

func fault(w http.ResponseWriter, status int, subcode, reason string) {
	reply(w, status, `<s:Fault><s:Code><s:Value>s:Sender</s:Value><s:Subcode><s:Value>`+subcode+
		`</s:Value></s:Subcode></s:Code><s:Reason><s:Text xml:lang="en">`+html.EscapeString(reason)+
		`</s:Text></s:Reason></s:Fault>`)
}

// onvifBusy holds a place for each ONVIF request being answered.
var onvifBusy = make(chan struct{}, maxONVIF)

// maxONVIF is how many ONVIF requests are answered at once.
const maxONVIF = 4

// serialNumber is what a recorder is told the device's serial number is: steady for the device, so the
// recorder knows it again, and not its name, which is often a room.
func serialNumber(name string) string {
	sum := sha256.Sum256([]byte("techo5-onvif serial:" + name))
	return "TECHO5-" + strings.ToUpper(hex.EncodeToString(sum[:6]))
}
