package dlna

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/feature/video"
)

// The web port side: the device's description, each service's description (its actions), and the
// actions themselves, as SOAP. Eventing (SUBSCRIBE) is in events.go.

// mostSOAP bounds a request: an action and a song's description are a few kilobytes.
const mostSOAP = 64 << 10

func (f *Feature) serve(w http.ResponseWriter, r *http.Request) {
	if !hostIsAddress(r.Host) {
		// Controllers use the address the device announced. A name here is a web page that pointed
		// a name of its own at the device (DNS rebinding) to reach it from a browser: refused.
		http.Error(w, "use the device's address", http.StatusForbidden)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, pathPrefix)
	switch {
	case path == "device.xml" && r.Method == http.MethodGet:
		writeXML(w, deviceXML())
	case path == "AVTransport.xml" && r.Method == http.MethodGet:
		writeXML(w, scpd(avtActions, avtVars))
	case path == "RenderingControl.xml" && r.Method == http.MethodGet:
		writeXML(w, scpd(rcActions, rcVars))
	case path == "ConnectionManager.xml" && r.Method == http.MethodGet:
		writeXML(w, scpd(cmActions, cmVars))
	case strings.HasSuffix(path, "/control") && r.Method == http.MethodPost:
		f.control(w, r, strings.TrimSuffix(path, "/control"))
	case strings.HasSuffix(path, "/event"):
		f.e.handle(w, r, strings.TrimSuffix(path, "/event"))
	default:
		http.NotFound(w, r)
	}
}

// hostIsAddress is whether a request's Host is an IP address, with or without a port.
func hostIsAddress(host string) bool {
	if host == "" {
		return true // an old client that sends none; a browser, which rebinding needs, always does
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host, _, _ = strings.Cut(strings.Trim(host, "[]"), "%") // fe80::1%wlan0: the zone is not the address
	return net.ParseIP(host) != nil
}

func writeXML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	_, _ = io.WriteString(w, xml.Header+body)
}

func esc(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func deviceXML() string {
	service := func(kind, typ string) string {
		return "<service><serviceType>" + typ + "</serviceType><serviceId>urn:upnp-org:serviceId:" + kind + "</serviceId>" +
			"<SCPDURL>" + pathPrefix + kind + ".xml</SCPDURL><controlURL>" + pathPrefix + kind + "/control</controlURL>" +
			"<eventSubURL>" + pathPrefix + kind + "/event</eventSubURL></service>"
	}
	return `<root xmlns="urn:schemas-upnp-org:device-1-0" xmlns:dlna="urn:schemas-dlna-org:device-1-0">` +
		`<specVersion><major>1</major><minor>0</minor></specVersion><device>` +
		`<deviceType>` + deviceType + `</deviceType>` +
		`<friendlyName>` + esc(friendlyName()) + `</friendlyName>` +
		`<manufacturer>TECHO5</manufacturer><manufacturerURL>https://github.com/HuskerMinion/techo5</manufacturerURL>` +
		`<modelName>TECHO5</modelName><modelDescription>TECHO5 speaker</modelDescription>` +
		`<UDN>` + udn() + `</UDN>` +
		`<dlna:X_DLNADOC>DMR-1.50</dlna:X_DLNADOC>` +
		`<serviceList>` + service("AVTransport", avtType) + service("RenderingControl", rcType) +
		service("ConnectionManager", cmType) + `</serviceList></device></root>`
}

// An action's arguments: name, direction and the state variable it relates to.
type arg struct{ name, dir, v string }

type action struct {
	name string
	args []arg
}

// A state variable: name, type, whether it sends events, and the values it may take.
type stateVar struct {
	name, typ string
	events    bool
	allowed   []string
}

func in(name, v string) arg  { return arg{name, "in", v} }
func out(name, v string) arg { return arg{name, "out", v} }

var instance = in("InstanceID", "A_ARG_TYPE_InstanceID")

var avtActions = []action{
	{"SetAVTransportURI", []arg{instance, in("CurrentURI", "AVTransportURI"), in("CurrentURIMetaData", "AVTransportURIMetaData")}},
	{"SetNextAVTransportURI", []arg{instance, in("NextURI", "NextAVTransportURI"), in("NextURIMetaData", "NextAVTransportURIMetaData")}},
	{"GetMediaInfo", []arg{instance, out("NrTracks", "NumberOfTracks"), out("MediaDuration", "CurrentMediaDuration"),
		out("CurrentURI", "AVTransportURI"), out("CurrentURIMetaData", "AVTransportURIMetaData"),
		out("NextURI", "NextAVTransportURI"), out("NextURIMetaData", "NextAVTransportURIMetaData"),
		out("PlayMedium", "PlaybackStorageMedium"), out("RecordMedium", "RecordStorageMedium"), out("WriteStatus", "RecordMediumWriteStatus")}},
	{"GetTransportInfo", []arg{instance, out("CurrentTransportState", "TransportState"),
		out("CurrentTransportStatus", "TransportStatus"), out("CurrentSpeed", "TransportPlaySpeed")}},
	{"GetPositionInfo", []arg{instance, out("Track", "CurrentTrack"), out("TrackDuration", "CurrentTrackDuration"),
		out("TrackMetaData", "CurrentTrackMetaData"), out("TrackURI", "CurrentTrackURI"), out("RelTime", "RelativeTimePosition"),
		out("AbsTime", "AbsoluteTimePosition"), out("RelCount", "RelativeCounterPosition"), out("AbsCount", "AbsoluteCounterPosition")}},
	{"GetDeviceCapabilities", []arg{instance, out("PlayMedia", "PossiblePlaybackStorageMedia"),
		out("RecMedia", "PossibleRecordStorageMedia"), out("RecQualityModes", "PossibleRecordQualityModes")}},
	{"GetTransportSettings", []arg{instance, out("PlayMode", "CurrentPlayMode"), out("RecQualityMode", "CurrentRecordQualityMode")}},
	{"GetCurrentTransportActions", []arg{instance, out("Actions", "CurrentTransportActions")}},
	{"Stop", []arg{instance}},
	{"Play", []arg{instance, in("Speed", "TransportPlaySpeed")}},
	{"Pause", []arg{instance}},
	{"Seek", []arg{instance, in("Unit", "A_ARG_TYPE_SeekMode"), in("Target", "A_ARG_TYPE_SeekTarget")}},
	{"Next", []arg{instance}},
	{"Previous", []arg{instance}},
}

var avtVars = []stateVar{
	{"TransportState", "string", false, []string{"STOPPED", "PLAYING", "PAUSED_PLAYBACK", "TRANSITIONING", "NO_MEDIA_PRESENT"}},
	{"TransportStatus", "string", false, []string{"OK", "ERROR_OCCURRED"}},
	{"TransportPlaySpeed", "string", false, []string{"1"}},
	{"PlaybackStorageMedium", "string", false, []string{"NETWORK", "NONE"}},
	{"PossiblePlaybackStorageMedia", "string", false, nil},
	{"RecordStorageMedium", "string", false, []string{"NOT_IMPLEMENTED"}},
	{"PossibleRecordStorageMedia", "string", false, nil},
	{"RecordMediumWriteStatus", "string", false, []string{"NOT_IMPLEMENTED"}},
	{"PossibleRecordQualityModes", "string", false, nil},
	{"CurrentRecordQualityMode", "string", false, []string{"NOT_IMPLEMENTED"}},
	{"CurrentPlayMode", "string", false, []string{"NORMAL"}},
	{"NumberOfTracks", "ui4", false, nil},
	{"CurrentTrack", "ui4", false, nil},
	{"CurrentTrackDuration", "string", false, nil},
	{"CurrentMediaDuration", "string", false, nil},
	{"CurrentTrackMetaData", "string", false, nil},
	{"CurrentTrackURI", "string", false, nil},
	{"AVTransportURI", "string", false, nil},
	{"AVTransportURIMetaData", "string", false, nil},
	{"NextAVTransportURI", "string", false, nil},
	{"NextAVTransportURIMetaData", "string", false, nil},
	{"RelativeTimePosition", "string", false, nil},
	{"AbsoluteTimePosition", "string", false, nil},
	{"RelativeCounterPosition", "i4", false, nil},
	{"AbsoluteCounterPosition", "i4", false, nil},
	{"CurrentTransportActions", "string", false, nil},
	{"LastChange", "string", true, nil},
	{"A_ARG_TYPE_SeekMode", "string", false, []string{"REL_TIME", "TRACK_NR"}},
	{"A_ARG_TYPE_SeekTarget", "string", false, nil},
	{"A_ARG_TYPE_InstanceID", "ui4", false, nil},
}

var rcActions = []action{
	{"GetVolume", []arg{instance, in("Channel", "A_ARG_TYPE_Channel"), out("CurrentVolume", "Volume")}},
	{"SetVolume", []arg{instance, in("Channel", "A_ARG_TYPE_Channel"), in("DesiredVolume", "Volume")}},
	{"GetMute", []arg{instance, in("Channel", "A_ARG_TYPE_Channel"), out("CurrentMute", "Mute")}},
	{"SetMute", []arg{instance, in("Channel", "A_ARG_TYPE_Channel"), in("DesiredMute", "Mute")}},
	{"ListPresets", []arg{instance, out("CurrentPresetNameList", "PresetNameList")}},
	{"SelectPreset", []arg{instance, in("PresetName", "A_ARG_TYPE_PresetName")}},
}

var rcVars = []stateVar{
	{"Volume", "ui2", false, nil},
	{"Mute", "boolean", false, nil},
	{"PresetNameList", "string", false, nil},
	{"LastChange", "string", true, nil},
	{"A_ARG_TYPE_Channel", "string", false, []string{"Master"}},
	{"A_ARG_TYPE_PresetName", "string", false, []string{"FactoryDefaults"}},
	{"A_ARG_TYPE_InstanceID", "ui4", false, nil},
}

var cmActions = []action{
	{"GetProtocolInfo", []arg{out("Source", "SourceProtocolInfo"), out("Sink", "SinkProtocolInfo")}},
	{"GetCurrentConnectionIDs", []arg{out("ConnectionIDs", "CurrentConnectionIDs")}},
	{"GetCurrentConnectionInfo", []arg{in("ConnectionID", "A_ARG_TYPE_ConnectionID"), out("RcsID", "A_ARG_TYPE_RcsID"),
		out("AVTransportID", "A_ARG_TYPE_AVTransportID"), out("ProtocolInfo", "A_ARG_TYPE_ProtocolInfo"),
		out("PeerConnectionManager", "A_ARG_TYPE_ConnectionManager"), out("PeerConnectionID", "A_ARG_TYPE_ConnectionID"),
		out("Direction", "A_ARG_TYPE_Direction"), out("Status", "A_ARG_TYPE_ConnectionStatus")}},
}

var cmVars = []stateVar{
	{"SourceProtocolInfo", "string", true, nil},
	{"SinkProtocolInfo", "string", true, nil},
	{"CurrentConnectionIDs", "string", true, nil},
	{"A_ARG_TYPE_ConnectionStatus", "string", false, []string{"OK", "ContentFormatMismatch", "InsufficientBandwidth", "UnreliableChannel", "Unknown"}},
	{"A_ARG_TYPE_ConnectionManager", "string", false, nil},
	{"A_ARG_TYPE_Direction", "string", false, []string{"Input", "Output"}},
	{"A_ARG_TYPE_ProtocolInfo", "string", false, nil},
	{"A_ARG_TYPE_ConnectionID", "i4", false, nil},
	{"A_ARG_TYPE_AVTransportID", "i4", false, nil},
	{"A_ARG_TYPE_RcsID", "i4", false, nil},
}

func scpd(actions []action, vars []stateVar) string {
	var b strings.Builder
	b.WriteString(`<scpd xmlns="urn:schemas-upnp-org:service-1-0"><specVersion><major>1</major><minor>0</minor></specVersion><actionList>`)
	for _, a := range actions {
		b.WriteString("<action><name>" + a.name + "</name><argumentList>")
		for _, g := range a.args {
			b.WriteString("<argument><name>" + g.name + "</name><direction>" + g.dir + "</direction><relatedStateVariable>" + g.v + "</relatedStateVariable></argument>")
		}
		b.WriteString("</argumentList></action>")
	}
	b.WriteString("</actionList><serviceStateTable>")
	for _, v := range vars {
		ev := "no"
		if v.events {
			ev = "yes"
		}
		b.WriteString(`<stateVariable sendEvents="` + ev + `"><name>` + v.name + `</name><dataType>` + v.typ + `</dataType>`)
		if len(v.allowed) > 0 {
			b.WriteString("<allowedValueList>")
			for _, a := range v.allowed {
				b.WriteString("<allowedValue>" + a + "</allowedValue>")
			}
			b.WriteString("</allowedValueList>")
		}
		b.WriteString("</stateVariable>")
	}
	b.WriteString("</serviceStateTable></scpd>")
	return b.String()
}

// soapError is a UPnP error: a code from the spec and its words.
type soapError struct {
	code int
	desc string
}

func (e soapError) Error() string { return fmt.Sprintf("%d %s", e.code, e.desc) }

var (
	errInvalidAction = soapError{401, "Invalid Action"}
	errInvalidArgs   = soapError{402, "Invalid Args"}
	errNotSupported  = soapError{710, "Seek mode not supported"}
	errNoContents    = soapError{701, "Transition not available"}
	errNotVideo      = soapError{714, "Illegal MIME-type"}
)

// control runs one action on a service.
func (f *Feature) control(w http.ResponseWriter, r *http.Request, service string) {
	typ := map[string]string{"AVTransport": avtType, "RenderingControl": rcType, "ConnectionManager": cmType}[service]
	if typ == "" {
		http.NotFound(w, r)
		return
	}
	name, args, err := readAction(io.LimitReader(r.Body, mostSOAP), r.Header.Get("SOAPACTION"), typ)
	if err != nil {
		soapFault(w, errInvalidAction)
		return
	}
	var out []kv
	switch service {
	case "AVTransport":
		out, err = f.r.avTransport(name, args, remoteHost(r))
	case "RenderingControl":
		out, err = renderingControl(name, args)
	case "ConnectionManager":
		out, err = connectionManager(name)
	}
	if err != nil {
		se, ok := err.(soapError)
		if !ok {
			slog.Info("dlna: an action failed", "action", name, "err", err)
			se = soapError{501, "Action Failed"}
		}
		soapFault(w, se)
		return
	}
	var b strings.Builder
	b.WriteString(`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>`)
	b.WriteString(`<u:` + name + `Response xmlns:u="` + typ + `">`)
	for _, p := range out {
		b.WriteString("<" + p.k + ">" + esc(p.v) + "</" + p.k + ">")
	}
	b.WriteString(`</u:` + name + `Response></s:Body></s:Envelope>`)
	writeXML(w, b.String())
}

type kv struct{ k, v string }

// readAction reads a SOAP request: the action's name (which must match the SOAPACTION header's, and
// be on the service the request was sent to) and its arguments by name.
func readAction(body io.Reader, header, service string) (string, map[string]string, error) {
	h := strings.Trim(strings.TrimSpace(header), `"`)
	typ, want, ok := strings.Cut(h, "#")
	if !ok || typ != service {
		return "", nil, fmt.Errorf("SOAPACTION %q is not for %s", header, service)
	}
	d := xml.NewDecoder(body)
	args := map[string]string{}
	depth, actionDepth := 0, -1
	var cur string
	var text strings.Builder
	name := ""
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			switch {
			case actionDepth < 0 && t.Name.Space == service:
				name, actionDepth = t.Name.Local, depth
			case actionDepth > 0 && depth == actionDepth+1:
				cur = t.Name.Local
				text.Reset()
			}
		case xml.CharData:
			if cur != "" {
				text.Write(t)
			}
		case xml.EndElement:
			if actionDepth > 0 && depth == actionDepth+1 && cur != "" {
				args[cur] = text.String()
				cur = ""
			}
			depth--
		}
	}
	if name == "" || name != want {
		return "", nil, fmt.Errorf("action %q, header %q", name, want)
	}
	return name, args, nil
}

func soapFault(w http.ResponseWriter, e soapError) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = io.WriteString(w, xml.Header+`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><s:Fault>`+
		`<faultcode>s:Client</faultcode><faultstring>UPnPError</faultstring><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0">`+
		fmt.Sprintf("<errorCode>%d</errorCode><errorDescription>%s</errorDescription>", e.code, esc(e.desc))+
		`</UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
}

// audioProtocols are the songs the device takes: what DecodeStream plays.
const audioProtocols = "http-get:*:audio/mpeg:*,http-get:*:audio/mp3:*,http-get:*:audio/flac:*,http-get:*:audio/x-flac:*," +
	"http-get:*:audio/wav:*,http-get:*:audio/x-wav:*,http-get:*:audio/wave:*"

// sinkProtocols are what the renderer says it takes: the songs, and videos as well while DLNA video is
// on (feature/video), so a controller offers the device only what it will play.
func sinkProtocols() string {
	if video.DLNAOn() {
		return audioProtocols + "," + video.DLNAProtocols
	}
	return audioProtocols
}

func connectionManager(name string) ([]kv, error) {
	switch name {
	case "GetProtocolInfo":
		return []kv{{"Source", ""}, {"Sink", sinkProtocols()}}, nil
	case "GetCurrentConnectionIDs":
		return []kv{{"ConnectionIDs", "0"}}, nil
	case "GetCurrentConnectionInfo":
		return []kv{{"RcsID", "0"}, {"AVTransportID", "0"}, {"ProtocolInfo", ""}, {"PeerConnectionManager", ""},
			{"PeerConnectionID", "-1"}, {"Direction", "Input"}, {"Status", "OK"}}, nil
	}
	return nil, errInvalidAction
}
