package redact

import (
	"strings"
	"testing"
)

// Lines taken from a real bundle. A redactor that mangles the log is not safer, it is only less
// useful: the whole point of the bundle is that somebody can read it in an issue.
func TestOrdinaryLogLinesSurvive(t *testing.T) {
	for _, line := range []string{
		"I [ 624.90] wake frames per_second=50.03029410868278 mean_us=4127 worst_ms=16 busy_pct=20",
		"I [ 761.04] wake detected slot=1 peak=0.8235294117647058 crossing=0.796078431372549",
		"I [ 26.43] Sync #1: rtt=4430μs, offset=-1789717437327089μs, error=1108μs",
		"I [ 597.63] sendspin correction off_ms=7 from=27464704 anchor_frame=19660253 corrected=813",
		"I [ 26.36] Group update: id=d631b2b6-0dbc-47a6-b5d3-84a6e149187a, state=stopped",
	} {
		if got := New().Text(line); got != line {
			t.Errorf("a log line was changed:\n old: %s\n new: %s", line, got)
		}
	}
}

// And the numbers that are somebody's still go. Which placeholder they get is not the point — a SIP
// address is taken as an address rather than a telephone number, and either way the number is gone.
func TestNumbersWorthHidingStillGo(t *testing.T) {
	for _, c := range []struct{ in, gone string }{
		{"phone: contact Alex number=15551234567 ok", "15551234567"},
		{"calling +15557654321 now", "+15557654321"},
		{"sip:15551112222@sip.example.net", "15551112222"},
	} {
		got := New().Text(c.in)
		if strings.Contains(got, c.gone) {
			t.Errorf("%q kept %s: %s", c.in, c.gone, got)
		}
		if !strings.Contains(got, "<") {
			t.Errorf("%q was not replaced with anything: %s", c.in, got)
		}
	}
}
