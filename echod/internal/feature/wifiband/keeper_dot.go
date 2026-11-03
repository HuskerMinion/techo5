//go:build dot

package wifiband

// The Dot's images bring up their Wi-Fi without the boot scripts' network keeper, so there is nothing
// for the switch to tell.
const hasKeeper = false
