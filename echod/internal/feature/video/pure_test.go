package video

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// Only http and https addresses naming another host are fetched: never a file, data, a local path, an
// ffmpeg protocol prefix or the device itself.
func TestOnlyNetworkAddressesArePlayed(t *testing.T) {
	good := []string{
		"http://192.168.1.20:8096/Videos/1/stream.mp4",
		"https://media.example.com/clip.mp4?api_key=abc",
		"HTTPS://media.example.com/live/index.m3u8",
		"http://user:pass@192.168.1.5/film.mkv",
		"http://[2001:db8::1]/film.mp4",
		"http://cafe.be/film.mp4",
		"http://nas2/film.mp4",
	}
	for _, u := range good {
		if _, err := CheckURL(u); err != nil {
			t.Errorf("%s: refused: %v", u, err)
		}
	}
	bad := []string{
		"",
		"file:///etc/passwd",
		"FILE:///data/misc/techo5/state.json",
		"/etc/passwd",
		"etc/passwd",
		"data:video/mp4;base64,AAAA",
		"rtsp://192.168.1.40/stream",
		"pipe:0",
		"concat:http://a/1.ts|file:/etc/passwd",
		"subfile,,start,0,end,0,,:/etc/passwd",
		"crypto+file:///etc/passwd",
		"hls+file:///etc/passwd",
		"tcp://192.168.1.20:80",
		"http:///nohost",
		"http:/etc/passwd",
		"http://localhost/x.mp4",
		"http://foo.localhost/x.mp4",
		"http://127.0.0.1:6053/x",
		"http://[::1]/x",
		"http://0.0.0.0/x",
		"http://169.254.169.254/latest",
		"http://224.0.0.1/x",
		"http://127.1/x",
		"http://2130706433/x",
		"http://0x7f000001/x",
		"http://0/x",
		"http://0x7f.1/x",
		"http://[fe80::1%25wlan0]/x.mp4",
		"http://[2001:db8::1%25eth0]:8096/x.mp4",
		"http://[::ffff:127.0.0.1]/x",
		"http://192.168.1.20/a b.mp4",
		"http://192.168.1.20/a\nHost: evil",
		"http://192.168.1.20/" + strings.Repeat("a", mostURL),
	}
	for _, u := range bad {
		if _, err := CheckURL(u); err == nil {
			t.Errorf("%q: accepted", u)
		}
	}
	if u, _ := CheckURL("HTTP://Example.com/x"); u.Scheme != "http" {
		t.Errorf("the scheme is handed on as %q", u.Scheme)
	}
}

// The log never carries a password or a media server's token.
func TestTheLogNeverHasTheSecrets(t *testing.T) {
	u, err := CheckURL("http://user:secret@192.168.1.20:8096/Videos/1/stream.mp4?api_key=token#t=3")
	if err != nil {
		t.Fatal(err)
	}
	r := Redacted(u)
	if strings.Contains(r, "secret") || strings.Contains(r, "token") || strings.Contains(r, "user") {
		t.Errorf("redacted: %s", r)
	}
	if !strings.Contains(r, "192.168.1.20:8096/Videos/1/stream.mp4") {
		t.Errorf("redacted too much: %s", r)
	}
	if Host(u) != "192.168.1.20" {
		t.Errorf("host %q", Host(u))
	}
}

const probeMP4 = `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'https://media.example.com/bbb.mp4':
  Metadata:
    major_brand     : isom
  Duration: 00:10:34.53, start: 0.000000, bitrate: 1048 kb/s
  Stream #0:0[0x1](und): Video: h264 (High) (avc1 / 0x31637661), yuv420p(tv, bt709, progressive), 1280x720 [SAR 1:1 DAR 16:9], 916 kb/s, 24 fps, 24 tbr, 12288 tbn (default)
      Metadata:
        handler_name    : VideoHandler
  Stream #0:1[0x2](und): Audio: aac (LC) (mp4a / 0x6134706D), 48000 Hz, stereo, fltp, 125 kb/s (default)
      Metadata:
        handler_name    : SoundHandler
At least one output file must be specified
`

const probePhone = `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'http://192.168.1.20/phone.mp4':
  Duration: 00:00:12.03, start: 0.000000, bitrate: 17000 kb/s
  Stream #0:0[0x1](eng): Video: h264 (High) (avc1 / 0x31637661), yuvj420p(pc, bt709, progressive), 1920x1080, 16900 kb/s, 29.97 fps, 29.97 tbr, 90k tbn (default)
      Metadata:
        creation_time   : 2026-01-01T00:00:00.000000Z
      Side data:
        displaymatrix: rotation of -90.00 degrees
  Stream #0:1[0x2](eng): Audio: aac (LC) (mp4a / 0x6134706D), 48000 Hz, stereo, fltp, 192 kb/s (default)
At least one output file must be specified
`

const probeHLS = `Input #0, hls, from 'https://media.example.com/live/index.m3u8':
  Duration: N/A, start: 1.400000, bitrate: N/A
  Program 0
    Metadata:
      variant_bitrate : 4000000
  Stream #0:0: Video: h264 (High) ([27][0][0][0] / 0x001B), yuv420p(tv, bt709, progressive), 1920x1080 [SAR 1:1 DAR 16:9], 60 fps, 60 tbr, 90k tbn
  Stream #0:1: Audio: aac (LC) ([15][0][0][0] / 0x000F), 48000 Hz, stereo, fltp
  Program 1
    Metadata:
      variant_bitrate : 2000000
  Stream #0:2: Video: h264 (Main) ([27][0][0][0] / 0x001B), yuv420p(tv, bt709, progressive), 1280x720 [SAR 1:1 DAR 16:9], 30 fps, 30 tbr, 90k tbn
  Stream #0:3: Audio: aac (LC) ([15][0][0][0] / 0x000F), 48000 Hz, stereo, fltp
  Program 2
  Stream #0:4: Video: h264 (Main) ([27][0][0][0] / 0x001B), yuv420p(tv, bt709, progressive), 640x360 [SAR 1:1 DAR 16:9], 30 fps, 30 tbr, 90k tbn
  Stream #0:5: Audio: aac (LC) ([15][0][0][0] / 0x000F), 48000 Hz, stereo, fltp
At least one output file must be specified
`

// What the public Mux test stream says: each variant's sound before its picture.
const probeMux = `Input #0, hls, from 'https://media.example.com/x36xhzz.m3u8':
  Duration: 00:10:34.58, start: 10.000000, bitrate: 0 kb/s
  Program 0
    Metadata:
      variant_bitrate : 0
  Stream #0:0[0x0]: Audio: aac (LC) ([15][0][0][0] / 0x000F), 44100 Hz, stereo, fltp, start 10.010100
  Stream #0:1[0x0]: Video: h264 (High) ([27][0][0][0] / 0x001B), yuv420p, 1280x720 [SAR 1:1 DAR 16:9], 60 fps, 60 tbr, 90k tbn, start 10.033322
  Program 1
    Metadata:
      variant_bitrate : 0
  Stream #0:2[0x1]: Audio: aac (HE-AAC) ([15][0][0][0] / 0x000F), 44100 Hz, stereo, fltp, start 10.000000
  Stream #0:3[0x1]: Video: h264 (Constrained Baseline) ([27][0][0][0] / 0x001B), yuv420p, 320x184 [SAR 1:1 DAR 40:23], 30 fps, 30 tbr, 90k tbn, start 10.000000
  Program 2
  Stream #0:4[0x2]: Audio: aac (HE-AAC) ([15][0][0][0] / 0x000F), 44100 Hz, stereo, fltp, start 10.000000
  Stream #0:5[0x2]: Video: h264 (Constrained Baseline) ([27][0][0][0] / 0x001B), yuv420p, 512x288 [SAR 1:1 DAR 16:9], 30 fps, 30 tbr, 90k tbn, start 10.000000
  Program 3
  Stream #0:6[0x3]: Audio: aac (LC) ([15][0][0][0] / 0x000F), 44100 Hz, stereo, fltp, start 10.010100
  Stream #0:7[0x3]: Video: h264 (High) ([27][0][0][0] / 0x001B), yuv420p, 848x480 [SAR 1:1 DAR 53:30], 60 fps, 60 tbr, 90k tbn, start 10.033322
  Stream #0:8[0x4]: Audio: aac (LC) ([15][0][0][0] / 0x000F), 44100 Hz, stereo, fltp, start 10.010100
At least one output file must be specified
`

const probeAnamorphic = `Input #0, mpegts, from 'http://192.168.1.20/dvd.ts':
  Duration: 00:01:00.00, start: 1.000000, bitrate: 6000 kb/s
  Stream #0:0[0x100]: Video: mpeg4 (Simple Profile) ([16][0][0][0] / 0x0010), yuv420p(tv, progressive), 720x480 [SAR 32:27 DAR 16:9], 29.97 fps, 29.97 tbr, 90k tbn
  Stream #0:1[0x101](eng): Audio: ac3 ([129][0][0][0] / 0x0081), 48000 Hz, 5.1(side), fltp, 448 kb/s
At least one output file must be specified
`

const probeCover = `Input #0, mov,mp4,m4a,3gp,3g2,mj2, from 'http://192.168.1.20/song.m4a':
  Duration: 00:03:30.00, start: 0.000000, bitrate: 256 kb/s
  Stream #0:0[0x1](und): Audio: aac (LC) (mp4a / 0x6134706D), 44100 Hz, stereo, fltp, 256 kb/s (default)
  Stream #0:1[0x0]: Video: mjpeg (Baseline), yuvj420p(pc, bt470bg/unknown/unknown), 600x600 [SAR 1:1 DAR 1:1], 90k tbr, 90k tbn (attached pic)
At least one output file must be specified
`

func TestWhatFFmpegSaysIsRead(t *testing.T) {
	i, err := ParseProbe(probeMP4)
	if err != nil {
		t.Fatal(err)
	}
	if i.Width != 1280 || i.Height != 720 || i.FPS != 24 || i.VideoIndex != 0 || i.AudioIndex != 0 ||
		i.Duration != 10*time.Minute+34530*time.Millisecond || i.Rotation != 0 || i.AudioCodec != "aac" {
		t.Errorf("mp4: %+v", i)
	}

	i, err = ParseProbe(probePhone)
	if err != nil {
		t.Fatal(err)
	}
	if i.Rotation != -90 || i.FPS != 29.97 {
		t.Errorf("phone: %+v", i)
	}
	if w, h := i.Display(); w != 1080 || h != 1920 {
		t.Errorf("a phone held upright shows %vx%v", w, h)
	}

	// A stream's variants: the biggest within 720 rows, and the sound that goes with it.
	i, err = ParseProbe(probeHLS)
	if err != nil {
		t.Fatal(err)
	}
	if i.VideoIndex != 1 || i.AudioIndex != 1 || i.Height != 720 || i.Duration != 0 {
		t.Errorf("hls: %+v", i)
	}

	i, err = ParseProbe(probeMux)
	if err != nil {
		t.Fatal(err)
	}
	if i.VideoIndex != 0 || i.AudioIndex != 0 || i.Height != 720 || i.FPS != 60 {
		t.Errorf("mux: %+v", i)
	}

	// Its sound is AC-3, as a media server's films so often are: decoded and mixed down.
	i, err = ParseProbe(probeAnamorphic)
	if err != nil {
		t.Fatal(err)
	}
	if i.SARNum != 32 || i.SARDen != 27 || i.AudioIndex != 0 || i.AudioCodec != "ac3" || i.VideoCodec != "mpeg4" {
		t.Errorf("anamorphic: %+v", i)
	}
	// DTS is not decoded: the video plays silent.
	i, _ = ParseProbe(strings.Replace(probeAnamorphic, "Audio: ac3", "Audio: dts", 1))
	if i.AudioIndex != -1 || i.AudioCodec != "dts" {
		t.Errorf("dts: %+v", i)
	}

	// A song with its cover is not a video.
	if _, err := ParseProbe(probeCover); err == nil {
		t.Error("a song's cover was taken for a video")
	}
	// ffmpeg's own complaint is passed on.
	_, err = ParseProbe("https://media.example.com/x.mp4: Server returned 404 Not Found\n")
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("a 404 gave %v", err)
	}
	_, err = ParseProbe(strings.Replace(probeMP4, "h264 (High)", "hevc (Main)", 1))
	if err == nil || !strings.Contains(err.Error(), "hevc") {
		t.Errorf("hevc gave %v", err)
	}
}

// The picture keeps its shape: as large as fits, in the middle, black around it, all even.
func TestThePictureKeepsItsShape(t *testing.T) {
	show := Screen{W: 960, H: 480, Rotated: true, PixFmt: "bgra"}
	for _, tc := range []struct {
		name string
		info Info
		want Fit
	}{
		{"16:9", Info{Width: 1280, Height: 720}, Fit{W: 852, H: 480, X: 54, Y: 0}},
		{"854x480", Info{Width: 854, Height: 480}, Fit{W: 854, H: 480, X: 52, Y: 0}},
		{"2:1 fills it", Info{Width: 1920, Height: 960}, Fit{W: 960, H: 480}},
		{"scope", Info{Width: 1920, Height: 800}, Fit{W: 960, H: 400, X: 0, Y: 40}},
		{"4:3", Info{Width: 640, Height: 480}, Fit{W: 640, H: 480, X: 160, Y: 0}},
		{"upright phone", Info{Width: 1920, Height: 1080, Rotation: -90}, Fit{W: 270, H: 480, X: 344, Y: 0}},
		{"anamorphic DVD", Info{Width: 720, Height: 480, SARNum: 32, SARDen: 27}, Fit{W: 852, H: 480, X: 54, Y: 0}},
		{"tiny", Info{Width: 160, Height: 90}, Fit{W: 852, H: 480, X: 54, Y: 0}},
		{"odd", Info{Width: 1001, Height: 499}, Fit{W: 962 &^ 1, H: 480}},
	} {
		got := FitIn(tc.info, show)
		if tc.name == "odd" {
			if got.W%2 != 0 || got.H%2 != 0 || got.X%2 != 0 || got.Y%2 != 0 || got.W > 960 || got.H > 480 {
				t.Errorf("%s: %+v", tc.name, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("%s: %+v, want %+v", tc.name, got, tc.want)
		}
	}
	// The Spot: the square, as the Show's screen is filled.
	if got := FitIn(Info{Width: 1280, Height: 720}, Screen{W: 480, H: 480}); got != (Fit{W: 480, H: 270, X: 0, Y: 104}) {
		t.Errorf("spot 16:9: %+v", got)
	}
	// The Show 8.
	if got := FitIn(Info{Width: 1280, Height: 720}, Screen{W: 1280, H: 800}); got != (Fit{W: 1280, H: 720, X: 0, Y: 40}) {
		t.Errorf("show 8: %+v", got)
	}
}

func TestFrameRates(t *testing.T) {
	for fps, want := range map[float64]Rate{
		24: {24, 1}, 25: {25, 1}, 30: {30, 1}, 23.976: {24000, 1001}, 29.97: {30000, 1001},
		59.94: {30000, 1001}, 60: {30, 1}, 50: {25, 1}, 120: {30, 1}, 0: {30, 1}, 12.5: {12500, 1000},
	} {
		if got := FrameRate(fps); got != want {
			t.Errorf("%v fps: %v, want %v", fps, got, want)
		}
	}
}

// The decoder is run with the network alone, a picture made for the panel on fd 3 and the sound on
// its standard output.
func TestTheDecoderArguments(t *testing.T) {
	info, _ := ParseProbe(probeMP4)
	a := DecodeArgs(Decoding{URL: "https://media.example.com/bbb.mp4", Info: info,
		Screen: Screen{W: 960, H: 480, Rotated: true, PixFmt: "bgra"}, CAFile: "/etc/ssl/certs/ca-certificates.crt"})
	has := func(seq ...string) bool {
		for i := 0; i+len(seq) <= len(a); i++ {
			if slices.Equal(a[i:i+len(seq)], seq) {
				return true
			}
		}
		return false
	}
	for _, seq := range [][]string{
		{"-protocol_whitelist", "http,https,tcp,tls,hls,crypto"},
		{"-max_pixels", "2088960"},
		{"-tls_verify", "1"},
		{"-ca_file", "/etc/ssl/certs/ca-certificates.crt"},
		{"-threads", "3"},
		{"-i", "https://media.example.com/bbb.mp4"},
		{"-map", "0:v:0"},
		{"-vf", "fps=fps=24/1:start_time=0,scale=852:480:flags=fast_bilinear,format=yuv420p,pad=960:480:54:0:black,transpose=1,scale,format=bgra"},
		{"-f", "rawvideo", "-pix_fmt", "bgra", "pipe:3"},
		{"-map", "0:a:0"},
		{"-af", "aresample=48000:async=1:first_pts=0,acompressor=threshold=0.0125:ratio=5:attack=20:release=800:knee=4:makeup=12:detection=rms,alimiter=limit=0.89:attack=5:release=100:level=disabled", "-ac", "2", "-ar", "48000", "-f", "s16le", "pipe:1"},
	} {
		if !has(seq...) {
			t.Errorf("no %q in %q", seq, a)
		}
	}
	// The whitelist comes before the address, or it does not apply to it.
	if slices.Index(a, "-protocol_whitelist") > slices.Index(a, "-i") {
		t.Error("the whitelist is after the input")
	}
	for _, s := range a {
		if strings.Contains(s, "file") && s != "/etc/ssl/certs/ca-certificates.crt" && s != "-ca_file" {
			t.Errorf("%q", s)
		}
	}

	// No sound to play: no sound output. 1080p: four threads. Plain http: no certificate options, which
	// ffmpeg would refuse for an input that opens no TLS.
	info, _ = ParseProbe(probeAnamorphic)
	info.Height, info.Width, info.AudioIndex = 1080, 1920, -1
	a = DecodeArgs(Decoding{URL: "http://192.168.1.20/dvd.ts", Info: info, Screen: Screen{W: 960, H: 480, Rotated: true}, CAFile: "/etc/ssl/certs/ca-certificates.crt"})
	if has("pipe:1") || !has("-threads", "4") || has("-skip_loop_filter") || has("-tls_verify") || has("-ca_file") {
		t.Errorf("silent 1080p: %q", a)
	}
	// Insecure TLS: https unchecked.
	a = DecodeArgs(Decoding{URL: "https://192.168.1.20/dvd.ts", Info: info, Screen: Screen{W: 960, H: 480, Rotated: true}, CAFile: "/etc/ssl/certs/ca-certificates.crt", Insecure: true})
	if !has("-tls_verify", "0") || has("-ca_file") {
		t.Errorf("insecure: %q", a)
	}
	// Unturned (a panel drawn as it is): no transpose, and a filled screen needs no bars.
	if f := Filter(Info{Width: 1920, Height: 960}, Screen{W: 960, H: 480, PixFmt: "rgba"}); f != "fps=fps=30/1:start_time=0,scale=960:480:flags=fast_bilinear,format=yuv420p,scale,format=rgba" {
		t.Errorf("filter: %s", f)
	}

	// Through the guard proxy: the proxy given, and its CONNECT allowed.
	a = DecodeArgs(Decoding{URL: "https://192.168.1.20/x.mp4", Info: info, Screen: Screen{W: 960, H: 480}, Proxy: "http://video:pw@127.0.0.1:4567"})
	if !has("-http_proxy", "http://video:pw@127.0.0.1:4567") || !has("-protocol_whitelist", "http,https,tcp,tls,hls,crypto,httpproxy") {
		t.Errorf("proxied: %q", a)
	}
	p := ProbeArgs("http://192.168.1.20/x.mp4", "", false, "")
	if p[len(p)-1] != "http://192.168.1.20/x.mp4" || slices.Index(p, "-protocol_whitelist") < 0 || slices.Index(p, "-ca_file") >= 0 ||
		slices.Index(p, "-max_pixels") < 0 {
		t.Errorf("probe: %q", p)
	}
}
