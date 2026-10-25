package video

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Info is what ffmpeg said about a stream before decoding it (probe): the picture's size and shape,
// its frame rate and turn, which streams to decode, and how long it runs.
type Info struct {
	// Width and Height are the picture as coded; SARNum:SARDen the shape of its pixels (1:1 unless the
	// file says otherwise).
	Width, Height  int
	SARNum, SARDen int

	// FPS is the frame rate, 0 when ffmpeg named none.
	FPS float64

	// Rotation is the turn the file asks for, in degrees, as ffmpeg prints it (-90 is a phone held
	// upright). ffmpeg applies it itself; a quarter turn swaps the picture's width and height.
	Rotation int

	// VideoIndex and AudioIndex are the streams to decode, counted among the streams of their own
	// kind (ffmpeg's 0:v:N and 0:a:N); AudioIndex is -1 when there is no sound to play.
	VideoIndex, AudioIndex int

	// VideoCodec and AudioCodec are their codecs, for the log.
	VideoCodec, AudioCodec string

	// PixFmt is the picture's pixel format as ffmpeg names it (yuv420p, yuv420p10le, ...), "" when it
	// named none; Interlaced, that its frames are two fields.
	PixFmt     string
	Interlaced bool

	// Duration is how long it runs, 0 for a live stream or one that does not say.
	Duration time.Duration
}

// Usable codecs: what the image's ffmpeg decodes (tools/linux/build-ffmpeg.sh). H.265, VP8 and VP9 only
// where they were built in (the static aarch64 ffmpeg athena's and the Skylight's images carry,
// build-ffmpeg-arm64.sh, run on their 64-bit kernels), which decodesHEVC and decodesVPx say.
var (
	videoCodecs = map[string]bool{"h264": true, "mpeg4": true}
	audioCodecs = map[string]bool{"aac": true, "aac_latm": true, "mp3": true, "mp3float": true, "opus": true, "ac3": true, "eac3": true}
)

// decodesHEVC is whether the image's ffmpeg decodes H.265, on the cores (proc.go looks).
var decodesHEVC = func() bool { return false }

// decodesVPx is whether the image's ffmpeg decodes VP8 and VP9 (WebM's video), on the cores (proc.go
// looks).
var decodesVPx = func() bool { return false }

// coresMost is the most of a codec the cores decode in time, in pixels a second (width by height by
// frame rate), absent for no limit; a stream past it is not played, rather than played behind. H.265,
// VP8 and VP9 are held to 1080p30 wherever they are built in, unless the board says otherwise
// (cores_show.go).
var coresMost = map[string]float64{"hevc": p1080 * 30, "vp8": p1080 * 30, "vp9": p1080 * 30}

// p1080 and p720 are a 1080p and a 720p picture's pixels, 1080p as the decoders round it (16 lines).
const (
	p1080 = 1920 * 1088
	p720  = 1280 * 720
)

// tooFast is whether a picture is more than the cores decode in time here. A stream that does not say
// its rate is not refused, nor one whose rate is past fpsMost: that is a time base (a browser's WebM
// says "1k tbr"), not frames.
func tooFast(codec string, w, h int, fps float64) bool {
	most := coresMost[codec]
	return most > 0 && fps > 0 && fps <= fpsMost && float64(w)*float64(h)*fps > most
}

// fpsMost is the highest frame rate believed from a probe.
const fpsMost = 240

func usableVideo(codec string) bool {
	switch codec {
	case "hevc":
		return decodesHEVC()
	case "vp8", "vp9":
		return decodesVPx()
	}
	return videoCodecs[codec]
}

// reach says what coresMost allows of a codec, for a refusal: as a 1080p rate, or a 720p one where that
// is under 25, which is how streams are offered.
func reach(codec string) string {
	name := map[string]string{"hevc": "H.265", "vp8": "VP8", "vp9": "VP9"}[codec]
	if name == "" {
		name = codec
	}
	most, size, px := coresMost[codec], "1080p", float64(p1080) // the size coresMost counts in
	if math.Round(most/px) < 25 {
		size, px = "720p", p720
	}
	return name + " up to " + size + " at " + strconv.FormatFloat(math.Round(most/px), 'f', -1, 64) + " frames a second"
}

// decodable names the video codecs this device plays, for a refusal.
func decodable() string {
	c := []string{"H.264"}
	if decodesHEVC() {
		c = append(c, "H.265")
	}
	if decodesVPx() {
		c = append(c, "VP8", "VP9")
	}
	return strings.Join(c, ", ") + " and MPEG-4 only"
}

var (
	reStream = regexp.MustCompile(`^\s*Stream #\d+:\d+(?:\[[^\]]*\])?(?:\([^)]*\))?: (Video|Audio|Subtitle|Data|Attachment): ([A-Za-z0-9_]+)(.*)$`)
	reSize   = regexp.MustCompile(`, (\d{1,5})x(\d{1,5})\b`)
	// The pixel format, after the codec's own parenthesis, with what ffmpeg says of the picture in its
	// own: "yuv420p(tv, bt709, progressive)", "yuv420p(top first)".
	rePixFmt     = regexp.MustCompile(`, ((?:yuvj?|nv|gray|rgb|bgr|p0)[0-9a-z]*)(?:\(([^)]*)\))?`)
	reInterlaced = regexp.MustCompile(`\b(top|bottom) first\b|\b[tb][tb]\b`)
	reSAR        = regexp.MustCompile(`\[SAR (\d+):(\d+) DAR`)
	reFPS        = regexp.MustCompile(`, ([0-9.]+) fps\b`)
	reTBR        = regexp.MustCompile(`, ([0-9.]+)(k?) tbr\b`)
	reRotation   = regexp.MustCompile(`rotation of (-?[0-9.]+) degrees`)
	reDuration   = regexp.MustCompile(`Duration: (\d+):(\d\d):(\d\d(?:\.\d+)?)`)
	reProgram    = regexp.MustCompile(`^\s*Program (\d+)`)
)

// most is the largest picture worth choosing among a stream's variants: past it, a variant only costs
// the Show more without looking better on its screen.
const most = 720

// ParseProbe reads what `ffmpeg -i <address>` printed about the input: the best picture to decode
// (the largest at most 720 rows, or else the smallest), the sound beside it, and the rest of Info.
func ParseProbe(out string) (Info, error) {
	info := Info{VideoIndex: -1, AudioIndex: -1, SARNum: 1, SARDen: 1}
	type videoStream struct {
		idx, n        int // its place among video streams, and among all streams
		program       int // the variant it belongs to in a stream that has them, else -1
		w, h          int
		sarN, sarD    int
		fps           float64
		rotation      int
		codec         string
		usable        bool
		attachedImage bool
		pixFmt        string
		interlaced    bool
		tooFast       bool
	}
	type audioStream struct {
		idx, n, program int
		codec           string
	}
	var vids []videoStream
	var auds []audioStream
	nv, na, n := 0, 0, 0
	lastVideo, program := -1, -1
	var problem string
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if m := reDuration.FindStringSubmatch(line); m != nil && info.Duration == 0 {
			h, _ := strconv.Atoi(m[1])
			mi, _ := strconv.Atoi(m[2])
			s, _ := strconv.ParseFloat(m[3], 64)
			info.Duration = time.Duration(h)*time.Hour + time.Duration(mi)*time.Minute + time.Duration(s*float64(time.Second))
			continue
		}
		if m := reProgram.FindStringSubmatch(line); m != nil {
			program, _ = strconv.Atoi(m[1])
			lastVideo = -1
			continue
		}
		if m := reRotation.FindStringSubmatch(line); m != nil && lastVideo >= 0 {
			f, _ := strconv.ParseFloat(m[1], 64)
			vids[lastVideo].rotation = int(f)
			continue
		}
		m := reStream.FindStringSubmatch(line)
		if m == nil {
			// A line of ffmpeg's own saying what went wrong; not one of its components' chatter ("[hls @ …]
			// Opening …"), which names other addresses.
			if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "At least one output file") &&
				!strings.HasPrefix(line, " ") && !strings.HasPrefix(t, "Input #") && !strings.HasPrefix(t, "[") {
				problem = t
			}
			continue
		}
		kind, codec, rest := m[1], m[2], m[3]
		switch kind {
		case "Video":
			v := videoStream{idx: nv, n: n, program: program, sarN: 1, sarD: 1, codec: codec, usable: usableVideo(codec),
				attachedImage: strings.Contains(rest, "(attached pic)")}
			if s := rePixFmt.FindStringSubmatch(rest); s != nil {
				v.pixFmt = s[1]
				v.interlaced = reInterlaced.MatchString(s[2])
			}
			if s := reSize.FindStringSubmatch(rest); s != nil {
				v.w, _ = strconv.Atoi(s[1])
				v.h, _ = strconv.Atoi(s[2])
			}
			if s := reSAR.FindStringSubmatch(rest); s != nil {
				v.sarN, _ = strconv.Atoi(s[1])
				v.sarD, _ = strconv.Atoi(s[2])
			}
			if s := reFPS.FindStringSubmatch(rest); s != nil {
				v.fps, _ = strconv.ParseFloat(s[1], 64)
			} else if s := reTBR.FindStringSubmatch(rest); s != nil {
				v.fps, _ = strconv.ParseFloat(s[1], 64)
				if s[2] == "k" {
					v.fps *= 1000
				}
			}
			if v.usable && tooFast(codec, v.w, v.h, v.fps) {
				v.usable, v.tooFast = false, true
			}
			vids = append(vids, v)
			lastVideo = len(vids) - 1
			nv++
		case "Audio":
			auds = append(auds, audioStream{idx: na, n: n, program: program, codec: codec})
			na++
			lastVideo = -1
		default:
			lastVideo = -1
		}
		n++
	}

	best := -1
	for i, v := range vids {
		if !v.usable || v.attachedImage || v.w <= 0 || v.h <= 0 {
			continue
		}
		if best < 0 {
			best = i
			continue
		}
		b := vids[best]
		switch {
		case v.h <= most && (b.h > most || v.h > b.h):
			best = i // a bigger one within reach, or the first within reach
		case v.h > most && b.h > most && v.h < b.h:
			best = i // all too big: the smallest
		}
	}
	if best < 0 {
		for _, v := range vids {
			if v.tooFast {
				return info, fmt.Errorf("its video is %s at %dx%d and %s frames a second, more than this device decodes in time (%s)",
					v.codec, v.w, v.h, strconv.FormatFloat(v.fps, 'f', -1, 64), reach(v.codec))
			}
		}
		if len(vids) > 0 {
			return info, errors.New("its video is " + vids[0].codec + ", which this device does not decode (" + decodable() + ")")
		}
		if problem != "" {
			return info, errors.New(problem)
		}
		return info, errors.New("there is no video in it")
	}
	v := vids[best]
	info.VideoIndex, info.VideoCodec = v.idx, v.codec
	info.Width, info.Height, info.FPS, info.Rotation = v.w, v.h, v.fps, v.rotation
	info.PixFmt, info.Interlaced = v.pixFmt, v.interlaced
	if v.sarN > 0 && v.sarD > 0 {
		info.SARNum, info.SARDen = v.sarN, v.sarD
	}
	// The sound: the first usable stream of the picture's own variant, when the stream has variants
	// (a second variant would be a second download), or else the first usable at all.
	for _, a := range auds {
		if audioCodecs[a.codec] && v.program >= 0 && a.program == v.program {
			info.AudioIndex, info.AudioCodec = a.idx, a.codec
			break
		}
	}
	if info.AudioIndex < 0 {
		for _, a := range auds {
			if audioCodecs[a.codec] {
				info.AudioIndex, info.AudioCodec = a.idx, a.codec
				break
			}
		}
	}
	if info.AudioIndex < 0 && len(auds) > 0 {
		info.AudioCodec = auds[0].codec // there, but not one this device decodes: the video plays silent
	}
	return info, nil
}
