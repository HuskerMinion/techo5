//go:build !dot && !spot

package display

import (
	"bytes"
	"image"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	"github.com/HuskerMinion/techo5/echod/internal/feature/notify"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
)

// Video replaces the whole page, even though its rendering returns before the
// ordinary page-reset block. It must publish no hitbox from the old sunrise.
func TestVideoReplacesTheSunriseSkipToken(t *testing.T) {
	now := time.Now()
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: now.Add(15 * time.Minute)}, label: "Wake up"}
	for _, panel := range showPanels() {
		t.Run(panel.name, func(t *testing.T) {
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
			light := scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer}
			r.draw(light)
			b := r.skipAt
			if b.Empty() {
				t.Fatal("the initial sunrise button was not drawn")
			}
			mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
			r.draw(scene{now: now, phase: "idle", showVideo: true, videoLive: true})
			if got, ok := r.skipTapped(mid); ok {
				t.Fatalf("video kept the hidden sunrise token: %+v", got)
			}
			r.draw(light)
			if got, ok := r.skipTapped(mid); !ok || got.token != offer.token {
				t.Fatalf("the newly drawn sunrise did not restore its offer: %+v %v", got, ok)
			}
		})
	}
}

// videoScene can lower videoOnScreen before the returning clock frame is drawn.
// A tap in that controlled interval must not reach the sunrise from before video.
func TestATapDuringVideoExitCannotSkipTheHiddenAlarm(t *testing.T) {
	now := time.Now()
	d := skipDisplay(t)
	if err := config.Set().Screen().ClockTap("nothing"); err != nil {
		t.Fatal(err)
	}
	asked := recordingSkip(t, nil)
	mid := drawLightPage(t, d, now, now.Add(15*time.Minute))
	d.videoOnScreen = true
	d.r.draw(scene{now: now, phase: "idle", showVideo: true, videoLive: true})
	returning := scene{now: now, phase: "idle"}
	d.videoScene(&returning, now)
	if d.videoUp() {
		t.Fatal("the stopped player's composition did not leave the video page")
	}
	// Deliberately do not draw returning: the panel still has the video frame.
	tapAt(d, mid)
	if len(*asked) != 0 {
		t.Fatalf("a tap on the old video frame skipped an alarm: %+v", *asked)
	}
	if note := d.skipNoteNow(now); note.exists() {
		t.Errorf("a tap with no skip button produced a skip answer: %q", skipWhole(note))
	}
}

// The result is the only answer a press gets. A secondary Bluetooth name must
// not squeeze it out; for these few seconds the answer has the footer to itself.
func TestASkipAnswerTakesTheFooterBeforeBluetooth(t *testing.T) {
	config.Use(t.TempDir() + "/state.json")
	at := time.Now().Add(15 * time.Minute)
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: at}, label: "Wake up"}
	for _, panel := range showPanels() {
		for _, light := range []float64{0, 1} {
			for _, ok := range []bool{false, true} {
				s := scene{now: at, phase: "idle", sunrise: light, skipNote: answerOf(offer, ok)}
				plain := image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))
				newRenderer(plain).footer(s)
				s.bt.Connected = strings.Repeat("A very long speaker name ", 30)
				connected := image.NewRGBA(plain.Rect)
				newRenderer(connected).footer(s)
				if !bytes.Equal(plain.Pix, connected.Pix) {
					t.Errorf("%s/light=%.1f/success=%v: Bluetooth displaced the skip answer", panel.name, light, ok)
				}
			}
		}
	}
}

// A card over the light's page leaves no button for a finger, and none for the frame still on the panel
// when the card goes: whoever dismisses it must not find a button under it that they never saw, and the
// alarm behind it must not be left out by a tap made at a place where nothing was drawn.
func TestACoveredLightPageLeavesNoButtonToTap(t *testing.T) {
	now := time.Now()
	for _, panel := range showPanels() {
		t.Run(panel.name, func(t *testing.T) {
			d := skipDisplay(t)
			d.r = newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
			if err := config.Set().Screen().ClockTap("nothing"); err != nil {
				t.Fatal(err)
			}
			asked := recordingSkip(t, nil)
			offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: now.Add(15 * time.Minute)}, label: "Wake up"}
			clear := scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer}
			d.r.draw(clear)
			b := d.r.skipAt
			if b.Empty() {
				t.Fatal("the light's page drew no button")
			}
			mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
			notify.Get().Dismiss()
			notify.Get().Note("Fictional card", "A touch dismisses this notification, not an alarm.")
			n, up := notify.Get().Card()
			if !up {
				t.Fatal("no card")
			}
			covered := clear
			covered.showNotification, covered.notification = true, n
			d.r.draw(covered)
			if !d.r.skipAt.Empty() {
				t.Fatalf("a covered page published a button at %v", d.r.skipAt)
			}
			// The card goes between frames: the panel still has the frame it was drawn on.
			notify.Get().Dismiss()
			tapAt(d, mid)
			if len(*asked) != 0 {
				t.Fatalf("a tap on a frame whose button was never drawn skipped %+v", *asked)
			}
			if note := d.skipNoteNow(now); note.exists() {
				t.Errorf("a tap with no button answered %q", skipWhole(note))
			}
			// The frame that draws the page again puts the button back, and the press it takes is the
			// button's.
			d.r.draw(clear)
			tapAt(d, mid)
			if len(*asked) != 1 || (*asked)[0].ID != "wake" {
				t.Fatalf("the frame that drew the button did not offer it: %+v", *asked)
			}
		})
	}
}

// The frames that own the foot leave the button out too, on every panel: the strips along the bottom,
// the drawer and the volume bar are drawn over it, so it is not a thing a finger could have seen.
func TestAFrameThatOwnsTheFootPublishesNoButton(t *testing.T) {
	now := time.Now()
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: now.Add(15 * time.Minute)}, label: "Wake up"}
	covered := []struct {
		name string
		over func(*scene)
	}{
		{"volume", func(s *scene) { s.showVolume = true }},
		{"drawer", func(s *scene) { s.showDrawer = true }},
		{"announcement", func(s *scene) { s.showAnnouncement = true }},
		{"recording", func(s *scene) { s.announceRecording = true }},
		{"update", func(s *scene) { s.updateActive = true }},
		{"notification", func(s *scene) { s.showNotification = true }},
		{"reminder", func(s *scene) { s.showReminder = true }},
	}
	for _, c := range covered {
		for _, panel := range showPanels() {
			t.Run(c.name+"/"+panel.name, func(t *testing.T) {
				r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
				clear := scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer}
				r.draw(clear)
				want := r.skipAt
				if want.Empty() {
					t.Fatal("the light's page drew no button")
				}
				over := clear
				c.over(&over)
				r.draw(over)
				if !r.skipAt.Empty() {
					t.Fatalf("%s: the frame published a button at %v", c.name, r.skipAt)
				}
				r.draw(clear)
				if got, ok := r.skipTapped(want.Min.Add(image.Pt(want.Dx()/2, want.Dy()/2))); !ok || got.token.ID != "wake" {
					t.Fatalf("%s: the page that drew the button again did not offer it: %+v %v", c.name, got, ok)
				}
			})
		}
	}
}

// The volume bar is drawn over the light's page, and a button under it is not one anybody saw: the page
// itself leaves it out, so the pixels and the press go together.
func TestTheVolumeBarLeavesNoButtonUnderIt(t *testing.T) {
	now := time.Now()
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: now.Add(15 * time.Minute)}, label: "Wake up"}
	for _, panel := range showPanels() {
		t.Run(panel.name, func(t *testing.T) {
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
			r.draw(scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer})
			b := r.skipAt
			drawn := image.NewRGBA(r.dst.Rect)
			copy(drawn.Pix, r.dst.Pix)
			r.draw(scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer, showVolume: true, volume: 3})
			if r.skipDrawn.Overlaps(b) {
				t.Errorf("the page drew the button under the volume bar: %v", r.skipDrawn)
			}
			if !r.skipAt.Empty() {
				t.Errorf("the volume bar's frame published a button: %v", r.skipAt)
			}
		})
	}
}

// The captions of a turn sit above the light's page's button, clear of the ring a tap just outside the
// button still counts in (skipTapped), so reading the answer cannot take the press meant for the alarm,
// and the centre of the button still reaches it.
func TestTheCaptionsSitAboveTheLightPagesButton(t *testing.T) {
	now := time.Now()
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: now.Add(15 * time.Minute)}, label: "Morning light"}
	for _, panel := range showPanels() {
		t.Run(panel.name, func(t *testing.T) {
			r := newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
			r.draw(scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer, cue: "replying",
				reply: strings.Repeat("A fictional long answer used to check captions and action controls. ", 12), since: now.Add(-time.Second)})
			b := r.skipAt
			if b.Empty() {
				t.Fatal("the light's page drew no button")
			}
			mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
			r.zmu.Lock()
			captions := r.captionsAt
			r.zmu.Unlock()
			if captions.Empty() {
				t.Fatal("the turn drew no captions")
			}
			if r.captionsTapped(mid) {
				t.Errorf("the captions intercept the button's centre %v", mid)
			}
			if ring := b.Inset(-r.s(8)); !captions.Intersect(ring).Empty() {
				t.Errorf("the captions %v cover the button's finger ring %v", captions, ring)
			}
			if got, ok := r.skipTapped(mid); !ok || got.token.ID != "wake" {
				t.Errorf("the centre of the button does not reach it: %+v %v", got, ok)
			}
		})
	}
}

// The footer's word for the music is the way back to the page only while a frame drew it: the answer to
// a press on the light's page has the foot, and the corner is no way back until a frame draws the word
// again.
func TestTheMusicTargetFollowsTheFrameThatDrewIt(t *testing.T) {
	now := time.Now()
	for _, panel := range showPanels() {
		for _, music := range []struct {
			name            string
			playing, paused bool
		}{
			{"playing", true, false}, {"paused", false, true},
		} {
			for _, page := range []struct {
				name    string
				sunrise float64
			}{
				{"clock", 0}, {"sunrise", 0.5},
			} {
				t.Run(panel.name+"/"+music.name+"/"+page.name, func(t *testing.T) {
					d := skipDisplay(t)
					if err := config.Set().Screen().ClockTap("nothing"); err != nil {
						t.Fatal(err)
					}
					d.r = newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high)))
					b := d.r.playingButton()
					at := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
					d.away = true
					d.skipNote = skipAnswer{label: "Wake up", at: now.Add(15 * time.Minute), skipped: true}
					d.skipNoteUntil = now.Add(skipNoteFor)
					s := scene{now: now, phase: "idle", playing: music.playing, paused: music.paused,
						sunrise: page.sunrise, skipNote: d.skipNoteNow(now)}
					d.r.draw(s)
					if d.r.musicWordShown() {
						t.Fatal("skip feedback published a hidden music target")
					}
					tapAt(d, at)
					if !d.away {
						t.Fatal("an invisible footer target reopened the music")
					}

					// Expiry can be observed while the panel still holds the feedback frame.
					expired := now.Add(skipNoteFor + time.Second)
					if d.skipNoteNow(expired).exists() {
						t.Fatal("the feedback did not expire")
					}
					tapAt(d, at)
					if !d.away {
						t.Fatal("expiry reactivated music before a replacement frame")
					}

					// Only the frame drawing the word restores its target.
					s.now, s.skipNote = expired, d.skipNoteNow(expired)
					d.r.draw(s)
					if !d.r.musicWordShown() {
						t.Fatal("the drawn music word published no target")
					}
					tapAt(d, at)
					if d.away {
						t.Fatal("the drawn word was not the way back to the music")
					}
				})
			}
		}
	}
}

// Rainbow is chosen and draws nothing between turns: the page keeps its button. While any of it is on
// the screen - the entry, the light, the card, the fade going out - the page leaves the button out, and
// the first frame after it is gone puts it back. A page kept under Rainbow is not one a card, the light
// before an alarm or a skip answer is over.
func TestRainbowOwnsTheLightPagesButtonOnlyWhileItDraws(t *testing.T) {
	now := time.Date(2026, time.October, 30, 15, 0, 0, 0, time.Local)
	config.Use(t.TempDir() + "/state.json")
	if err := config.Set().Screen().TurnStyle("rainbow"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set().Screen().TurnStyle("") })
	offer := skipOffer{token: alarm.SkipToken{ID: "wake", At: now.Add(15 * time.Minute)}, label: "Wake up"}
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	// Chosen, with no turn in hand: nothing of Rainbow is drawn, so the page keeps its button.
	r.draw(scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer})
	if r.skipAt.Empty() {
		t.Fatal("Rainbow chose not to draw, and the button went with it")
	}
	turn := scene{now: now.Add(100 * time.Millisecond), phase: "idle", sunrise: 0.4, skip: &offer,
		cue: "replying", reply: "Turned the light on.", since: now}
	r.draw(turn)
	if !r.skipDrawn.Empty() || !r.skipAt.Empty() {
		t.Fatalf("Rainbow drew over the page and the button with it: drawn %v published %v", r.skipDrawn, r.skipAt)
	}
	// The turn ends; the light goes out over the next frames, and the button comes back with the first
	// frame that has none of Rainbow on it.
	at := turn.now
	for i := 0; i < 40; i++ {
		at = at.Add(200 * time.Millisecond)
		r.draw(scene{now: at, phase: "idle", sunrise: 0.4, skip: &offer})
		if r.rainbow.Busy() {
			if !r.skipAt.Empty() {
				t.Fatalf("frame %d: Rainbow was still going out and the button was published", i)
			}
			continue
		}
		if r.skipAt.Empty() {
			t.Fatalf("frame %d: Rainbow was gone and the button was not back", i)
		}
		return
	}
	t.Fatal("Rainbow never finished going out")
}

// A page kept under Rainbow keeps the footer's word for the music with its pixels, and the frame that
// reuses it publishes the target again; a page a card or a skip answer is over is not kept at all.
func TestRainbowKeepsTheFooterWordWithThePage(t *testing.T) {
	now := time.Date(2026, time.October, 30, 15, 0, 0, 0, time.Local)
	config.Use(t.TempDir() + "/state.json")
	if err := config.Set().Screen().TurnStyle("rainbow"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = config.Set().Screen().TurnStyle("") })
	r := newRenderer(image.NewRGBA(image.Rect(0, 0, showWide, showHigh)))
	page := scene{now: now, phase: "idle", playing: true, cue: "listening", since: now}
	r.draw(page)
	if !r.rainbowPageOK || !r.musicWordShown() {
		t.Fatalf("the page under Rainbow was not kept with its word: kept %v word %v", r.rainbowPageOK, r.musicWordShown())
	}
	// The next frame within the same second asks for the kept page, as draw does before the page is
	// drawn: it comes back with the word for the music beside it.
	kept := page
	kept.now = now.Add(50 * time.Millisecond)
	r.rainbowBegin(kept)
	if !r.rainbowKept(kept) {
		t.Fatal("the page under Rainbow was not kept within the same second")
	}
	if !r.musicDrawn {
		t.Fatal("the kept page came back without the word for the music")
	}
	// A card over the page, or the answer to a press on the light's page, is not kept.
	card := page
	card.now = now.Add(2 * time.Second)
	card.showNotification = true
	r.draw(card)
	if r.rainbowPageOK {
		t.Fatal("a page with a card over it was kept")
	}
	answer := page
	answer.now = now.Add(3 * time.Second)
	answer.skipNote = skipAnswer{label: "Wake up", at: now.Add(7 * time.Hour), skipped: true}
	r.draw(answer)
	if r.rainbowPageOK {
		t.Fatal("a page with the skip answer on its foot was kept")
	}
}

// The card the frame was drawn under is not the page's to keep a press for, against the alarm feature
// itself and the config file a restart reads: a tap where the button would have been, with the card
// dismissed between frames, leaves nothing in memory and nothing on disk. The frame that draws the page
// again offers the very occurrence, and that press is saved once. It holds for a normal alarm and for a
// light-only one, whose light is all its morning, on every panel.
func TestACoveredLightPageLeavesNoMarkerInTheStore(t *testing.T) {
	for _, kind := range []struct {
		name  string
		alarm config.Alarm
	}{
		{"normal", config.Alarm{ID: "wake", Label: "Wake up", Sunrise: 30}},
		{"light-only", config.Alarm{ID: "wake", Label: "Morning light", Sunrise: 30, LightOnly: true}},
	} {
		for _, panel := range showPanels() {
			t.Run(kind.name+"/"+panel.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "state.json")
				config.Use(path)
				if err := config.Set().Screen().Welcomed(true); err != nil {
					t.Fatal(err)
				}
				heldLight(t, time.Time{}, false, false)
				t.Cleanup(func() { notify.Get().Dismiss() })

				now := time.Now()
				at := now.Add(15 * time.Minute).Truncate(time.Minute)
				al := kind.alarm
				al.Hour, al.Minute = at.Hour(), at.Minute()
				al.On, al.Days = true, config.DaysEvery
				if err := config.Set().Alarms().Put(al); err != nil {
					t.Fatal(err)
				}
				token, ok := alarm.Get().Skippable(now)
				if !ok {
					t.Fatalf("the engine offers no occurrence of %+v at %v", al, now)
				}
				if !token.At.Equal(at) {
					t.Fatalf("the engine offers the occurrence at %v, want the alarm's %v", token.At, at)
				}

				d := &Display{r: newRenderer(image.NewRGBA(image.Rect(0, 0, panel.wide, panel.high))), on: true,
					poke: make(chan struct{}, 1), view: voice.State{Phase: "idle"}}
				offer := skipOffer{token: token, label: al.Label}
				clear := scene{now: now, phase: "idle", sunrise: 0.4, skip: &offer}
				// Where the button is drawn on the page as it was seen first: the place a finger would
				// have gone, taken from the frame that drew it and not from the one under the card.
				d.r.draw(clear)
				b := d.r.skipAt
				if b.Empty() {
					t.Fatal("the light's page drew no button")
				}
				mid := b.Min.Add(image.Pt(b.Dx()/2, b.Dy()/2))
				if got, ok := d.r.skipTapped(mid); !ok || got.token != token {
					t.Fatalf("the frame drew %+v %v at %v, want %+v", got, ok, mid, token)
				}

				// A card over the page: this frame publishes no button at all.
				notify.Get().Note("Fictional card", "A touch dismisses this notification, not an alarm.")
				n, up := notify.Get().Card()
				if !up {
					t.Fatal("no card")
				}
				covered := clear
				covered.showNotification, covered.notification = true, n
				d.r.draw(covered)
				if !d.r.skipAt.Empty() {
					t.Fatalf("the covered frame published a button at %v", d.r.skipAt)
				}

				// The card goes between frames: the panel still has the frame it was drawn on.
				notify.Get().Dismiss()
				tapAt(d, mid)
				if got := config.Get().Alarms.Skipped; len(got) != 0 {
					t.Fatalf("a tap under a card left a marker in memory: %+v", got)
				}
				if note := d.skipNoteNow(now); note.exists() {
					t.Errorf("a tap with no button answered %q", skipWhole(note))
				}
				// Nor in the store: a restart reads the file, not this process.
				config.Use(path)
				if got := config.Get().Alarms.Skipped; len(got) != 0 {
					t.Fatalf("a tap under a card left a marker in the store: %+v", got)
				}

				// The frame that draws the page again offers the same occurrence, and the press it
				// takes is saved once, in memory and in the store.
				d.r.draw(clear)
				if got, ok := d.r.skipTapped(mid); !ok || got.token != token {
					t.Fatalf("the page did not offer %+v again at %v: %+v %v", token, mid, got, ok)
				}
				tapAt(d, mid)
				if got := config.Get().Alarms.Skipped; len(got) != 1 || got[0].ID != token.ID || !got[0].At.Equal(token.At) {
					t.Fatalf("the press that was drawn saved %+v, want one for %+v", got, token)
				}
				if note := d.skipNoteNow(now); !note.exists() || !note.skipped {
					t.Errorf("the saved press answered %+v", note)
				}
				config.Use(path)
				if got := config.Get().Alarms.Skipped; len(got) != 1 || got[0].ID != token.ID || !got[0].At.Equal(token.At) {
					t.Fatalf("the marker did not come back from the store: %+v", got)
				}
			})
		}
	}
}
