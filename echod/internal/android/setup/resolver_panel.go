//go:build panel

package setup

// resolver is nothing on a panel. The app's echod is a GOOS=android build with cgo, and Go resolves
// through bionic's getaddrinfo there, which asks Android's own resolver (netd) for the network the
// tablet is on. Pointing Go's resolver at nameservers instead would leave it with none: an app on
// Android 8 and later cannot read the net.dns properties, and on Android 10 and later not
// /proc/net/route either, so every lookup would fail.
var resolver []Action
