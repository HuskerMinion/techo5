// Package improv lets a phone give the device its Wi-Fi over Bluetooth, for a device with no screen to
// type a passphrase on (Here): the Echo Dot and the Echo 2nd gen. Without it, such a device taken to
// another network had to go back on the USB cable and through the installer.
//
// It speaks Improv Wi-Fi (improv-wifi.com), which Home Assistant, ESPHome's web tools and the Improv
// page in a Chromium browser all know: they find the device over Bluetooth LE, send a network name
// and passphrase, and are handed the setup page's address once it is on.
//
// Improv has no pairing or encryption of its own, so the device offers it only briefly and only when
// it should:
//
//   - by itself, once a boot, when it still has no address bootGrace after starting. Then a press of
//     any button on the device is needed before a network is taken (Improv's "authorization"), so a
//     stranger in Bluetooth range cannot move it onto a network of theirs.
//   - when the mute button is held until the long hold (five seconds on the Dot). Holding it is
//     already being at the device, so a network is taken at once. Holding it again closes it.
//
// Either way it stops after idle with nothing sent, after most at the latest, and doneHold after a
// network was joined.
package improv
