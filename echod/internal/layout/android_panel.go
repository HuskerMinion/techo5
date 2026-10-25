//go:build panel

package layout

// OnAndroid is always true on a panel: the app only runs on Android. Its tests run on Linux, and see
// what a tablet has with it (component/all's inventory, Bluetooth audio left to Android).
func OnAndroid() bool { return true }
