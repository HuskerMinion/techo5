//go:build !dot

package video

import (
	"slices"
	"strings"
	"testing"
)

// The decoder opens the network and nothing else, whoever asked for the video, an add-on included:
// no file, no concat, no subfile, no pipe, nothing that reads the device's own storage. hls and crypto
// open what they name through the same list, so they reach nothing it does not.
func TestTheDecoderOpensOnlyTheNetwork(t *testing.T) {
	allowed := strings.Split(protocols, ",")
	for _, p := range []string{"file", "concat", "subfile", "pipe", "fd", "data", "cache", "async", "unix"} {
		if slices.Contains(allowed, p) {
			t.Errorf("the decoder may open %s:", p)
		}
	}
	for _, args := range [][]string{ProbeArgs("http://x/a.mp4", "", false, ""), DecodeArgs(Decoding{URL: "http://x/a.mp4"})} {
		i := slices.Index(args, "-protocol_whitelist")
		if i < 0 || args[i+1] != protocols || i > slices.Index(args, "-i") && slices.Index(args, "-i") >= 0 {
			t.Errorf("the whitelist is not before the input: %q", args)
		}
	}
}
