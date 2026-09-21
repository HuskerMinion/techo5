//go:build !dot

package display

import "strconv"

// What the screens say about announcements, shared by the Show's drawer and the Spot's menu so the
// two cannot drift into saying it differently.

// devicesText is how many others will hear it, in words rather than a bare number.
func devicesText(n int) string {
	if n == 1 {
		return "1 other device"
	}
	return strconv.Itoa(n) + " other devices"
}
