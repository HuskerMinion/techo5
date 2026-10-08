// Package lyrics finds a song's words, timed to the music, at LRCLIB (lrclib.net): a free, open
// database that needs no account and no key. What is sent is the song's title and artist, and its album
// and length when known; nothing else about the device or the room.
package lyrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Line is one line of the words and when it is sung.
type Line struct {
	At   time.Duration
	Text string
}

// ErrNone is a song with no timed words: not in the database, an instrumental, or words without times.
var ErrNone = errors.New("lyrics: none for this song")

// Base is the service's address; a test points it elsewhere.
var Base = "https://lrclib.net"

var client = &http.Client{Timeout: 10 * time.Second}

// mostBytes bounds a reply: a song's words are a few kilobytes, a search's a few dozen.
const mostBytes = 1 << 20

type record struct {
	TrackName    string  `json:"trackName"`
	ArtistName   string  `json:"artistName"`
	Duration     float64 `json:"duration"`
	Instrumental bool    `json:"instrumental"`
	SyncedLyrics string  `json:"syncedLyrics"`
}

// Find is the song's timed words. dur is its length, 0 when unknown: with it, a version of a different
// length (a live take, a radio edit) is not taken for this one.
func Find(ctx context.Context, title, artist, album string, dur time.Duration) ([]Line, error) {
	title, artist = strings.TrimSpace(title), strings.TrimSpace(artist)
	if title == "" || artist == "" {
		return nil, ErrNone
	}
	// The exact lookup wants everything; it is the right one when it answers.
	if album != "" && dur > 0 {
		q := url.Values{"track_name": {title}, "artist_name": {artist}, "album_name": {album},
			"duration": {strconv.Itoa(int(dur.Round(time.Second) / time.Second))}}
		var r record
		switch err := get(ctx, "/api/get?"+q.Encode(), &r); {
		case err == nil:
			if lines := Parse(r.SyncedLyrics); len(lines) > 0 && !r.Instrumental {
				return lines, nil
			}
		case !errors.Is(err, errNotFound):
			return nil, err
		}
	}
	q := url.Values{"track_name": {title}, "artist_name": {artist}}
	var found []record
	if err := get(ctx, "/api/search?"+q.Encode(), &found); err != nil {
		if errors.Is(err, errNotFound) {
			return nil, ErrNone
		}
		return nil, err
	}
	for _, r := range found {
		if r.Instrumental || r.SyncedLyrics == "" {
			continue
		}
		if dur > 0 && r.Duration > 0 && absDur(time.Duration(r.Duration*float64(time.Second))-dur) > 3*time.Second {
			continue
		}
		if lines := Parse(r.SyncedLyrics); len(lines) > 0 {
			return lines, nil
		}
	}
	return nil, ErrNone
}

var errNotFound = errors.New("lyrics: not found")

func get(ctx context.Context, path string, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, Base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "TECHO5 (github.com/HuskerMinion/techo5)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("lyrics: %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, mostBytes)).Decode(into)
}

// Parse reads LRC text: "[mm:ss.xx] words" lines, a line with more than one time sung at each of them.
// Tags ("[ar: ...]") and lines without a time are left out; the result is in time order.
func Parse(lrc string) []Line {
	var out []Line
	for raw := range strings.SplitSeq(lrc, "\n") {
		s := strings.TrimSpace(raw)
		var times []time.Duration
		for strings.HasPrefix(s, "[") {
			end := strings.IndexByte(s, ']')
			if end < 0 {
				break
			}
			t, ok := stamp(s[1:end])
			if !ok {
				times = nil
				break
			}
			times = append(times, t)
			s = strings.TrimSpace(s[end+1:])
		}
		for _, t := range times {
			out = append(out, Line{At: t, Text: s})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}

// stamp reads "mm:ss", "mm:ss.xx" or "mm:ss.xxx".
func stamp(s string) (time.Duration, bool) {
	m, rest, ok := strings.Cut(s, ":")
	if !ok {
		return 0, false
	}
	mins, err := strconv.Atoi(m)
	if err != nil || mins < 0 {
		return 0, false
	}
	secs, err := strconv.ParseFloat(rest, 64)
	if err != nil || secs < 0 || secs >= 60 {
		return 0, false
	}
	return time.Duration(mins)*time.Minute + time.Duration(secs*float64(time.Second)), true
}

// At is the line being sung at pos and the one after it, with when the next one starts (0 when there
// is none). Before the first line, the current one is empty and the next is the first.
func At(lines []Line, pos time.Duration) (cur, next string, nextAt time.Duration) {
	i := sort.Search(len(lines), func(i int) bool { return lines[i].At > pos })
	if i > 0 {
		cur = lines[i-1].Text
	}
	if i < len(lines) {
		next, nextAt = lines[i].Text, lines[i].At
	}
	return cur, next, nextAt
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
