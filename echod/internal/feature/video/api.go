package video

import (
	"errors"
	"net"
	"time"
)

// Origin is who asked for a video.
type Origin int

const (
	// FromHomeAssistant is the play_video action: over the ESPHome link, which is already
	// authenticated, so it never asks on the screen.
	FromHomeAssistant Origin = iota

	// FromDLNA is a DLNA controller, which anybody on the network can be: the first video from an
	// address asks on the screen first, unless the controller is on the device itself (FromDevice).
	FromDLNA
)

// FromDevice is whether a DLNA controller's address is the device's own loopback (127.0.0.0/8, ::1):
// a program on the device driving the renderer on localhost, which plays without asking, as
// play_video's videos do.
//
// Loopback alone used to be enough, as putting a program there took root. With add-ons running as users
// of their own (feature/addons) it is not: the renderer gives a loopback address as From only when the
// program at the other end runs as root, or as an add-on the owner installed and agreed to, whose
// warning says it can drive the device (feature/dlna, controller; lib/peeruid). Any other user on the
// device comes as "a program on this device (user N)", which is not an address, and is asked about.
//
// From is the connection's own source address (the renderer's RemoteAddr), never a header, so a
// request cannot claim it. Nobody on the network can be it either: the kernel drops a packet that
// arrives on Wi-Fi from a loopback source, and the answer to one would go out on loopback, so no
// connection gets made. A connection the device makes to its own Wi-Fi address comes from that
// address, not loopback: an add-on calls 127.0.0.1 (or [::1]) to be taken as the device's.
func FromDevice(from string) bool {
	ip := net.ParseIP(from)
	return ip != nil && ip.IsLoopback()
}

// Request is a video to play.
type Request struct {
	URL   string
	Title string

	Origin Origin
	// From is the address that asked, for a DLNA controller: what the screen names and remembers.
	From string

	// Start is where in the video to begin: zero for its start, else where a seek moved it to (Seek).
	// A video that says no length (a live stream) starts where it is, whatever this says.
	Start time.Duration
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

	// Aspect is how the picture takes the screen (Refit): AspectFit for every video as it starts.
	Aspect Aspect

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
	// ErrNotSeekable is a seek in a video that cannot be moved through: a live stream, which says no
	// length, or a video that is no longer the one up.
	ErrNotSeekable = errors.New("video: this video cannot be moved through")
	// ErrNotInstalled is an image without the decoder.
	ErrNotInstalled = errors.New("video: this image has no video decoder")
)

// DLNAProtocols are what the DLNA renderer adds to its sink protocols while DLNA video is on.
const DLNAProtocols = "http-get:*:video/mp4:*,http-get:*:video/x-matroska:*,http-get:*:video/mp2t:*," +
	"http-get:*:video/quicktime:*,http-get:*:application/vnd.apple.mpegurl:*,http-get:*:application/x-mpegURL:*"
