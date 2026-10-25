//go:build panel

package streaming

// Here: a panel does not offer AirPlay or Spotify Connect (here.go). They are programs the Echos'
// images carry (avahi, shairport-sync, librespot), and an app has none of them, so the switches could
// only ever be on and do nothing.
const Here = false
