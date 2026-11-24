package dlna

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// A Subsonic server's password or its salted hash, an S3 address's signature and anything after a #
// are credentials too.
func TestMoreCredentialsAreHidden(t *testing.T) {
	for in, out := range map[string]string{
		"http://h/rest/stream?u=me&p=pw&t=tk&s=sa&id=1":                             "http://h/rest/stream?u=me&p=&t=&s=&id=1",
		"http://h/a?p=2&t=3&s=4":                                                    "http://h/a?p=2&t=3&s=4",
		"https://b.s3/x?X-Amz-Credential=AK%2F2026&X-Amz-Signature=ff&X-Amz-Date=1": "https://b.s3/x?X-Amz-Credential=&X-Amz-Signature=&X-Amz-Date=1",
		"http://h/a#token=xyz":                                                      "http://h/a",
		"http://h/a?id=1#xyz":                                                       "http://h/a?id=1",
	} {
		if got := withoutSecrets(in); got != out {
			t.Errorf("%q: %q, want %q", in, got, out)
		}
	}
}

// plexMeta is a description as a Plex add-on sends one: the stream's address and the cover's carry
// the server's token.
const plexMeta = `<?xml version="1.0" encoding="utf-8"?>` +
	`<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
	`xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns:techo5="urn:techo5:dlna">` +
	`<item id="1" parentID="0" restricted="1"><dc:title>Tom &amp; Jerry &lt;3</dc:title>` +
	`<upnp:albumArtURI>http://192.168.1.20:32400/photo/:/transcode?url=%2Flibrary%2F1%2Fthumb&amp;X-Plex-Token=abc123</upnp:albumArtURI>` +
	`<upnp:class>object.item.videoItem.movie</upnp:class>` +
	`<res protocolInfo="http-get:*:video/mp2t:*" duration="1:37:00" techo5:start="0:53:00" ` +
	`importUri="http://192.168.1.20:32400/x?X-Plex-Token=abc123">` +
	`http://192.168.1.20:32400/video/:/transcode/universal/start.ts?offset=3180&amp;X-Plex-Token=abc123&amp;session=s1</res>` +
	`</item></DIDL-Lite>`

// The description of the song is the controller's own, as it sent it; any other device is given it
// with every address in it as shownURI shows the song's, so the token in the stream's address and the
// cover's is not handed to whoever asks what is playing (GetPositionInfo, GetMediaInfo, an event). The
// rest reads as it did.
func TestTheDescriptionHidesItsTokenFromOthers(t *testing.T) {
	s := parseSong("http://192.168.1.20:32400/video/:/transcode/universal/start.ts?offset=3180&X-Plex-Token=abc123", plexMeta)
	s.from = "127.0.0.1"
	if got := shownMeta(s, "127.0.0.1"); got != plexMeta {
		t.Errorf("to its own controller: %s", got)
	}
	shown := shownMeta(s, "192.168.1.30")
	if strings.Contains(shown, "abc123") || !strings.Contains(shown, "X-Plex-Token=") {
		t.Fatalf("to another device: %s", shown)
	}
	again := parseSong(s.uri, shown)
	if again.title != "Tom & Jerry <3" || again.dur != 97*time.Minute || again.start != 53*time.Minute || again.class != s.class ||
		again.art != "http://192.168.1.20:32400/photo/:/transcode?url=%2Flibrary%2F1%2Fthumb&X-Plex-Token=" {
		t.Errorf("read again: %+v", again)
	}
	if strings.Contains(shown, "<3") {
		t.Errorf("text came out as markup: %s", shown)
	}

	// What cannot be read as XML is shown to another as it came, its addresses' credentials blanked
	// (TestALooseDescriptionKeepsItsTitle).
	for _, bad := range []string{`<a>x</b>`, `<a>`, `<a>&nope;</a>`, `<a>http://h/x?token=sekrit</b>`} {
		if got := shownMeta(song{meta: bad, from: "127.0.0.1"}, "192.168.1.30"); strings.Contains(got, "sekrit") || got == "" {
			t.Errorf("%q: %q", bad, got)
		}
	}
}

// The answers that carry the description give another device the one without the token.
func TestTheAnswersHideTheDescriptionsToken(t *testing.T) {
	stubs(t)
	f, r := newRenderer()
	r.cur = parseSong("http://192.168.1.20:32400/a.ts?X-Plex-Token=abc123", plexMeta)
	r.cur.from = "127.0.0.1"
	r.next = r.cur
	r.state = stStopped
	for _, action := range []string{"GetPositionInfo", "GetMediaInfo"} {
		code, b := call(t, f, "AVTransport", action, `<InstanceID>0</InstanceID>`)
		if code != 200 || strings.Contains(b, "abc123") || !strings.Contains(b, "X-Plex-Token=") {
			t.Errorf("%s to another device: %d %s", action, code, b)
		}
	}
}

// An error fetching a song names its address: the log is given it without the token, and what the
// error is (a fetch ended) can still be told.
func TestAFetchErrorHidesTheToken(t *testing.T) {
	stubs(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, _, err := fetchSong(ctx, "http://127.0.0.1:9/a.mp3?X-Plex-Token=abc123")
	if err == nil || strings.Contains(err.Error(), "abc123") || !strings.Contains(err.Error(), "X-Plex-Token=") {
		t.Errorf("fetching the song: %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("what the error is was lost: %v", err)
	}
	_, err = fetchRange(ctx, fetchClient, "http://127.0.0.1:9/a.mp3?api_key=abc123", 0, 10)
	if err == nil || strings.Contains(err.Error(), "abc123") {
		t.Errorf("fetching part of it: %v", err)
	}
	if e := errors.New("no address in it"); quietErr(e) != e {
		t.Error("an error with no address in it was changed")
	}
}

// A description that is not well-formed (a bare & a controller did not escape, an HTML entity) is
// kept for the others with its addresses' credentials blanked, so they still see the title.
func TestALooseDescriptionKeepsItsTitle(t *testing.T) {
	meta := `<DIDL-Lite xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/"><item><dc:title>Rock &nbsp; Roll</dc:title>` +
		`<res>http://192.0.2.10:32400/a.mp3?a=1&X-Plex-Token=sekrit</res>` +
		`<upnp:albumArtURI>http://192.0.2.10/art?x=1&amp;X-Plex-Token=sekrit</upnp:albumArtURI></item></DIDL-Lite>`
	got := metaWithoutSecrets(meta)
	if strings.Contains(got, "sekrit") {
		t.Errorf("a token was shown: %s", got)
	}
	if !strings.Contains(got, "Rock &nbsp; Roll") || !strings.Contains(got, "</res>") {
		t.Errorf("the description was not kept: %s", got)
	}
}
