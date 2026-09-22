package layout

import "strings"

// EntitySlug is the prefix Home Assistant builds this device's entity ids from. It slugifies a
// friendly name its own way: everything that is not a letter or a digit becomes an underscore, and
// repeats collapse — "Guest's Desk" is "guest_s_desk", so its media player is
// media_player.guest_s_desk_speaker.
//
// Slug, with its dashes, is the node name and the mDNS host: a different thing for a different
// purpose. Using one where the other belongs names an entity that does not exist, which is silent,
// because nothing answers.
func EntitySlug(name string) string {
	var b strings.Builder
	under := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			under = false
		case b.Len() > 0 && !under:
			b.WriteByte('_')
			under = true
		}
	}
	return strings.Trim(b.String(), "_")
}
