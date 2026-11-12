package home

import (
	"encoding/binary"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// When a photo from somewhere other than Immich was taken, for Slideshow date and place (#502). Immich
// says when and where; any other source has only the photo itself, whose EXIF data a camera or phone
// fills in, and its file's name, which phones and many exports write the day into
// (20260818_111018.jpg, IMG-20260818-WA0001.jpg, 2026-08-18 11.10.18.jpg). Only the day is shown, and
// no place: GPS is in the EXIF data too, but naming the place it points at needs a service to ask.

// photoTaken is when a photo was taken, from its EXIF data or, failing that, its id's file name; zero
// when neither says.
func photoTaken(id string, b []byte) time.Time {
	if t := exifTaken(b); !t.IsZero() {
		return t
	}
	return nameTaken(id)
}

// exifTaken is when a JPEG says it was taken: DateTimeOriginal in its EXIF directory, or failing that
// the first directory's DateTime, which is when the file was last written and is still the day for
// most photos. Zero when it says neither, or says something no camera could have meant.
func exifTaken(b []byte) time.Time {
	t := exifTIFF(b)
	if len(t) < 8 {
		return time.Time{}
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return time.Time{}
	}
	ifd0 := int(order.Uint32(t[4:]))
	var written time.Time
	if sub, ok := tiffTag(t, order, ifd0, 0x8769); ok { // the EXIF directory
		if v, ok := tiffTag(t, order, int(order.Uint32(t[sub+8:])), 0x9003); ok {
			if when := tiffDate(t, order, v); !when.IsZero() {
				return when
			}
		}
	}
	if v, ok := tiffTag(t, order, ifd0, 0x0132); ok {
		written = tiffDate(t, order, v)
	}
	return written
}

// tiffTag is where tag's entry is in the directory at ifd, if it has one.
//
// Every bound on an offset out of the file is a subtraction from len(t), never an addition to the offset:
// the devices are 32-bit, where an offset near the top of an int wraps negative when added to and passes
// the bound it was meant to fail.
func tiffTag(t []byte, order binary.ByteOrder, ifd int, tag uint16) (int, bool) {
	if ifd <= 0 || ifd > len(t)-2 {
		return 0, false
	}
	n := int(order.Uint16(t[ifd:]))
	for e := range n {
		at := ifd + 2 + e*12
		if at+12 > len(t) {
			return 0, false
		}
		if order.Uint16(t[at:]) == tag {
			return at, true
		}
	}
	return 0, false
}

// exifDateForm is how EXIF writes a moment.
const exifDateForm = "2006:01:02 15:04:05"

// tiffDate reads the entry at e as an EXIF date: ASCII, "2026:08:18 11:10:18", held elsewhere in the
// data since it is longer than an entry's own four bytes. Zero for anything else, or a date of zeros.
func tiffDate(t []byte, order binary.ByteOrder, e int) time.Time {
	if order.Uint16(t[e+2:]) != 2 { // ASCII
		return time.Time{}
	}
	count, off := int(order.Uint32(t[e+4:])), int(order.Uint32(t[e+8:]))
	if count < len(exifDateForm) || off < 0 || off > len(t)-len(exifDateForm) {
		return time.Time{}
	}
	when, err := time.ParseInLocation(exifDateForm, string(t[off:off+len(exifDateForm)]), time.Local)
	if err != nil || !plausible(when) {
		return time.Time{}
	}
	return when
}

// nameDay finds a day written into a file's name: a year from 1990 on, then the month and the day, run
// together or with a dash, an underscore or a dot between, and no other digit touching them.
var nameDay = regexp.MustCompile(`(?:^|\D)((?:19|20)\d\d)[-_.]?(0[1-9]|1[0-2])[-_.]?(0[1-9]|[12]\d|3[01])(?:\D|$)`)

// nameTaken is the day a photo's file name says it was taken, zero when it says none.
func nameTaken(id string) time.Time {
	name := id
	if u, err := url.PathUnescape(id); err == nil {
		name = u
	}
	name = path.Base(strings.TrimRight(name, "/"))
	m := nameDay.FindStringSubmatch(name)
	if m == nil {
		return time.Time{}
	}
	when, err := time.ParseInLocation("2006-01-02", m[1]+"-"+m[2]+"-"+m[3], time.Local)
	if err != nil || !plausible(when) {
		return time.Time{} // the 31st of a month with 30, say
	}
	return when
}

// plausible is whether a photo could have been taken then: not before 1990, and not in the future,
// which a camera whose clock was never set or a file name of some other number would give.
func plausible(when time.Time) bool {
	return when.Year() >= 1990 && when.Before(time.Now().Add(48*time.Hour))
}

// takenNoted is when the photos fetched lately were taken, by id, for their captions: the photo's bytes
// are only at hand while it is fetched (fetchSlideshowSource), and its caption is made after.
var takenNoted = struct {
	sync.Mutex
	when map[string]time.Time
}{when: map[string]time.Time{}}

// takenNotedMax bounds how many are kept: a look fetches at most a few, a pair and those passed over.
const takenNotedMax = 32

// noteTaken keeps when the photo id was taken, from its bytes b.
func noteTaken(id string, b []byte) {
	when := photoTaken(id, b)
	takenNoted.Lock()
	defer takenNoted.Unlock()
	if len(takenNoted.when) >= takenNotedMax {
		clear(takenNoted.when)
	}
	takenNoted.when[id] = when
}

// takenOf is when the photo id was taken, as noted when it was fetched; zero when that was not noted
// or the photo did not say.
func takenOf(id string) time.Time {
	takenNoted.Lock()
	defer takenNoted.Unlock()
	return takenNoted.when[id]
}
