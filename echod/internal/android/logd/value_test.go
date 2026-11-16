package logd

import (
	"log/slog"
	"testing"
)

// A value with a control character in it is quoted, so a name with a line break in it cannot start a
// line of its own in the log.
func TestAValueWithAControlCharacterIsQuoted(t *testing.T) {
	if got := value(slog.StringValue("Kitchen\nI [0.00] fake")); got != `"Kitchen\nI [0.00] fake"` {
		t.Errorf("value = %s", got)
	}
	if got := value(slog.StringValue("Kitchen")); got != "Kitchen" {
		t.Errorf("a plain value was changed: %s", got)
	}
}
