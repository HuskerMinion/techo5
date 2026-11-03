//go:build !dot

package display

import "fmt"

// What the screens say about announcements, shared by the Show's drawer and the Spot's menu so the
// two cannot drift into saying it differently.

// devicesText is how many others will hear it, in words rather than a bare number.
func devicesText(n int) string {
	if n == 1 {
		return tr("drawer_one_device")
	}
	return fmt.Sprintf(tr("drawer_device_count"), n)
}
