package streaming

import "testing"

func TestSoloistCoverChoosesLargestSecureImage(t *testing.T) {
	covers := []soloistCover{{"https://example.invalid/64", "small"}, {"https://example.invalid/640", "xlarge"}, {"https://example.invalid/300", "large"}}
	for range len(covers) {
		if got := soloistCoverURL(covers); got != "https://example.invalid/640" {
			t.Fatalf("selected %q", got)
		}
		covers = append(covers[1:], covers[0])
	}
	if got := soloistCoverURL([]soloistCover{{"http://example.invalid/640", "xlarge"}, {"https://example.invalid/300", "large"}}); got != "https://example.invalid/300" {
		t.Fatal("insecure image selected")
	}
}
