package announce

// popupID is whether id is how a Show names a calendar pop-up's event: "popup-" and 24 lowercase hex
// digits (display.popupMatch). A dismissal is kept in the settings until the event is past, so one
// naming anything else, long or short, is turned away before it can be saved.
func popupID(id string) bool {
	const prefix, digits = "popup-", 24
	if len(id) != len(prefix)+digits || id[:len(prefix)] != prefix {
		return false
	}
	for _, c := range id[len(prefix):] {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
