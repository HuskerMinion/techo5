package config

import (
	"slices"
	"time"
)

// Video is the Show playing videos on its screen (feature/video, docs/video.md). Everything is off on
// a new device: each switch lets something on the network put a picture and sound on it.
type Video struct {
	// On lets videos play at all: from Home Assistant's play_video action, and from DLNA when DLNA
	// is on too. Off, nothing plays, whoever asks.
	On bool `json:"on,omitempty"`

	// DLNA makes the DLNA renderer take videos as well as music, while On and the DLNA switch are on.
	DLNA bool `json:"dlna,omitempty"`

	// Allowed are the addresses whose DLNA videos the screen was told to allow, and when each last
	// sent one: the first video from any other address, or from one unused for AllowedFor, asks on
	// the screen first. Home Assistant's own action never asks.
	Allowed []AllowedAddr `json:"allowed,omitempty"`
}

// AllowedAddr is an address allowed to send DLNA videos, and when it last did.
type AllowedAddr struct {
	Addr string    `json:"addr"`
	Used time.Time `json:"used"`
}

// MostAllowed bounds the remembered addresses: the least recently used goes when a new one is allowed
// past it.
const MostAllowed = 32

// AllowedFor is how long an allowance lasts unused: an address on a home network is a device's only
// for as long as the router keeps giving it the same one.
const AllowedFor = 30 * 24 * time.Hour

// IsAllowed is whether addr's DLNA videos play without asking, now.
func (v Video) IsAllowed(addr string, now time.Time) bool {
	if addr == "" {
		return false
	}
	for _, a := range v.Allowed {
		if a.Addr == addr {
			return now.Sub(a.Used) < AllowedFor
		}
	}
	return false
}

func (v Video) clone() Video {
	v.Allowed = slices.Clone(v.Allowed)
	return v
}

type VideoWriter struct{ st *Store }

func (w VideoWriter) On(v bool) error {
	return w.st.Update(func(c *Config) { c.Video.On = v })
}

func (w VideoWriter) DLNA(v bool) error {
	return w.st.Update(func(c *Config) { c.Video.DLNA = v })
}

// Allow remembers addr as one whose videos play without asking, as used at now: allowing it, and every
// video it sends, start its AllowedFor again. Allowances that have run out go.
func (w VideoWriter) Allow(addr string, now time.Time) error {
	return w.st.Update(func(c *Config) {
		if addr == "" {
			return
		}
		kept := c.Video.Allowed[:0:0]
		for _, a := range c.Video.Allowed {
			if a.Addr != addr && now.Sub(a.Used) < AllowedFor {
				kept = append(kept, a)
			}
		}
		kept = append(kept, AllowedAddr{Addr: addr, Used: now.UTC().Truncate(time.Second)})
		slices.SortStableFunc(kept, func(a, b AllowedAddr) int { return a.Used.Compare(b.Used) })
		if n := len(kept); n > MostAllowed {
			kept = kept[n-MostAllowed:]
		}
		c.Video.Allowed = kept
	})
}

// ForgetAllowed makes every address ask again.
func (w VideoWriter) ForgetAllowed() error {
	return w.st.Update(func(c *Config) { c.Video.Allowed = nil })
}
