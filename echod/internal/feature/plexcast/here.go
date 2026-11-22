//go:build !dot

package plexcast

// Here is whether this device is a Plex player: every one with a screen is. The Dot and the Echo have
// a light ring where the Show has a screen, so there is nothing to show a PIN on and nothing to cast a
// film to (none.go).
const Here = true
