package config

import (
	"maps"
	"slices"
	"strings"
	"time"
)

// Share is settings shared with the other devices of the house (feature/share): whether this device takes
// part, which of its settings are linked with which devices, the ones it keeps to itself whatever the
// others do, where each value came from, and the links asked for that are not settled yet. All of it is
// this device's own: a settings file neither carries nor changes it (KeepOwn).
//
// A link is made on each device, never by another: this device lists a device for a setting when somebody
// ticked it here, or accepted that device's request here.
type Share struct {
	On bool `json:"on,omitempty"`

	// Links is, by setting (its entity's object ID), the other devices it is kept in step with, at most
	// MaxLinked of them.
	Links map[string][]string `json:"links,omitempty"`

	// Keep is the settings this device keeps its own: nothing another device sends changes them.
	Keep []string `json:"keep,omitempty"`

	// From is, by setting, where its value came from and its place in the order of changes.
	From map[string]ShareFrom `json:"from,omitempty"`

	// Requests is links other devices have asked for, waiting to be accepted or declined here, at most
	// MaxRequests of them.
	Requests []ShareRequest `json:"requests,omitempty"`

	// Waiting is, by setting, the devices asked to link with this one that have not accepted yet, and
	// AskedAt when each was first asked (by AskKey).
	Waiting map[string][]string  `json:"waiting,omitempty"`
	AskedAt map[string]time.Time `json:"asked_at,omitempty"`

	// Declined is the requests declined here, at most MaxRequests: the same request again is answered as
	// declined rather than shown again.
	Declined []ShareRequest `json:"declined,omitempty"`
}

// AskKey is a setting and a device asked about it, as AskedAt is keyed.
func AskKey(id, device string) string { return id + "/" + strings.ToLower(device) }

const (
	// MaxLinked is the most devices one setting is linked with.
	MaxLinked = 16
	// MaxRequests is the most link requests kept waiting; the oldest goes first.
	MaxRequests = 64
)

// ShareFrom is where a setting's value came from: the device a change was made on, when this device
// took it (by its own clock, for showing), and N, the change's place in the setting's order: every change
// is one more than the highest the device making it had seen, so the order does not depend on any
// device's clock.
type ShareFrom struct {
	Device string    `json:"device"`
	At     time.Time `json:"at"`
	N      uint64    `json:"n"`
	// Via is, for a copy, the device it was copied from: the change is this device's own in the order
	// (Device), since it is this device that numbered it.
	Via string `json:"via,omitempty"`
}

// ShareRequest is a link another device asked for.
type ShareRequest struct {
	ID     string    `json:"id"`
	Device string    `json:"device"`
	At     time.Time `json:"at"`
}

func (s Share) clone() Share {
	s.Links = cloneLists(s.Links)
	s.Waiting = cloneLists(s.Waiting)
	s.Keep = slices.Clone(s.Keep)
	s.From = maps.Clone(s.From)
	s.Requests = slices.Clone(s.Requests)
	s.AskedAt = maps.Clone(s.AskedAt)
	s.Declined = slices.Clone(s.Declined)
	return s
}

// WasDeclined is whether a device's request to link a setting was declined here.
func (s Share) WasDeclined(id, device string) bool {
	return slices.ContainsFunc(s.Declined, func(r ShareRequest) bool { return r.ID == id && strings.EqualFold(r.Device, device) })
}

func cloneLists(m map[string][]string) map[string][]string {
	m = maps.Clone(m)
	for k, v := range m {
		m[k] = slices.Clone(v)
	}
	return m
}

func hasName(list []string, name string) bool {
	return slices.ContainsFunc(list, func(d string) bool { return strings.EqualFold(d, name) })
}

func withoutName(list []string, name string) []string {
	return slices.DeleteFunc(slices.Clone(list), func(d string) bool { return strings.EqualFold(d, name) })
}

// Linked is the devices a setting is kept in step with.
func (s Share) Linked(id string) []string { return s.Links[id] }

// LinkedWith is whether a setting is kept in step with a device, by its name however cased.
func (s Share) LinkedWith(id, device string) bool { return hasName(s.Links[id], device) }

// WaitingFor is whether a device has been asked to link a setting and has not accepted yet.
func (s Share) WaitingFor(id, device string) bool { return hasName(s.Waiting[id], device) }

// Kept is whether this device keeps a setting its own.
func (s Share) Kept(id string) bool { return slices.Contains(s.Keep, id) }

type ShareWriter struct{ st *Store }

func (w ShareWriter) On(v bool) error {
	return w.st.Update(func(c *Config) { c.Share.On = v })
}

// Keep sets whether this device keeps a setting its own.
func (w ShareWriter) Keep(id string, keep bool) error {
	return w.st.Update(func(c *Config) {
		i := slices.Index(c.Share.Keep, id)
		switch {
		case keep && i < 0:
			c.Share.Keep = append(c.Share.Keep, id)
			slices.Sort(c.Share.Keep)
		case !keep && i >= 0:
			c.Share.Keep = slices.Delete(c.Share.Keep, i, i+1)
		}
	})
}

// Update changes the sharing settings as one write: for several changes made together, such as where
// each of the settings a message carried came from.
func (w ShareWriter) Update(f func(s *Share)) error {
	return w.st.Update(func(c *Config) { f(&c.Share) })
}

// AddLink links a setting with a device, unless it already is or the setting has MaxLinked; answers
// whether it is linked now. Asking and being asked about it are settled.
func AddLink(s *Share, id, device string) bool {
	s.Requests = slices.DeleteFunc(s.Requests, func(r ShareRequest) bool {
		return r.ID == id && strings.EqualFold(r.Device, device)
	})
	if hasName(s.Links[id], device) {
		return true
	}
	if len(s.Links[id]) >= MaxLinked {
		return false
	}
	if s.Links == nil {
		s.Links = map[string][]string{}
	}
	s.Links[id] = append(s.Links[id], device)
	return true
}

// RemoveLink unlinks a setting from a device, and forgets any request either way.
func RemoveLink(s *Share, id, device string) {
	if l := withoutName(s.Links[id], device); len(l) > 0 {
		s.Links[id] = l
	} else {
		delete(s.Links, id)
	}
	SetWaiting(s, id, device, false, time.Time{})
	s.Requests = slices.DeleteFunc(s.Requests, func(r ShareRequest) bool {
		return r.ID == id && strings.EqualFold(r.Device, device)
	})
}

// SetWaiting marks a device asked to link a setting, or no longer waited on.
func SetWaiting(s *Share, id, device string, waiting bool, now time.Time) {
	l := withoutName(s.Waiting[id], device)
	if waiting {
		l = append(l, device)
		if _, ok := s.AskedAt[AskKey(id, device)]; !ok {
			if s.AskedAt == nil {
				s.AskedAt = map[string]time.Time{}
			}
			s.AskedAt[AskKey(id, device)] = now
		}
	} else {
		delete(s.AskedAt, AskKey(id, device))
	}
	if len(l) > 0 {
		if s.Waiting == nil {
			s.Waiting = map[string][]string{}
		}
		s.Waiting[id] = l
	} else {
		delete(s.Waiting, id)
	}
}

// AddRequest keeps a device's request to link a setting, once, the oldest going past MaxRequests.
func AddRequest(s *Share, id, device string, at time.Time) {
	if slices.ContainsFunc(s.Requests, func(r ShareRequest) bool { return r.ID == id && strings.EqualFold(r.Device, device) }) {
		return
	}
	s.Requests = append(s.Requests, ShareRequest{ID: id, Device: device, At: at})
	if n := len(s.Requests) - MaxRequests; n > 0 {
		s.Requests = slices.Delete(s.Requests, 0, n)
	}
}

// Decline forgets a device's request to link a setting, unlinks it, and remembers the decline, the
// oldest going past MaxRequests.
func Decline(s *Share, id, device string, at time.Time) {
	RemoveLink(s, id, device)
	if s.WasDeclined(id, device) {
		return
	}
	s.Declined = append(s.Declined, ShareRequest{ID: id, Device: device, At: at})
	if n := len(s.Declined) - MaxRequests; n > 0 {
		s.Declined = slices.Delete(s.Declined, 0, n)
	}
}

// Undecline forgets that a device's request to link a setting was declined: this device asks it itself.
func Undecline(s *Share, id, device string) {
	s.Declined = slices.DeleteFunc(s.Declined, func(r ShareRequest) bool { return r.ID == id && strings.EqualFold(r.Device, device) })
}
