package announce

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/url"
)

// What an announcement looks like on the wire.
//
// The body is the voice, as the microphone heard it: signed 16-bit samples, little-endian, mono, at
// the microphone's rate. Not a container and not a codec — both ends are this daemon on the same
// network, fifteen seconds is under half a megabyte, and a format nobody has to agree on is a format
// nobody can disagree about.
//
// Who it came from and what was said ride in headers, because they are small and because a body that
// is one thing is easier to bound than a body that is two.

const (
	fromHeader = "X-Techo5-From"
	textHeader = "X-Techo5-Text"

	// audioType says what the body is, for anything that looks.
	audioType = "audio/L16; rate=16000; channels=1"

	// mostAudio bounds what will be read: twenty seconds at the microphone's rate, which is longer
	// than anything this sends and short enough that a peer cannot fill memory with one request.
	mostAudio = 20 * 16000 * 2
)

// encode turns a message into a request body and the headers that go with it.
func encode(m Message) (body []byte, headers map[string]string) {
	headers = map[string]string{
		fromHeader:     url.QueryEscape(m.From),
		textHeader:     url.QueryEscape(m.Text),
		"Content-Type": audioType,
	}
	if len(m.Voice) == 0 {
		return nil, headers
	}
	var b bytes.Buffer
	b.Grow(len(m.Voice) * 2)
	_ = binary.Write(&b, binary.LittleEndian, m.Voice)
	return b.Bytes(), headers
}

// decode reads one back. A header that was not escaped by this daemon is taken as it stands rather
// than refused: the words are shown, never run.
func decode(r *http.Request) (Message, error) {
	m := Message{
		From: unescape(r.Header.Get(fromHeader)),
		Text: unescape(r.Header.Get(textHeader)),
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, mostAudio))
	if err != nil {
		return m, err
	}
	m.Voice = samples(raw)
	return m, nil
}

// samples reads the body as little-endian 16-bit mono. An odd trailing byte is a truncated sample
// and is dropped.
func samples(raw []byte) []int16 {
	if len(raw) < 2 {
		return nil
	}
	out := make([]int16, len(raw)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(raw[i*2:]))
	}
	return out
}

func unescape(v string) string {
	if v == "" {
		return ""
	}
	if s, err := url.QueryUnescape(v); err == nil {
		return s
	}
	return v
}
