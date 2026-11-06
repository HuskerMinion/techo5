//go:build !dot && !spot

package display

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
)

// withCallScreen runs the rest of the test with Call screen set to style, and puts it back after.
func withCallScreen(t *testing.T, style callBarStyle) {
	t.Helper()
	was := callScreen.Load()
	callScreen.Store(int32(style))
	t.Cleanup(func() { callScreen.Store(was) })
}

// Each choice keeps the style its frames draw, and Full screen, the first, is what a config without
// the setting reads as.
func TestTheCallScreenChoices(t *testing.T) {
	want := []callBarStyle{callBarNone, callBarButton, callBarDetails}
	if len(callScreens) != len(want) || callScreens[0].value != "" {
		t.Fatalf("choices %v", callScreens)
	}
	for i, c := range callScreens {
		if c.style != want[i] || callScreenOptions()[i] != c.label {
			t.Errorf("choice %d: %+v", i, c)
		}
	}
}

// Only a call being placed or up is drawn over the page, and only with the setting saying so. A call
// ringing is answered or declined on its page, and a Drop In says on the whole screen that somebody
// is listening.
func TestTheCallIsOverThePageOnlyPlacedOrUp(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 7, 0, 0, time.Local)
	for _, c := range []struct {
		name string
		st   phone.State
		over bool
	}{
		{"idle", phone.State{}, false},
		{"ringing", phone.State{Phase: phone.Ringing, Peer: "104", Incoming: true}, false},
		{"calling", phone.State{Phase: phone.Dialing, Peer: "104"}, true},
		{"waiting to dial", phone.State{Phase: phone.Dialing, Peer: "104", Waiting: true}, true},
		{"talking", phone.State{Phase: phone.Talking, Peer: "104", Since: at}, true},
		{"intercom", phone.State{Phase: phone.Talking, Peer: "Kitchen", Intercom: true, Since: at}, true},
		{"drop in", phone.State{Phase: phone.Talking, Peer: "Kitchen", Intercom: true, DropIn: true, Since: at}, false},
	} {
		withCallScreen(t, callBarNone)
		if callBarOn(c.st) != callBarNone {
			t.Errorf("%s: over the page with Full screen", c.name)
		}
		for _, style := range []callBarStyle{callBarButton, callBarDetails} {
			withCallScreen(t, style)
			want := callBarNone
			if c.over {
				want = style
			}
			if got := callBarOn(c.st); got != want {
				t.Errorf("%s, style %d: got %d", c.name, style, got)
			}
		}
	}
}

// drawCall draws s at the panel's size with the call in it, over the page in style, or on its page.
func drawCall(wide, high int, s scene, call phone.State, style callBarStyle) (*image.RGBA, *renderer) {
	s.call, s.callBar = call, style
	img := image.NewRGBA(image.Rect(0, 0, wide, high))
	r := newRenderer(img)
	r.draw(s)
	return img, r
}

// drawnAt is where the renderer last drew the call over the page, and its Hang up.
func drawnAt(r *renderer) (all, hang image.Rectangle) {
	r.zmu.Lock()
	defer r.zmu.Unlock()
	return r.callBarAt, r.hangAt
}

// outsideSame is whether a and b are the same everywhere but inside keep.
func outsideSame(a, b *image.RGBA, keep image.Rectangle) bool {
	for y := a.Rect.Min.Y; y < a.Rect.Max.Y; y++ {
		for x := a.Rect.Min.X; x < a.Rect.Max.X; x++ {
			if image.Pt(x, y).In(keep) {
				continue
			}
			i := a.PixOffset(x, y)
			if a.Pix[i] != b.Pix[i] || a.Pix[i+1] != b.Pix[i+1] || a.Pix[i+2] != b.Pix[i+2] {
				return false
			}
		}
	}
	return true
}

// Hang up is where the home screen's Call button is, the same size, and the call over the page leaves
// the page as it was: outside the button, its details and their shadows, the frame is the page without
// the call, pixel for pixel. The call's page, drawn instead, is not.
func TestTheCallOverThePageLeavesThePageAsItWas(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 7, 0, 0, time.Local)
	call := phone.State{Configured: true, Registered: true, Phase: phone.Talking, Peer: "104", Since: at.Add(-75 * time.Second)}
	for _, panel := range showPanels() {
		page := scene{now: at, phase: "idle"}
		plain, _ := drawCall(panel.wide, panel.high, page, phone.State{}, callBarNone)
		full, rf := drawCall(panel.wide, panel.high, page, call, callBarNone)
		if all, _ := drawnAt(rf); !all.Empty() {
			t.Errorf("%s: the call's page left a button to tap at %v", panel.name, all)
		}
		for _, style := range []callBarStyle{callBarButton, callBarDetails} {
			over, r := drawCall(panel.wide, panel.high, page, call, style)
			all, hang := drawnAt(r)
			if hang != r.callButtonRect() {
				t.Errorf("%s/%d: Hang up at %v, the Call button at %v", panel.name, style, hang, r.callButtonRect())
			}
			if style == callBarButton && all != hang {
				t.Errorf("%s: the button alone drew %v", panel.name, all)
			}
			if style == callBarDetails && (all.Dx() <= hang.Dx() || all.Max.X > panel.wide-r.margin || all.Min.Y != hang.Min.Y || all.Max.Y != hang.Max.Y) {
				t.Errorf("%s: the details at %v beside Hang up at %v", panel.name, all, hang)
			}
			if !outsideSame(plain, over, all.Inset(-r.s(20))) {
				t.Errorf("%s/%d: the call changed the page outside its corner", panel.name, style)
			}
			if same(over.Pix, plain.Pix) {
				t.Errorf("%s/%d: nothing drawn", panel.name, style)
			}
			if outsideSame(plain, full, all.Inset(-r.s(20))) {
				t.Errorf("%s/%d: the call's own page left the page as it was", panel.name, style)
			}
		}
	}
}

// Under its page a notification's card is not seen; with Hang up over the page it is.
func TestANotificationIsSeenOverACall(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 7, 0, 0, time.Local)
	call := phone.State{Phase: phone.Talking, Peer: "104", Since: at.Add(-75 * time.Second)}
	note := notify.Notification{Kind: notify.Text, Title: "Washer", Message: "The washer is done"}
	for _, panel := range showPanels() {
		quiet := scene{now: at, phase: "idle"}
		card := quiet
		card.notification, card.showNotification = note, true

		fullQuiet, _ := drawCall(panel.wide, panel.high, quiet, call, callBarNone)
		fullCard, _ := drawCall(panel.wide, panel.high, card, call, callBarNone)
		if !same(fullQuiet.Pix, fullCard.Pix) {
			t.Errorf("%s: a notification showed through the call's page", panel.name)
		}
		for _, style := range []callBarStyle{callBarButton, callBarDetails} {
			overQuiet, _ := drawCall(panel.wide, panel.high, quiet, call, style)
			overCard, _ := drawCall(panel.wide, panel.high, card, call, style)
			if same(overQuiet.Pix, overCard.Pix) {
				t.Errorf("%s/%d: the notification is not seen", panel.name, style)
			}
		}
	}
}

// The details change with the call: placed or up, the doorbell pressed, the time. The button alone
// stays where it is. Whatever the length of the name, the details stay inside the margins. With
// SHOW_PREVIEW set, each is written there to look at.
func TestTheDetailsSayWhatTheCallIsDoing(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 7, 0, 0, time.Local)
	talking := phone.State{Phase: phone.Talking, Peer: "104", Since: at.Add(-75 * time.Second)}
	long := talking
	long.Peer = strings.Repeat("A very long caller ID name ", 8)
	dir := os.Getenv("SHOW_PREVIEW")
	for _, panel := range showPanels() {
		page := scene{now: at, phase: "idle"}
		door := page
		door.atDoor = true
		later := talking
		later.Since = talking.Since.Add(-time.Minute)
		_, rb := drawCall(panel.wide, panel.high, page, talking, callBarDetails)
		base, _ := drawnAt(rb)
		baseImg, _ := drawCall(panel.wide, panel.high, page, talking, callBarDetails)
		for name, c := range map[string]struct {
			s    scene
			call phone.State
		}{
			"talking": {page, talking},
			"calling": {page, phone.State{Phase: phone.Dialing, Peer: "104"}},
			"at-door": {door, talking},
			"later":   {page, later},
			"long":    {page, long},
		} {
			img, r := drawCall(panel.wide, panel.high, c.s, c.call, callBarDetails)
			all, hang := drawnAt(r)
			if hang != r.callButtonRect() || all.Max.X > panel.wide-r.margin {
				t.Errorf("%s/%s: details %v, Hang up %v", panel.name, name, all, hang)
			}
			if name != "talking" && same(img.Pix, baseImg.Pix) {
				t.Errorf("%s/%s: the details look the same as a plain call's at %v", panel.name, name, base)
			}
			button, rb := drawCall(panel.wide, panel.high, c.s, c.call, callBarButton)
			if a, h := drawnAt(rb); a != h || h != rb.callButtonRect() {
				t.Errorf("%s/%s: the button alone at %v, %v", panel.name, name, a, h)
			}
			if dir == "" {
				continue
			}
			for file, pic := range map[string]*image.RGBA{"details": img, "button": button} {
				f, err := os.Create(filepath.Join(dir, "call-"+file+"-"+name+"-"+panel.name+".png"))
				if err != nil {
					t.Fatal(err)
				}
				if err := png.Encode(f, pic); err != nil {
					t.Fatal(err)
				}
				f.Close()
			}
		}
	}
}

// A tap on Hang up hangs up, a little room around it included, as around the Call button; a tap on
// the details is theirs and does nothing; a tap anywhere else is the page's. A frame with no call over
// the page leaves nothing to tap.
func TestHangUpTakesOnlyItsOwnTaps(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 7, 0, 0, time.Local)
	call := phone.State{Phase: phone.Talking, Peer: "104", Since: at}
	mid := func(b image.Rectangle) image.Point { return image.Pt((b.Min.X+b.Max.X)/2, (b.Min.Y+b.Max.Y)/2) }
	for _, panel := range showPanels() {
		img := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
		r := newRenderer(img)
		r.draw(scene{now: at, phase: "idle", call: call, callBar: callBarDetails})
		all, hang := drawnAt(r)
		for name, c := range map[string]struct {
			p        image.Point
			on, hang bool
		}{
			"hang up":         {mid(hang), true, true},
			"next to hang up": {image.Pt(hang.Min.X-r.s(callBarReach)/2, mid(hang).Y), true, true},
			"above hang up":   {image.Pt(mid(hang).X, hang.Min.Y-r.s(callBarReach)/2), true, true},
			"the details":     {image.Pt(all.Max.X-r.s(callBarPad), mid(all).Y), true, false},
			"the page":        {image.Pt(panel.wide/2, panel.high/2), false, false},
			"the top":         {image.Pt(panel.wide/2, r.s(20)), false, false},
			"right of all":    {image.Pt(all.Max.X+r.s(callBarReach)*3, mid(all).Y), false, false},
			"well above":      {image.Pt(mid(hang).X, hang.Min.Y-r.s(callBarReach)*3), false, false},
		} {
			on, h := r.callBarTapped(c.p)
			if on != c.on || h != c.hang {
				t.Errorf("%s/%s at %v: on %v hang %v, want %v %v", panel.name, name, c.p, on, h, c.on, c.hang)
			}
		}
		if !r.callBarDrawn() {
			t.Errorf("%s: what was drawn is not said to be", panel.name)
		}
		r.draw(scene{now: at, phase: "idle"})
		if r.callBarDrawn() {
			t.Errorf("%s: a frame without the call left it to tap", panel.name)
		}
		if on, h := r.callBarTapped(mid(hang)); on || h {
			t.Errorf("%s: Hang up still taken after the call went", panel.name)
		}
	}
}

// A page that asks for an answer at its foot, where Hang up would be, brings the call's page back:
// it already deals with a ring behind the call, and holds the others back as it always has.
func TestAnAnswerAskedForBringsTheCallPageBack(t *testing.T) {
	at := time.Date(2026, 11, 5, 14, 7, 0, 0, time.Local)
	call := phone.State{Phase: phone.Talking, Peer: "104", Since: at}
	for name, s := range map[string]scene{
		"ringing":    {ring: ringState{timer: "Pasta"}},
		"setup asks": {setupAsking: true},
		"video asks": {showVideoAsk: true},
		"pin":        {pin: pinView{open: true}},
	} {
		s.now, s.phase = at, "idle"
		for _, style := range []callBarStyle{callBarButton, callBarDetails} {
			full, _ := drawCall(showWide, showHigh, s, call, callBarNone)
			over, r := drawCall(showWide, showHigh, s, call, style)
			if all, _ := drawnAt(r); !all.Empty() {
				t.Errorf("%s/%d: Hang up drawn over it at %v", name, style, all)
			}
			if !same(full.Pix, over.Pix) {
				t.Errorf("%s/%d: not the call's page", name, style)
			}
		}
	}
}

// What the details say, in less room each time: the name gives way first, cut with an ellipsis while
// three of its letters are left, then whole; then the time or the doorbell; and what the call is doing
// stays, unclipped while it fits at all.
func TestTheDetailsGiveWayInOrder(t *testing.T) {
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	face, gap := r.small, r.s(callBarGap)
	what, who, door := "On a call", "A very long caller ID name", "Someone's at the door"
	full := r.width(face, what+"  ·  "+who) + gap + r.width(face, door)
	for room := full; room >= r.width(face, what); room -= r.s(4) {
		label, when := r.callBarLine(face, what, who, door, room, gap)
		used := r.width(face, label)
		if when != "" {
			used += gap + r.width(face, when)
		}
		if used > room {
			t.Fatalf("room %d: %q %q takes %d", room, label, when, used)
		}
		if strings.HasPrefix(label, what+"  ·  ") && strings.HasSuffix(label, ellipsis) {
			cut := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(label, what+"  ·  "), ellipsis))
			if len([]rune(cut)) < 3 {
				t.Errorf("room %d: the name cut to %q", room, cut)
			}
		}
		if when == "" && r.width(face, what)+gap+r.width(face, door) <= room {
			t.Errorf("room %d: the doorbell went while the name could still give way: %q", room, label)
		}
		if label != what && !strings.HasPrefix(label, what+"  ·  ") {
			t.Errorf("room %d: %q", room, label)
		}
	}
	if label, when := r.callBarLine(face, what, who, door, full, gap); label != what+"  ·  "+who || when != door {
		t.Errorf("all the room: %q %q", label, when)
	}
	if label, when := r.callBarLine(face, what, who, door, r.width(face, what)+gap+r.width(face, door), gap); label != what || when != door {
		t.Errorf("room for what and the doorbell only: %q %q", label, when)
	}
}
