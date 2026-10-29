//go:build !dot

package display

import "slices"

// replyPage is the lines of a reply to show when only fit of them fit: all of them when they do,
// otherwise the page of fit lines the voice is on, said being how much of the reply it has said (0 to
// 1). Pages are counted in characters, the way the voice gets through them (#333). A page with more
// after it ends in "…", as the Spot's do.
func replyPage(lines []string, fit int, said float64) []string {
	if fit <= 0 {
		return nil
	}
	if len(lines) <= fit {
		return lines
	}
	total := 0
	for _, l := range lines {
		total += len([]rune(l))
	}
	at := int(said * float64(total))
	start, chars := 0, 0
	for i := 0; i < len(lines); i += fit {
		if i > 0 && chars > at {
			break
		}
		start = i
		for _, l := range lines[i:min(i+fit, len(lines))] {
			chars += len([]rune(l))
		}
	}
	page := slices.Clone(lines[start:min(start+fit, len(lines))])
	if start+fit < len(lines) {
		page[len(page)-1] += " …"
	}
	return page
}
