package announce

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// An announcement goes out of one device and into another, and both ends are this code, so what is
// worth holding is that everything survives the trip: the voice sample for sample, the words as
// somebody typed them, and a name with a space or an accent in it.
func TestAVoiceSurvivesTheTrip(t *testing.T) {
	sent := Message{
		From:  "Terry's Desk",
		Text:  "dinner is ready — come down",
		Voice: []int16{0, 1, -1, 32767, -32768, 1234, -4321},
	}

	body, headers := encode(sent)
	r := httptest.NewRequest(http.MethodPost, "/announce", bytes.NewReader(body))
	for k, v := range headers {
		r.Header.Set(k, v)
	}

	got, err := decode(r)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.From != sent.From {
		t.Errorf("from %q, sent %q", got.From, sent.From)
	}
	if got.Text != sent.Text {
		t.Errorf("text %q, sent %q", got.Text, sent.Text)
	}
	if len(got.Voice) != len(sent.Voice) {
		t.Fatalf("%d samples, sent %d", len(got.Voice), len(sent.Voice))
	}
	for i := range sent.Voice {
		if got.Voice[i] != sent.Voice[i] {
			t.Fatalf("sample %d is %d, sent %d", i, got.Voice[i], sent.Voice[i])
		}
	}
}

// An automation sends words and no voice; a person sends voice and no words. Both are announcements.
func TestWordsWithoutAVoiceAndAVoiceWithoutWords(t *testing.T) {
	for _, m := range []Message{
		{From: "Kitchen", Text: "the washing is done"},
		{From: "Kitchen", Voice: []int16{5, 5, 5}},
	} {
		body, headers := encode(m)
		r := httptest.NewRequest(http.MethodPost, "/announce", bytes.NewReader(body))
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		got, err := decode(r)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Text != m.Text || len(got.Voice) != len(m.Voice) {
			t.Errorf("came back as %q with %d samples, sent %q with %d",
				got.Text, len(got.Voice), m.Text, len(m.Voice))
		}
	}
}

// A peer cannot fill this device's memory with one request.
func TestAnEnormousBodyIsCutOff(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/announce", bytes.NewReader(make([]byte, mostAudio*2)))
	got, err := decode(r)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Voice) > mostAudio/2 {
		t.Errorf("read %d samples, which is past the ceiling of %d", len(got.Voice), mostAudio/2)
	}
}
