package sendspin

import (
	"encoding/json"
	"testing"

	"github.com/Sendspin/sendspin-go/pkg/protocol"
)

// metadataMessage decodes what the server sends, because the tristate encoding only survives the
// round trip through UnmarshalJSON that records which keys were present.
func metadataMessage(t *testing.T, raw string) *protocol.MetadataState {
	t.Helper()
	var m protocol.MetadataState
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	return &m
}

// A message that only changes part of the track has to leave the rest of it alone. Overwriting the
// whole thing would clear the title every time something else about the track arrived.
func TestAMessageThatChangesPartKeepsTheRest(t *testing.T) {
	var m metadata

	if !m.merge(metadataMessage(t, `{"title":"On Late Nights","artist":"Snorre Kirk","album":"On Late Nights"}`)) {
		t.Fatal("the first track did not count as a change")
	}
	if m.title != "On Late Nights" || m.artist != "Snorre Kirk" || m.album != "On Late Nights" {
		t.Fatalf("merged to %+v", m)
	}

	if m.merge(metadataMessage(t, `{"repeat":"all"}`)) {
		t.Error("a change to something other than the track counted as a change to it")
	}
	if m.title != "On Late Nights" || m.artist != "Snorre Kirk" {
		t.Errorf("the track was cleared by a message that did not mention it: %+v", m)
	}
}

// An explicit null is a clear, and has to be told apart from a key that was not sent: both decode to
// a nil pointer, which is why the merge asks HasField rather than looking at the value.
func TestANullClearsAndAnOmissionDoesNot(t *testing.T) {
	var m metadata
	m.merge(metadataMessage(t, `{"title":"A","artist":"B"}`))

	if !m.merge(metadataMessage(t, `{"artist":null}`)) {
		t.Error("clearing the artist did not count as a change")
	}
	if m.artist != "" {
		t.Errorf("artist = %q after a null, want it cleared", m.artist)
	}
	if m.title != "A" {
		t.Errorf("title = %q, want it left alone by a message that did not mention it", m.title)
	}

	// And a message that carries neither field changes nothing.
	if m.merge(metadataMessage(t, `{}`)) {
		t.Error("an empty message counted as a change")
	}
	if m.title != "A" {
		t.Errorf("title = %q after an empty message", m.title)
	}
}
