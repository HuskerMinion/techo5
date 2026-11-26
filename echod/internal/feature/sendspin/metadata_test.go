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

// Switching to a new title clears stale artist and album when the server omits them (for example
// Music Assistant's aiosendspin omits None fields when switching to a radio station or jingle with
// no artist/album), while keeping fields that the new message provides.
func TestNewTitleClearsOmittedArtistAndAlbum(t *testing.T) {
	var m metadata
	m.merge(metadataMessage(t, `{"title":"Song A","artist":"Singer A","album":"CAPITAL 958"}`))

	// Same station switches to a jingle with no artist (omitted on the wire).
	if !m.merge(metadataMessage(t, `{"title":"Station Jingle","album":"CAPITAL 958"}`)) {
		t.Fatal("switching to jingle did not count as a change")
	}
	if m.title != "Station Jingle" || m.artist != "" || m.album != "CAPITAL 958" {
		t.Fatalf("after jingle = %+v, want title=Station Jingle artist=\"\" album=CAPITAL 958", m)
	}

	// Switch back to a track with artist, then to a radio station that sends only title.
	m.merge(metadataMessage(t, `{"title":"Song B","artist":"Singer B","album":"CAPITAL 958"}`))
	if !m.merge(metadataMessage(t, `{"title":"CRI 环球资讯广播"}`)) {
		t.Fatal("switching to station with title only did not count as a change")
	}
	if m.title != "CRI 环球资讯广播" || m.artist != "" || m.album != "" {
		t.Fatalf("after station switch = %+v, want title=CRI 环球资讯广播 artist=\"\" album=\"\"", m)
	}

	// Repeating the same title without artist/album does not clear them.
	m.merge(metadataMessage(t, `{"title":"Song C","artist":"Singer C","album":"Album C"}`))
	if m.merge(metadataMessage(t, `{"title":"Song C"}`)) {
		t.Error("repeating the same title counted as a change")
	}
	if m.artist != "Singer C" || m.album != "Album C" {
		t.Errorf("repeating the same title cleared fields: %+v", m)
	}
}

// A station's tagged title shows as its song; any other title as it came.
func TestCleanTitle(t *testing.T) {
	iheart := `text="Static" song_spot="M" MediaBaseId="3097657" itunesTrackId="0" amgTrackId="-1" amgArtistId="0" TAID="0" TPID="312041972" cartcutId="0442511001" amgArtworkURL="https:///v3/catalog/track/312041972?ops=fit(200,200),format(%22jpeg%22)" length="00:03:25" unsID="-1" spotInstanceId="-1"`
	for in, want := range map[string]string{
		iheart: "Static",
		`Sleep Theory - text="Static" song_spot="M" MediaBaseId="3097657"`: "Static",
		`adContext="aHR0cHM6Ly9uM2NiLWUy"`:                                 "",
		`text="" song_spot="T"`:                                            "",
		"I'll Follow You":                                                  "I'll Follow You",
		`She said text="hi" to me`:                                         `She said text="hi" to me`,
		`Song "Quoted" Title`:                                              `Song "Quoted" Title`,
	} {
		if got := cleanTitle(in); got != want {
			t.Errorf("%q came out %q, want %q", in, got, want)
		}
	}
}
