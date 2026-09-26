//go:build linux

package main

import (
	"encoding/binary"
	"image/color"
	"strings"
	"testing"
	"time"
)

// The fill works by doubling what has been written so far, and a doubling that stops early leaves
// part of the page holding stale pixels. Nothing would fail: the bench would time a smaller write
// than it claims and report a rate no moving content could reach, which is the one answer the whole
// test exists to avoid. So every byte is checked, at lengths either side of a word and of a
// power of two.
func TestFillPageFillsEveryByte(t *testing.T) {
	v := varInfo{}
	v.Red[0], v.Green[0], v.Blue[0], v.Transp[0] = 16, 8, 0, 24
	px := uint32(0x12)<<16 | uint32(0x34)<<8 | uint32(0x56) | uint32(0xff)<<24

	for _, n := range []int{0, 1, 3, 4, 5, 7, 8, 33, 100, 4093, 4096, 100000} {
		page := make([]byte, n)
		fillPage(page, color.RGBA{0x12, 0x34, 0x56, 0xff}, v)
		for i := 0; i < n; i++ {
			if want := byte(px >> (8 * uint(i%4))); page[i] != want {
				t.Fatalf("page of %d bytes: byte %d is %#02x, want %#02x", n, i, page[i], want)
			}
		}
	}
}

// The color is packed with the panel's own offsets, not the byte order of the image: a Show 5
// framebuffer is r16 g8 b0 a24, and a fill that ignored that would paint one channel and call it
// a frame, so the alternation a camera counts would be invisible.
func TestFillPageUsesThePanelsOffsets(t *testing.T) {
	v := varInfo{}
	v.Red[0], v.Green[0], v.Blue[0], v.Transp[0] = 16, 8, 0, 24

	page := make([]byte, 4)
	fillPage(page, color.RGBA{0xAA, 0xBB, 0xCC, 0xDD}, v)

	if got, want := binary.LittleEndian.Uint32(page), uint32(0xAA)<<16|uint32(0xBB)<<8|0xCC|uint32(0xDD)<<24; got != want {
		t.Fatalf("pixel is %#08x, want %#08x", got, want)
	}
}

func TestPageCount(t *testing.T) {
	if got := pageCount(varInfo{Xres: 480, Yres: 960, XresVirtual: 480, YresVirtual: 2880}); got != 3 {
		t.Fatalf("a 480x960 device with 2880 virtual rows has %d pages, want 3", got)
	}
	if got := pageCount(varInfo{Xres: 480, Yres: 960}); got != 1 {
		t.Fatalf("without virtual rows there is %d page, want 1", got)
	}
}

func TestPageOffsets(t *testing.T) {
	if got := pageOffsets(3, 960); got != "0, 960, 1920" {
		t.Fatalf("offsets are %q, want %q", got, "0, 960, 1920")
	}
	if got := pageOffsets(1, 960); got != "not at all (one page)" {
		t.Fatalf("one page reads %q", got)
	}
}

// The tally is read by somebody comparing it with the page count, so it has to come out in offset
// order rather than in whatever order the map hands it over.
func TestCountsAreInOffsetOrder(t *testing.T) {
	got := counts(map[uint32]int{960: 14, 0: 15, 1920: 1})
	if want := "0 x15, 960 x14, 1920 x1"; got != want {
		t.Fatalf("tally is %q, want %q", got, want)
	}
}

func TestSpreadOrdersItself(t *testing.T) {
	mn, p50, p95, mx := spread([]time.Duration{30 * time.Millisecond, 10 * time.Millisecond, 20 * time.Millisecond})
	if mn != 10*time.Millisecond || mx != 30*time.Millisecond {
		t.Fatalf("min and max are %v and %v, want 10ms and 30ms", mn, mx)
	}
	if p50 < mn || p50 > mx || p95 < p50 || p95 > mx {
		t.Fatalf("percentiles are out of order: p50 %v, p95 %v", p50, p95)
	}
}

// The verdict is the line somebody quotes afterwards, so which reading each pan cost produces is
// pinned here rather than left to whoever changes a threshold later.
func TestVerdictReadsThePanCost(t *testing.T) {
	cases := []struct {
		pan   time.Duration
		pace  time.Duration
		late  int
		words string
	}{
		{pan: 16 * time.Millisecond, words: "one frame period"},
		{pan: 40 * time.Millisecond, words: "two frame periods"},
		{pan: time.Millisecond, words: "not the ceiling"},
		{pan: time.Millisecond, pace: 41 * time.Millisecond, late: 3, words: "went late"},
	}
	for _, c := range cases {
		if got := verdict(c.pan, c.pace, c.late); !strings.Contains(got, c.words) {
			t.Errorf("a %v pan (pace %v, %d late) reads %q, which does not mention %q", c.pan, c.pace, c.late, got, c.words)
		}
	}
}
