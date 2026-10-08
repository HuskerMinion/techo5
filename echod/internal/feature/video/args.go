package video

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Screen is what the picture is made for: the canvas the daemon draws on (960×480 on the Show 5,
// 1280×800 on the Show 8), whether the panel is turned a quarter turn from it, and the panel's pixel
// layout in ffmpeg's words (hardware/screen).
type Screen struct {
	W, H    int
	Rotated bool
	PixFmt  string
}

// Fit is where the picture goes on the canvas: W×H at X, Y, the rest black. All even, which the
// decoder's 4:2:0 pictures need.
type Fit struct{ W, H, X, Y int }

// Display is the picture's shape as it is meant to be seen: its pixels' own shape applied, and a
// quarter turn swapping its sides.
func (i Info) Display() (w, h float64) {
	w, h = float64(i.Width), float64(i.Height)
	if i.SARNum > 0 && i.SARDen > 0 {
		w = w * float64(i.SARNum) / float64(i.SARDen)
	}
	if r := ((i.Rotation % 360) + 360) % 360; r == 90 || r == 270 {
		w, h = h, w
	}
	return w, h
}

// FitIn is the largest the picture goes on the screen with its shape kept, in the middle, the rest
// left black: bars above and below a wide film, at the sides of a tall one. On the Spot's round panel
// too: fitted inside the circle a 16:9 picture is barely 418 wide and looked small in the previews, so
// it fills the square and the bezel takes its corners.
func FitIn(i Info, s Screen) Fit {
	dw, dh := i.Display()
	if dw <= 0 || dh <= 0 || s.W <= 0 || s.H <= 0 {
		return Fit{W: even(s.W), H: even(s.H)}
	}
	scale := math.Min(float64(s.W)/dw, float64(s.H)/dh)
	w := min(even(int(math.Round(dw*scale))), even(s.W))
	h := min(even(int(math.Round(dh*scale))), even(s.H))
	w, h = max(w, 2), max(h, 2)
	return Fit{W: w, H: h, X: even((s.W - w) / 2), Y: even((s.H - h) / 2)}
}

func even(v int) int { return v &^ 1 }

// Rate is a frame rate as a fraction, which is how a frame's time is worked out without drift:
// frame n is at n×Den/Num seconds.
type Rate struct{ Num, Den int }

func (r Rate) String() string { return strconv.Itoa(r.Num) + "/" + strconv.Itoa(r.Den) }

// FrameRate is the rate the picture is shown at: its own, or half of it past 30 frames a second (a
// 50 or 60 frame stream costs the Show twice the scaling for nothing it can show), and 30 when the
// stream says nothing. The broadcast rates (29.97 and the rest) are kept exact.
func FrameRate(fps float64) Rate {
	if fps <= 1 || fps > 240 || math.IsNaN(fps) {
		return Rate{30, 1}
	}
	for fps > 30.5 {
		fps /= 2
	}
	for _, r := range []Rate{{24000, 1001}, {30000, 1001}, {60000, 1001}} {
		if math.Abs(fps-float64(r.Num)/float64(r.Den)) < 0.01 {
			return r
		}
	}
	if math.Abs(fps-math.Round(fps)) < 0.01 {
		return Rate{int(math.Round(fps)), 1}
	}
	return Rate{int(math.Round(fps * 1000)), 1000}
}

// threads is how many cores the decoder may use for a picture this tall: two hold 480p and 540p at
// 30 frames a second with the daemon running, 720p wants three, 1080p everything (docs/cast-plan.md,
// step 0, and the device test of step 1).
func threads(height int) int {
	switch {
	case height <= 540:
		return 2
	case height <= 720:
		return 3
	}
	return 4
}

// maxPixels bounds a picture the decoder will take: 1920×1088, the largest 1080p frame. A stream that
// claims more (4K, or a header made up to make it allocate) is refused before a frame is decoded. It
// bounds what ffmpeg makes too, and the panel's frames (at most the Show 8's 1280×800) are well under.
const maxPixels = "2088960"

// protocols is all the decoder may open: the network, never a file (it has none built in either).
const protocols = "http,https,tcp,tls,hls,crypto"

// Decoding is how the decoder is run: what to fetch and from where, and how.
type Decoding struct {
	URL    string
	Info   Info
	Screen Screen

	// CAFile is where the certificate authorities are, empty for OpenSSL's own default; Insecure
	// stops certificates being checked (the device's own Insecure TLS setting).
	CAFile   string
	Insecure bool

	// Proxy is the daemon's guard proxy, for a kernel that cannot fence the decoder (guard.go); empty
	// when it can.
	Proxy string
}

// netArgs are the input options every run of the decoder has: the network only, and patience for it.
// The certificate options go only with an https address: ffmpeg refuses an option nothing it opened
// took, and an http address opens no TLS. (A playlist fetched over http whose parts are https is
// fetched as ffmpeg does by default, unchecked.)
func netArgs(url, caFile string, insecure bool, proxy string) []string {
	allowed := protocols
	if proxy != "" {
		allowed += ",httpproxy" // https through the proxy is a CONNECT, which ffmpeg calls a protocol
	}
	a := []string{
		"-protocol_whitelist", allowed,
		// A connection that stops sending for this long ends the video, rather than leaving it frozen.
		"-rw_timeout", "15000000",
		// A connection that drops partway is made again; one refused at the start is not tried again
		// and again (ffmpeg's reconnect_on_network_error), so a wrong address says so at once.
		"-reconnect", "1", "-reconnect_streamed", "1", "-reconnect_delay_max", "4",
	}
	if proxy != "" {
		a = append(a, "-http_proxy", proxy)
	}
	switch {
	case !strings.HasPrefix(url, "https://"):
	case insecure:
		a = append(a, "-tls_verify", "0")
	default:
		a = append(a, "-tls_verify", "1")
		if caFile != "" {
			a = append(a, "-ca_file", caFile)
		}
	}
	return a
}

// ProbeArgs asks ffmpeg what is at the address and nothing more: with no output it reads the start of
// the stream, prints what it found and stops.
func ProbeArgs(url, caFile string, insecure bool, proxy string) []string {
	a := []string{"-hide_banner", "-nostdin", "-max_pixels", maxPixels}
	a = append(a, netArgs(url, caFile, insecure, proxy)...)
	return append(a, "-i", url)
}

// Filter is the picture's way from the decoder to the panel: an even frame rate first, so frame n is
// at n frames' time and late frames can be told; scaled to its place in the cheap 4:2:0 form, the bars
// added, turned for the panel, and only then made the panel's pixels, which is where the cost is
// (docs/cast-plan.md, step 0).
func Filter(i Info, s Screen) string {
	fit := FitIn(i, s)
	r := FrameRate(i.FPS)
	f := "fps=fps=" + r.String() + ":start_time=0"
	f += fmt.Sprintf(",scale=%d:%d:flags=fast_bilinear,format=yuv420p", fit.W, fit.H)
	if fit.W != s.W || fit.H != s.H {
		f += fmt.Sprintf(",pad=%d:%d:%d:%d:black", even(s.W), even(s.H), fit.X, fit.Y)
	}
	if s.Rotated {
		f += ",transpose=1" // clockwise: canvas (x, y) lands at panel (panel width-1-y, x)
	}
	pix := s.PixFmt
	if pix == "" {
		pix = "bgra"
	}
	return f + ",scale,format=" + pix
}

// Sound is what the sound is made into: what the speaker plays, padded from the stream's own start
// so its first sample is at the picture's time zero, then brought up to about where music is.
//
// Films and home videos are mixed far quieter than music: Big Buck Bunny is -27 LUFS over its length
// and -38 over its opening minute, where music is about -14, so at the same volume step a video was
// all but silent (measured on a Show 5 2nd gen, the samples going to the speaker: the video path
// adds no loss of its own; the same noise as a video and as a Home Assistant track came out 0.2 dB
// apart). A compressor brings it up with no look-ahead, which matters here: the sound is the clock,
// and a filter that holds seconds of it back (dynaudnorm does, by its window) leaves the picture
// waiting for sound that has not come out yet. Above -38 dBFS RMS it compresses 5:1 with a soft knee,
// then 12x (+21.6 dB) of makeup, so a quiet film comes up most and music hardly at all (measured:
// -37.9 LUFS comes out at -20.4, -27 at -17, -17.7 at -14.9); a limiter keeps the peaks under 0.89
// (-1 dBFS), so nothing clips. Attack 20 ms and release 800 ms keep a scene from pumping.
const (
	soundRate     = 48000
	soundChannels = 2
	soundFilter   = "aresample=48000:async=1:first_pts=0,acompressor=threshold=0.0125:ratio=5:attack=20:release=800:knee=4:makeup=12:detection=rms,alimiter=limit=0.89:attack=5:release=100:level=disabled"
)

// DecodeArgs runs the decoder: the picture as raw frames on file descriptor 3, the sound as 16-bit
// stereo on its standard output.
func DecodeArgs(d Decoding) []string {
	a := []string{"-hide_banner", "-nostdin", "-loglevel", "error", "-max_pixels", maxPixels}
	a = append(a, netArgs(d.URL, d.CAFile, d.Insecure, d.Proxy)...)
	_, h := d.Info.Display()
	t := threads(int(h))
	// Past 720p every core, and best effort even so: the decoder's own shortcuts (skipping the
	// deblocking) make ffmpeg 8 refuse to open the picture's output when given on its command line.
	a = append(a, "-threads", strconv.Itoa(t))
	a = append(a, "-i", d.URL)
	a = append(a, "-map", "0:v:"+strconv.Itoa(d.Info.VideoIndex), "-an", "-sn", "-dn",
		"-vf", Filter(d.Info, d.Screen), "-fps_mode", "passthrough",
		"-f", "rawvideo", "-pix_fmt", pixOr(d.Screen.PixFmt), "pipe:3")
	if d.Info.AudioIndex >= 0 {
		a = append(a, "-map", "0:a:"+strconv.Itoa(d.Info.AudioIndex), "-vn", "-sn", "-dn",
			"-af", soundFilter, "-ac", strconv.Itoa(soundChannels), "-ar", strconv.Itoa(soundRate),
			"-f", "s16le", "pipe:1")
	}
	return a
}

func pixOr(p string) string {
	if p == "" {
		return "bgra"
	}
	return p
}

// FrameBytes is one frame's size on the pipe, for a screen whose panel is panelW×panelH.
func FrameBytes(panelW, panelH int) int { return panelW * panelH * 4 }
