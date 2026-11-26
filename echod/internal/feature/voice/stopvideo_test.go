package voice

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
)

// playingVideo stands in for the video player for the rest of the test: a video is going while
// running is, and the count is how often it was stopped.
func playingVideo(t *testing.T, running bool) (*bool, *int) {
	t.Helper()
	wasRunning, wasStop := videoRunning, stopVideo
	t.Cleanup(func() { videoRunning, stopVideo = wasRunning, wasStop })
	var stopped int
	videoRunning = func() bool { return running }
	stopVideo = func() { running = false; stopped++ }
	return &running, &stopped
}

func TestWhatStopsAVideo(t *testing.T) {
	for _, s := range []string{"Stop.", " it. Stop playing.", "stop the video", "Stop this video, please.", "stop the stream", "Stopp das Video", "arrête la vidéo", "detén el vídeo", "выключи видео"} {
		if !stopsVideo(s) {
			t.Errorf("%q should stop a video", s)
		}
	}
	for _, s := range []string{"", "pause", "pause the video", "stop the music", "play the next video", "turn off the lights", "the video"} {
		if stopsVideo(s) {
			t.Errorf("%q should not stop a video", s)
		}
	}
	// The words for a video count for a video only: over a ring, "stop playing" is not about it.
	for _, s := range []string{"stop playing", "stop the video"} {
		if stopsRing(s) {
			t.Errorf("%q should not stop a ring", s)
		}
	}
}

// The stop word while a video plays ends it, as the screen's Stop does, where music is only paused.
// After the wake word, the turn's screen has paused the video under it, so nothing sounds, and the
// stop is still the video's: the turn is canceled and the video ended, whether the turn is listening
// or has gone on to thinking. A ring over the video is stopped first, and the video left.
func TestTheStopWordStopsAVideo(t *testing.T) {
	t.Run("no turn, the video playing", func(t *testing.T) {
		// The video's sound is a track of the media player's, so the music finds it too.
		v, _, paused := stopRoom(t, true)
		_, stopped := playingVideo(t, true)
		v.Interrupt()
		if *stopped != 1 || *paused != 0 {
			t.Errorf("stopped the video %d times and paused the music %d, want 1 and 0", *stopped, *paused)
		}
	})
	t.Run("Stop, the video playing", func(t *testing.T) {
		v, _, paused := stopRoom(t, true)
		_, stopped := playingVideo(t, true)
		if !v.Stop() {
			t.Error("Stop found nothing to stop")
		}
		if *stopped != 1 || *paused != 0 {
			t.Errorf("stopped the video %d times and paused the music %d, want 1 and 0", *stopped, *paused)
		}
	})
	for _, think := range []bool{false, true} {
		name := "after the wake word, listening"
		if think {
			name = "after the wake word, thinking"
		}
		t.Run(name, func(t *testing.T) {
			_, released, paused := stopRoom(t, false)
			_, stopped := playingVideo(t, true)
			w := newWatcher(t, config.OnHA{})
			w.handle(event{kind: evStart, slot: 0, woke: true, at: w.at})
			if think {
				w.think()
				if w.Phase() != phaseThinking {
					t.Fatalf("the turn is %s, want thinking", w.Phase())
				}
			}
			v := &Voice{turn: w.conversation}
			v.Interrupt()
			if *stopped != 1 || *paused != 0 || *released != 0 {
				t.Errorf("stopped the video %d times, paused the music %d, let go of the light %d, want 1, 0, 0", *stopped, *paused, *released)
			}
			select {
			case e := <-w.events:
				if e.kind != evCancel {
					t.Errorf("the turn was sent %v, want a cancel", e.kind)
				}
			default:
				t.Error("the turn was not canceled")
			}
		})
	}
	t.Run("a ring over the video", func(t *testing.T) {
		v, _, _ := stopRoom(t, false)
		_, stopped := playingVideo(t, true)
		sound(t)
		v.Interrupt()
		if ringing.Load() {
			t.Error("the ring was not stopped")
		}
		if *stopped != 0 {
			t.Error("the video was stopped with the ring")
		}
	})
	t.Run("a video paused by somebody", func(t *testing.T) {
		// Nothing is going: the stop word has nothing to do but let go of the held light, as before.
		v, released, _ := stopRoom(t, false)
		_, stopped := playingVideo(t, false)
		v.Interrupt()
		if *stopped != 0 || *released != 1 {
			t.Errorf("stopped the video %d times and let go of the light %d, want 0 and 1", *stopped, *released)
		}
	})
}

// A wake word said over a playing video opens a turn whose screen pauses the video, and a stop in it
// ("Stop playing.") is answered by the video ending here, not by Home Assistant's "OK, stopped.",
// which stopped nothing on the device and left the video going on when the turn was over. A sentence
// that asks for more, a pause, a stop with no video, and a stop over a ring are not.
func TestAStopInATurnOpenedOverAVideoStopsIt(t *testing.T) {
	for _, c := range []struct {
		name     string
		video    bool // a video plays when the turn opens
		ringing  bool
		heard    string
		answered bool
	}{
		{"stop playing", true, false, " it. Stop playing.", true},
		{"stop the video", true, false, "Stop the video.", true},
		{"stop alone", true, false, " stop. Stop.", true},
		{"in another language", true, false, "Arrête la vidéo.", true},
		{"a pause", true, false, "Pause.", false},
		{"a sentence that asks for more", true, false, "stop the video and turn on the lights", false},
		{"no video", false, false, "Stop playing.", false},
		{"a ring over the video", true, true, " Stop", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			running, stopped := playingVideo(t, c.video)
			if c.ringing {
				ringing.Store(true)
				done := ring.Sounding()
				ring.Silence()
				t.Cleanup(func() { ringing.Store(false); done() })
			}
			w := newWatcher(t, config.OnHA{})
			w.handle(event{kind: evStart, slot: 0, woke: true, at: w.at})
			if w.phase != phaseListening {
				t.Fatalf("the turn did not open: phase %s", w.phase)
			}
			w.handle(event{kind: evHeard, text: c.heard})

			if c.ringing {
				// The ring's stop answers it, and the video is left for after the ring.
				if w.phase != phaseIdle {
					t.Fatalf("after %q over a ring the turn is %s, want ended here", c.heard, w.phase)
				}
				if *stopped != 0 || !*running {
					t.Error("a stop over a ring stopped the video under it")
				}
				return
			}
			if !c.answered {
				if w.phase == phaseIdle {
					t.Fatalf("%q was not left to Home Assistant", c.heard)
				}
				if *stopped != 0 {
					t.Errorf("%q stopped the video", c.heard)
				}
				return
			}
			if w.phase != phaseIdle {
				t.Fatalf("after %q the turn is %s, want ended here", c.heard, w.phase)
			}
			if *stopped != 1 {
				t.Errorf("the video was stopped %d times, want once", *stopped)
			}
			if !w.draining {
				t.Error("the rest of Home Assistant's run would be taken up")
			}
			w.handle(event{kind: evReplyText, text: "OK, stopped."})
			if w.shown.Reply != "" {
				t.Errorf("the ended turn took up the reply %q", w.shown.Reply)
			}
		})
	}
}
