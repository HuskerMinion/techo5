package home

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// exifJPEG is the start of a JPEG whose EXIF data says it was written at written (IFD0's DateTime) and,
// unless original is empty, taken at original (the EXIF directory's DateTimeOriginal). Little-endian,
// as most phones write it.
func exifJPEG(written, original string) []byte {
	le := binary.LittleEndian
	t := []byte("II*\x00")
	t = le.AppendUint32(t, 8) // IFD0 just after the header
	entries := 1
	if original != "" {
		entries = 2
	}
	ifd0End := 8 + 2 + entries*12 + 4
	exifIFD := ifd0End
	exifEnd := exifIFD + 2 + 12 + 4
	writtenAt := exifEnd
	originalAt := writtenAt + 20

	entry := func(b []byte, tag, typ uint16, count, value uint32) []byte {
		b = le.AppendUint16(b, tag)
		b = le.AppendUint16(b, typ)
		b = le.AppendUint32(b, count)
		return le.AppendUint32(b, value)
	}
	t = le.AppendUint16(t, uint16(entries))
	t = entry(t, 0x0132, 2, 20, uint32(writtenAt))
	if original != "" {
		t = entry(t, 0x8769, 4, 1, uint32(exifIFD))
	}
	t = le.AppendUint32(t, 0)
	if original != "" {
		t = le.AppendUint16(t, 1)
		t = entry(t, 0x9003, 2, 20, uint32(originalAt))
		t = le.AppendUint32(t, 0)
	} else {
		t = append(t, make([]byte, exifEnd-ifd0End)...)
	}
	t = append(t, written+"\x00"...)
	t = append(t, original+"\x00"...)

	seg := append([]byte("Exif\x00\x00"), t...)
	b := []byte{0xFF, 0xD8, 0xFF, 0xE1}
	b = binary.BigEndian.AppendUint16(b, uint16(len(seg)+2))
	b = append(b, seg...)
	return append(b, 0xFF, 0xD9)
}

// tiffJPEG wraps a hand-made TIFF header and its data as a JPEG's EXIF segment.
func tiffJPEG(t []byte) []byte {
	seg := append([]byte("Exif\x00\x00"), t...)
	b := []byte{0xFF, 0xD8, 0xFF, 0xE1}
	b = binary.BigEndian.AppendUint16(b, uint16(len(seg)+2))
	b = append(b, seg...)
	return append(b, 0xFF, 0xD9)
}

// Offsets out of a crafted file near the top of a 32-bit int, or past it, are refused rather than
// wrapping negative when added to and slicing out of range: the devices are 32-bit, and a photo in a
// shared folder is anybody's. Run with GOARCH=arm this is the case that panicked.
func TestCraftedEXIFOffsetsAreRefused(t *testing.T) {
	le := binary.LittleEndian
	for _, off := range []uint32{0x7FFFFFF0, 0x7FFFFFFF, 0x80000000, 0xFFFFFFF0, 0xFFFFFFFF} {
		// The first directory's offset itself.
		bad := le.AppendUint32([]byte("II*\x00"), off)
		bad = append(bad, make([]byte, 32)...)
		if got := exifTaken(tiffJPEG(bad)); !got.IsZero() {
			t.Errorf("directory at %#x: %v", off, got)
		}
		if got := exifOrientation(tiffJPEG(bad)); got != 1 {
			t.Errorf("directory at %#x: orientation %d", off, got)
		}

		// A DateTime entry whose text is at off.
		d := le.AppendUint32([]byte("II*\x00"), 8)
		d = le.AppendUint16(d, 1)
		d = le.AppendUint16(d, 0x0132)
		d = le.AppendUint16(d, 2)
		d = le.AppendUint32(d, 20)
		d = le.AppendUint32(d, off)
		d = le.AppendUint32(d, 0)
		if got := exifTaken(tiffJPEG(d)); !got.IsZero() {
			t.Errorf("date at %#x: %v", off, got)
		}

		// The EXIF directory's pointer.
		p := le.AppendUint32([]byte("II*\x00"), 8)
		p = le.AppendUint16(p, 1)
		p = le.AppendUint16(p, 0x8769)
		p = le.AppendUint16(p, 4)
		p = le.AppendUint32(p, 1)
		p = le.AppendUint32(p, off)
		p = le.AppendUint32(p, 0)
		if got := exifTaken(tiffJPEG(p)); !got.IsZero() {
			t.Errorf("EXIF directory at %#x: %v", off, got)
		}
	}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.Local) }

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

func TestWhenAPhotoWasTaken(t *testing.T) {
	for _, c := range []struct {
		name, id string
		b        []byte
		want     time.Time
	}{
		{"the moment it was taken", "media-source://media_source/local/a.jpg",
			exifJPEG("2024:01:05 10:00:00", "2019:06:15 14:30:00"), day(2019, time.June, 15)},
		{"when it was written, with no moment taken", "media-source://media_source/local/a.jpg",
			exifJPEG("2021:03:02 09:00:00", ""), day(2021, time.March, 2)},
		{"a phone's file name", "media-source://media_source/local/Camera/20260818_111018.jpg",
			nil, day(2026, time.August, 18)},
		{"a messenger's file name", "media-source://synology/IMG-20250102-WA0001.jpg",
			nil, day(2025, time.January, 2)},
		{"dashes and an escaped space", "media-source://media_source/local/2024-12-24%2018.30.00.jpg",
			nil, day(2024, time.December, 24)},
		{"a date of zeros goes to the name", "media-source://x/PXL_20230704_120000.jpg",
			exifJPEG("0000:00:00 00:00:00", ""), day(2023, time.July, 4)},
		{"no date at all", "media-source://media_source/local/beach.jpg", nil, time.Time{}},
		{"a longer number is no date", "media-source://x/1234201906151234.jpg", nil, time.Time{}},
		{"no 31st of June", "media-source://x/20190631.jpg", nil, time.Time{}},
		{"not in the future", "media-source://x/20990101.jpg", nil, time.Time{}},
	} {
		got := photoTaken(c.id, c.b)
		if c.want.IsZero() != got.IsZero() || (!got.IsZero() && !sameDay(got, c.want)) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

// A photo from a Home Assistant folder has the day it was taken under it, once it has been fetched, and
// only with captions on in Screensaver mode.
func TestCaptionsForPhotosFromHomeAssistant(t *testing.T) {
	id := "media-source://media_source/local/photos/20260818_111018.jpg"
	noteTaken(id, nil)
	h := config.Slideshow{Mode: config.SlideshowScreensaver, Caption: true}
	caps := slideshowCaptions(h, []string{id, "media-source://media_source/local/photos/beach.jpg"})
	if len(caps) != 1 || !sameDay(caps[0].When, day(2026, time.August, 18)) || caps[0].Place != "" || caps[0].Right || !caps[0].Pair {
		t.Errorf("captions %+v, want the left one's day alone", caps)
	}
	h.Caption = false
	if caps := slideshowCaptions(h, []string{id}); caps != nil {
		t.Errorf("with captions off: %+v", caps)
	}
}
