package video

import (
	"errors"
	"time"
)

// Origin is who asked for a video.
type Origin int

const (
	// FromHomeAssistant is the play_video action: over the ESPHome link, which is already
	// authenticated, so it never asks on the screen.
	FromHomeAssistant Origin = iota

	// FromDLNA is a DLNA controller, which anybody on the network can be: the first video from an
	// address asks on the screen first.
	FromDLNA
)

// Request is a video to play.
type Request struct {
	URL   string
	Title string

	Origin Origin
	// From is the address that asked, for a DLNA controller: what the screen names and remembers.
	From string
}

// Phase is what the player is doing, as Home Assistant's Video sensor says it.
type Phase string

const (
	Idle    Phase = "idle"
	Asking  Phase = "asking"  // a DLNA video waiting for an answer on the screen
	Loading Phase = "loading" // finding out what it is, and the first frames
	Playing Phase = "playing"
	Paused  Phase = "paused"
)

// State is the player now.
type State struct {
	Phase Phase
	// ID is the video's: each request gets a new one, so a controller can tell its own from the next.
	ID     uint64
	Title  string
	Host   string
	Origin Origin
	From   string

	// Pos is how far into the video it is, Dur how long it runs (0 when it does not say).
	Pos, Dur time.Duration

	// Frames is whether the picture has begun to arrive.
	Frames bool

	// AskID is the DLNA video whose question is on the screen, when there is one: while another video
	// plays, the state is that video's, and this is how the one asking knows it is still asked about.
	AskID uint64

	// Shown and Dropped count frames.
	Shown, Dropped int

	// Ended is the ID of the last video to play to its end by itself; Failed the ID of the last that
	// failed, and Err why, ErrAt when.
	Ended  uint64
	Failed uint64
	Err    string
	ErrAt  time.Time
}

// Active is whether a video is up, from asking through to paused.
func (s State) Active() bool { return s.Phase != Idle && s.Phase != "" }

var (
	// ErrOff is a video asked for with the Video switch off (or DLNA video, for a DLNA one).
	ErrOff = errors.New("video: videos are off on this device")
	// ErrNotHere is a device with no video player: the Dot.
	ErrNotHere = errors.New("video: this device has no video player")
	// ErrDeclined is an address the screen just said Not now to.
	ErrDeclined = errors.New("video: not now")
	// ErrBusy is a DLNA video from one address while the screen is asking about another's: the question
	// on the screen is not changed under the finger answering it.
	ErrBusy = errors.New("video: the screen is asking about another video")
	// ErrNotInstalled is an image without the decoder.
	ErrNotInstalled = errors.New("video: this image has no video decoder")
)

// DLNAProtocols are what the DLNA renderer adds to its sink protocols while DLNA video is on.
const DLNAProtocols = "http-get:*:video/mp4:*,http-get:*:video/x-matroska:*,http-get:*:video/mp2t:*," +
	"http-get:*:video/quicktime:*,http-get:*:application/vnd.apple.mpegurl:*,http-get:*:application/x-mpegURL:*"
