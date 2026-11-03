package bluetooth

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/ygelfand/go-esphome-device/api"
	"google.golang.org/protobuf/proto"
)

type fakeSub struct {
	info string
	err  error
}

func (f *fakeSub) Send(proto.Message) error { return f.err }
func (f *fakeSub) ClientInfo() string       { return f.info }

func captureLog(t *testing.T) *bytes.Buffer {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(old) })
	return &buf
}

func TestTakeoverWarnsWhenPreviousStillConnected(t *testing.T) {
	buf := captureLog(t)
	var tk takeover
	a, b := &fakeSub{info: "Home Assistant A"}, &fakeSub{info: "Home Assistant B"}
	tk.note(a, true, noSlots)
	tk.note(a, true, noSlots) // the same connection asking again is not a takeover
	if buf.Len() != 0 {
		t.Fatalf("logged without a takeover: %s", buf)
	}
	tk.note(b, true, noSlots)
	for _, want := range []string{"took over", "Home Assistant A", "Home Assistant B"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("log %q lacks %q", buf, want)
		}
	}
}

func TestTakeoverQuietWhenPreviousGoneOrUnsubscribed(t *testing.T) {
	buf := captureLog(t)
	var tk takeover
	a, b, c := &fakeSub{info: "A", err: errors.New("closed")}, &fakeSub{info: "B"}, &fakeSub{info: "C"}
	tk.note(a, true, noSlots)
	tk.note(b, true, noSlots) // a is gone: an ordinary reconnect
	tk.note(b, false, nil)
	tk.note(c, true, noSlots) // nobody subscribed
	if buf.Len() != 0 {
		t.Errorf("logged: %s", buf)
	}
}

// noSlots is the free-slots reply of a proxy that lends no connections.
func noSlots() proto.Message { return &api.BluetoothConnectionsFreeResponse{} }
