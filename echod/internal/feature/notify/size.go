package notify

import "strings"

// Size is how large a notification's words are drawn.
type Size string

const (
	SizeNormal Size = ""      // the usual card
	SizeLarge  Size = "large" // a bigger card with bigger words
	SizeHuge   Size = "huge"  // a short message across most of the screen
)

// ParseSize reads the size argument of notify and notify_picture: "large" or "huge" in any letter
// case, and the normal size for empty or any other text, so a call that leaves it out is unchanged.
func ParseSize(s string) Size {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "large":
		return SizeLarge
	case "huge":
		return SizeHuge
	}
	return SizeNormal
}
