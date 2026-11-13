package main

import (
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// Typing into a page (techo5#493). The device draws pictures and sends touches, so it can't see a text
// field being tapped. fieldScript watches the page for a text field getting or losing focus and tells
// dashcast through fieldBinding; dashcast tells the device (kindKeyboard), which puts its own keyboard
// up; the keys come back as lines (touchMsg's text and key) and are typed into the field as a real
// keyboard would, so the page's own search and input handling react as they always do.

// fieldBinding is the name the page calls to say a field got or lost focus.
const fieldBinding = "techo5Field"

// fieldValueMax and fieldLabelMax bound what a page can have sent to the device: the field's text,
// to start the device's own from, and its label, for the keyboard's title.
const (
	fieldValueMax = 500
	fieldLabelMax = 80
)

// fieldScript runs in every document the tab loads. Home Assistant's fields are inside components'
// shadow roots, but focus events are composed: they reach the window with the real <input> first in
// their path. Only fields a keyboard types into count: text, search and the like, and a textarea.
// Losing focus is said a moment later, and only if no other field took it, so moving from one field to
// the next keeps the keyboard up, into a frame on the page included. A password's text is never sent,
// nor that of a field the page marks as a password, a one-time code or a card. Only focus that follows a
// touch counts: a field focused by the page itself (autofocus, a sign-in page) does not put a keyboard
// over a dashboard nobody touched.
const fieldScript = `(() => {
  const kinds = new Set(["text", "search", "email", "url", "tel", "password", "number"]);
  const fieldOf = (e) => {
    const t = e.composedPath ? e.composedPath()[0] : e.target;
    if (!t || t.readOnly || t.disabled) return null;
    if (t.tagName === "TEXTAREA") return t;
    if (t.tagName === "INPUT" && kinds.has((t.type || "text").toLowerCase())) return t;
    return null;
  };
  const say = (o) => { try { if (typeof window.` + fieldBinding + ` === "function") window.` + fieldBinding + `(JSON.stringify(o)); } catch (e) {} };
  let current = null, touched = 0;
  const secret = (f) => /password|one-time-code|cc-/.test(String(f.autocomplete || f.getAttribute("autocomplete") || "").toLowerCase());
  const active = () => {
    let a = document.activeElement;
    while (a && a.shadowRoot && a.shadowRoot.activeElement) a = a.shadowRoot.activeElement;
    return a;
  };
  const up = (f) => {
    current = f;
    const kind = f.tagName === "TEXTAREA" ? "textarea" : (f.type || "text").toLowerCase();
    const host = f.getRootNode && f.getRootNode().host;
    let label = f.getAttribute("aria-label") || f.placeholder || "";
    if (!label && host) label = host.label || (host.getAttribute && host.getAttribute("label")) || "";
    // Typing goes on at the end of what is there, as the device's own field shows it.
    try { const n = f.value.length; f.setSelectionRange(n, n); } catch (e) {}
    say({up: true, kind, value: kind === "password" || secret(f) ? "" : String(f.value || "").slice(0, 500), label: String(label).slice(0, 80)});
  };
  // A touch on a field the page focused by itself gives no focusin: the touch brings the keyboard then.
  const touch = (e) => {
    touched = Date.now();
    const f = fieldOf(e);
    if (f && f !== current && f === active()) up(f);
  };
  addEventListener("pointerdown", touch, true);
  addEventListener("touchstart", touch, true);
  addEventListener("focusin", (e) => {
    const f = fieldOf(e);
    if (f && Date.now() - touched <= 3000) up(f); // focus can follow a tap by a second or more
  }, true);
  addEventListener("focusout", (e) => {
    const f = fieldOf(e);
    if (!f || f !== current) return;
    current = null;
    // Focus gone into a frame is a field there, whose own copy of this says so.
    setTimeout(() => { if (!current && !(document.activeElement && document.activeElement.tagName === "IFRAME")) say({up: false}); }, 0);
  }, true);
})();`

// blurScript takes focus off whatever field has it, inside shadow roots too: the device's Done, and a
// tab being parked, so a field tapped again later gets focus again and brings the keyboard back up.
const blurScript = `(() => {
  let a = document.activeElement;
  while (a && a.shadowRoot && a.shadowRoot.activeElement) a = a.shadowRoot.activeElement;
  if (a && a.blur) a.blur();
})()`

// watchFields has the tab say when a field gets or loses focus (fieldScript, through fieldBinding).
// Run before the tab's first navigation, so it is in every document from the first.
func watchFields(ctx context.Context) error {
	if err := runtime.AddBinding(fieldBinding).Do(ctx); err != nil {
		return err
	}
	_, err := page.AddScriptToEvaluateOnNewDocument(fieldScript).Do(ctx)
	return err
}

// field is what the device is told about a field: up with its kind, text and label, or down.
type field struct {
	Up    bool   `json:"up"`
	Kind  string `json:"kind,omitempty"`
	Value string `json:"value,omitempty"`
	Label string `json:"label,omitempty"`
}

// fieldKinds are the kinds the script sends; anything else is taken for text.
var fieldKinds = map[string]bool{"text": true, "search": true, "email": true, "url": true, "tel": true, "password": true, "number": true, "textarea": true}

// parseField reads what the page said, which any script in it could have said: it is held to what
// fieldScript sends, cut to the bounds, and a password's text is dropped here too.
func parseField(payload string) (field, bool) {
	if len(payload) > 4*(fieldValueMax+fieldLabelMax)+200 {
		return field{}, false
	}
	var f field
	if json.Unmarshal([]byte(payload), &f) != nil {
		return field{}, false
	}
	if !f.Up {
		return field{}, true
	}
	if !fieldKinds[f.Kind] {
		f.Kind = "text"
	}
	if f.Kind == "password" {
		f.Value = ""
	}
	f.Value = cut(f.Value, fieldValueMax)
	f.Label = cut(strings.TrimSpace(f.Label), fieldLabelMax)
	return f, true
}

// cut is s in at most n characters, and valid UTF-8.
func cut(s string, n int) string {
	s = strings.ToValidUTF8(s, "")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// keys the device can press besides typing text, as Chrome names them, with their Windows key codes.
var keys = map[string]int64{"Backspace": 8, "Enter": 13}

// typeText types s into the field that has focus.
func typeText(ctx context.Context, s string) error {
	if s == "" {
		return nil
	}
	return input.InsertText(s).Do(ctx)
}

// pressKey presses one of keys, or "done": the keyboard put away, the field let go of.
func pressKey(ctx context.Context, k string) error {
	if k == "done" {
		_, exc, err := runtime.Evaluate(blurScript).Do(ctx)
		if err == nil && exc != nil {
			return exc
		}
		return err
	}
	code, ok := keys[k]
	if !ok {
		return nil
	}
	down := input.DispatchKeyEvent(input.KeyRawDown).WithKey(k).WithCode(k).WithWindowsVirtualKeyCode(code).WithNativeVirtualKeyCode(code)
	if k == "Enter" {
		// The text a key makes is what fires keypress and submits a form.
		down = input.DispatchKeyEvent(input.KeyDown).WithKey(k).WithCode(k).WithWindowsVirtualKeyCode(code).WithNativeVirtualKeyCode(code).WithText("\r").WithUnmodifiedText("\r")
	}
	if err := down.Do(ctx); err != nil {
		return err
	}
	return input.DispatchKeyEvent(input.KeyUp).WithKey(k).WithCode(k).WithWindowsVirtualKeyCode(code).WithNativeVirtualKeyCode(code).Do(ctx)
}

// blur lets go of any field with focus, for a tab about to be parked: given a moment of its own, so a
// slow page is parked anyway rather than closed for it.
func blur(tab context.Context) {
	ctx, cancel := context.WithTimeout(tab, 2*time.Second)
	defer cancel()
	_ = chromedp.Run(ctx, chromedp.Evaluate(blurScript, nil))
}
