// Package addonapi is the add-on API's wire protocol and a client for it: how a TECHO5 add-on talks to
// the daemon on the device it runs on (docs/addon-api.md). The daemon and the fake daemon for authors
// (cmd/techo5-addon-fake) speak it through the same definitions, as the deck and its agent share
// deckwire, so what an author tests against is what the device reads.
//
// The add-on dials the Unix socket named in TECHO5_ADDON_API. Every message either way is a 4-byte
// big-endian length and that many bytes of UTF-8 JSON, read strictly: a key the receiver does not know,
// one given twice or in another case, or anything after the value refuses the message. The first
// request is hello; after that the add-on sends requests and the daemon answers each by its id, and
// sends events of its own (a command to run, a timer that rang) whenever they happen.
//
// It uses only the standard library, so an add-on that imports it brings nothing else along.
package addonapi

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// Version is the API level this package speaks: stage 1, "api1" in the catalog's requires.
const Version = 1

// EnvSocket names the environment variable the daemon gives an add-on the socket's path in.
const EnvSocket = "TECHO5_ADDON_API"

// Limits, the same on both ends. Past one a request is answered too_large or rate_limited; an add-on
// that keeps on is disconnected for a minute.
const (
	// MaxMessage is the most a message may hold, its length prefix aside.
	MaxMessage = 64 << 10
	// Rate and Burst are the requests a second a connection may send, and how many it may send at
	// once after a quiet spell.
	Rate  = 20
	Burst = 40
	// MaxConns is how many connections one add-on may hold at once.
	MaxConns = 4
	// MaxOutstanding is how many requests of one connection may wait for their answers at once.
	MaxOutstanding = 64
)

// Capabilities an add-on can be granted in stage 1. Each is a line on the consent screen, pinned by
// the catalog, and checked on every request that needs it.
const (
	CapCommands  = "commands"
	CapEvents    = "events"
	CapSpeak     = "speak"
	CapSound     = "sound"
	CapTimers    = "timers"
	CapKeepAwake = "keepawake"
	CapSettings  = "settings"
	CapEntities  = "entities"
	CapMedia     = "media"
)

// Capabilities is every capability this level knows, in the order the consent screen lists them.
var Capabilities = []string{CapCommands, CapEvents, CapSpeak, CapSound, CapTimers, CapKeepAwake, CapSettings,
	CapEntities, CapMedia}

// Ops an add-on can ask for.
const (
	OpHello         = "hello"
	OpCommandReply  = "command.reply"
	OpEventFire     = "event.fire"
	OpSpeak         = "speak"
	OpSpeakCancel   = "speak.cancel"
	OpSoundPlay     = "sound.play"
	OpTimerStart    = "timer.start"
	OpTimerCancel   = "timer.cancel"
	OpTimerList     = "timer.list"
	OpAwakeHold     = "awake.hold"
	OpAwakeRenew    = "awake.renew"
	OpAwakeRelease  = "awake.release"
	OpSettingsGet   = "settings.get"
	OpSettingsReply = "settings.reply"
	OpEntitySet     = "entity.set"
	OpMediaPlay     = "media.play"
	OpMediaPause    = "media.pause"
	OpMediaResume   = "media.resume"
	OpMediaStop     = "media.stop"
)

// Events the daemon sends.
const (
	EventCommand       = "command"
	EventTimer         = "timer"
	EventSettings      = "settings"
	EventSettingsTest  = "settings.test"
	EventEntityCommand = "entity.command"
)

// Error codes, a fixed set.
const (
	CodeBadRequest  = "bad_request"
	CodeDenied      = "denied"
	CodeUnknownOp   = "unknown_op"
	CodeTooLarge    = "too_large"
	CodeRateLimited = "rate_limited"
	CodeBusy        = "busy"
	CodeUnavailable = "unavailable"
	CodeInternal    = "internal"
)

// Request is what an add-on sends.
type Request struct {
	ID   uint64          `json:"id"`
	Op   string          `json:"op"`
	Args json.RawMessage `json:"args,omitempty"`
}

// Response is the daemon's answer to one request, by its id: a result, or an error.
type Response struct {
	ID     uint64          `json:"id"`
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Event is something the daemon tells an add-on of its own accord.
type Event struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Error is a refused request: one of the codes, and words for a log.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Errorf is an Error with the code given.
func Errorf(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// CodeOf is the code of an error the daemon answered, or internal for any other.
func CodeOf(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}

// ErrTooLarge is a message whose length is more than the reader allows.
var ErrTooLarge = errors.New("addonapi: the message is larger than allowed")

// WriteFrame writes one message: its length, then it.
func WriteFrame(w io.Writer, b []byte) error {
	if len(b) > MaxMessage {
		return ErrTooLarge
	}
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf, uint32(len(b)))
	copy(buf[4:], b)
	_, err := w.Write(buf)
	return err
}

// ReadHeader reads one message's length.
func ReadHeader(r io.Reader) (uint32, error) {
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(h[:]), nil
}

// ReadFrame reads one message of at most max bytes. One that says it is longer is not read: the
// caller is told ErrTooLarge and the length, and either skips that many bytes to carry on or hangs up.
func ReadFrame(r io.Reader, max int) ([]byte, uint32, error) {
	n, err := ReadHeader(r)
	if err != nil {
		return nil, 0, err
	}
	if int64(n) > int64(max) {
		return nil, n, ErrTooLarge
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return nil, n, err
	}
	return b, n, nil
}

// Marshal is v as a message's JSON. Nothing in the protocol needs HTML escaping, and a message is
// read by a program, so none is done.
func Marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// Unmarshal reads a message strictly, as both ends do. Go's decoder takes the last of two keys with
// the same name, matches a key whatever its case, and skips one it has no field for: each lets one
// message say two things, so a key has to be lowercase ASCII, appear once in its object and name a
// field, and nothing may follow the value.
func Unmarshal(b []byte, v any) error {
	if err := checkKeys(b); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return errors.New("something follows the JSON value")
	}
	return nil
}

// keyPattern is every key the protocol has, and every name an add-on gives a command's argument, a
// setting or an entity: lowercase ASCII, digits and underscores. Go matches a key to a field with
// Unicode case folding, so a key outside ASCII (U+017F, the long s, folds to s) could name a field
// spelled otherwise.
var keyPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// checkKeys walks the tokens and refuses a key that is not lowercase ASCII, or one its object has
// already.
func checkKeys(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	type frame struct {
		object bool
		keys   map[string]bool
		key    bool
	}
	var stack []*frame
	depth := 0
	for {
		t, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var top *frame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if top != nil && top.object && top.key && t != json.Delim('}') {
			k, ok := t.(string)
			if !ok {
				return errors.New("an object key is not a string")
			}
			if !keyPattern.MatchString(k) {
				return fmt.Errorf("the key %q is not lowercase ASCII letters, digits and underscores", k)
			}
			for seen := range top.keys {
				if strings.EqualFold(seen, k) {
					return fmt.Errorf("the key %q is given twice in one object", k)
				}
			}
			top.keys[k] = true
			top.key = false
			continue
		}
		switch t {
		case json.Delim('{'), json.Delim('['):
			// Deep nesting is no use to anything in the protocol, and costs the reader for nothing.
			if depth++; depth > 16 {
				return errors.New("nested too deeply")
			}
			stack = append(stack, &frame{object: t == json.Delim('{'), keys: map[string]bool{}, key: true})
			continue
		case json.Delim('}'), json.Delim(']'):
			depth--
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].key = true
		}
	}
}

// Hello is the first request's arguments: the API level the add-on was written for.
type Hello struct {
	API int `json:"api"`
}

// Welcome is the daemon's answer to hello.
type Welcome struct {
	API          int            `json:"api"`
	Addon        string         `json:"addon"`
	Capabilities []string       `json:"capabilities"`
	Device       Device         `json:"device"`
	Settings     map[string]any `json:"settings,omitempty"`
}

// Device is what an add-on is told about the device it runs on.
type Device struct {
	// Family is "show", "spot" or "dot"; Board the model's board ("cronos", "rook" and the rest).
	Family string `json:"family"`
	Board  string `json:"board,omitempty"`
	// Width and Height are the screen's, in pixels; 0 on a device without one.
	Width  int `json:"width"`
	Height int `json:"height"`
	// Language is the screen's, as a BCP 47 tag ("en", "de").
	Language string `json:"language"`
	// Version is the TECHO5 release it runs.
	Version string `json:"version,omitempty"`
}
