package video

import (
	"bufio"
	"errors"
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

	// Duration is how long it runs, 0 for a live stream or one that does not say.
	Duration time.Duration
}

// Usable codecs: what the image's ffmpeg decodes (tools/linux/build-ffmpeg.sh).
var (
	videoCodecs = map[string]bool{"h264": true, "mpeg4": true}
	audioCodecs = map[string]bool{"aac": true, "aac_latm": true, "mp3": true, "mp3float": true, "opus": true, "ac3": true, "eac3": true}
)

var (
	reStream   = regexp.MustCompile(`^\s*Stream #\d+:\d+(?:\[[^\]]*\])?(?:\([^)]*\))?: (Video|Audio|Subtitle|Data|Attachment): ([A-Za-z0-9_]+)(.*)$`)
	reSize     = regexp.MustCompile(`, (\d{1,5})x(\d{1,5})\b`)
	reSAR      = regexp.MustCompile(`\[SAR (\d+):(\d+) DAR`)
	reFPS      = regexp.MustCompile(`, ([0-9.]+) fps\b`)
	reTBR      = regexp.MustCompile(`, ([0-9.]+)(k?) tbr\b`)
	reRotation = regexp.MustCompile(`rotation of (-?[0-9.]+) degrees`)
	reDuration = regexp.MustCompile(`Duration: (\d+):(\d\d):(\d\d(?:\.\d+)?)`)
	reProgram  = regexp.MustCompile(`^\s*Program (\d+)`)
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
			v := videoStream{idx: nv, n: n, program: program, sarN: 1, sarD: 1, codec: codec, usable: videoCodecs[codec],
				attachedImage: strings.Contains(rest, "(attached pic)")}
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
		if len(vids) > 0 {
			return info, errors.New("its video is " + vids[0].codec + ", which this device does not decode (H.264 and MPEG-4 only)")
		}
		if problem != "" {
			return info, errors.New(problem)
		}
		return info, errors.New("there is no video in it")
	}
	v := vids[best]
	info.VideoIndex, info.VideoCodec = v.idx, v.codec
	info.Width, info.Height, info.FPS, info.Rotation = v.w, v.h, v.fps, v.rotation
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
