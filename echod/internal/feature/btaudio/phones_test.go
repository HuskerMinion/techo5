//go:build linux

package btaudio

import (
	"slices"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluealsa"
	"github.com/HuskerMinion/techo5/echod/internal/lib/bluez"
	"github.com/godbus/dbus/v5"
)

func TestWhatIsAPhone(t *testing.T) {
	const speaker = "00:11:22:33:44:55"
	source := []string{"0000110A-0000-1000-8000-00805F9B34FB"}
	for name, c := range map[string]struct {
		d    bluez.Device
		want bool
	}{
		"sends audio":           {bluez.Device{Paired: true, UUIDs: source}, true},
		"says it is a phone":    {bluez.Device{Paired: true, Icon: "phone"}, true},
		"not paired":            {bluez.Device{Icon: "phone", UUIDs: source}, false},
		"plays audio too":       {bluez.Device{Paired: true, UUIDs: source, AudioSink: true}, false},
		"the remembered device": {bluez.Device{Paired: true, Address: speaker, UUIDs: source}, false},
		"a headset, no sink":    {bluez.Device{Paired: true, Icon: "audio-headset", UUIDs: source}, false},
		"headphones":            {bluez.Device{Paired: true, Icon: "audio-headphones"}, false},
		"an audio card":         {bluez.Device{Paired: true, Icon: "audio-card"}, false},
		"neither":               {bluez.Device{Paired: true, Icon: "input-keyboard"}, false},
	} {
		if got := isPhone(c.d, speaker); got != c.want {
			t.Errorf("%s: isPhone = %v, want %v", name, got, c.want)
		}
	}
}

func TestWhichPhoneJoins(t *testing.T) {
	const speaker = "00:11:22:33:44:55"
	phone := func(path, addr, name string) bluez.Device {
		return bluez.Device{Path: dbus.ObjectPath(path), Address: addr, Name: name, Paired: true, Connected: true, Icon: "phone"}
	}
	a := phone("/a", "AA:00:00:00:00:01", "Phone A")
	b := phone("/b", "AA:00:00:00:00:02", "Phone B")
	off := a
	off.Connected = false
	buds := bluez.Device{Path: "/c", Address: "AA:00:00:00:00:03", Paired: true, Connected: true, Icon: "audio-headphones"}
	up := func(paths ...string) map[string]bool {
		m := map[string]bool{}
		for _, p := range paths {
			m[p] = true
		}
		return m
	}
	for name, c := range map[string]struct {
		devices    []bluez.Device
		audio, was map[string]bool
		open       bool
		joined     string
		linked     bool
		wantName   string
	}{
		"audio comes up while open":       {[]bluez.Device{a}, up("/a"), up(), true, a.Address, true, "Phone A"},
		"connected, audio not up yet":     {[]bluez.Device{a}, up(), up(), true, "", true, "Phone A"},
		"audio already up before":         {[]bluez.Device{a}, up("/a"), up("/a"), true, "", true, "Phone A"},
		"second phone joins, first stays": {[]bluez.Device{a, b}, up("/a", "/b"), up("/a"), true, b.Address, true, "Phone A"},
		"window closed":                   {[]bluez.Device{a}, up("/a"), up(), false, "", true, "Phone A"},
		"not connected":                   {[]bluez.Device{off}, up("/a"), up(), true, "", false, ""},
		"earbuds are not a phone":         {[]bluez.Device{buds}, up("/c"), up(), true, "", false, ""},
	} {
		l := lookAtPhones(c.devices, speaker, c.audio, c.was, c.open)
		if l.joined != c.joined || l.linked != c.linked || l.name != c.wantName {
			t.Errorf("%s: joined %q linked %v name %q, want %q %v %q", name, l.joined, l.linked, l.name, c.joined, c.linked, c.wantName)
		}
	}
	// What the look remembers is what the next look compares with: the audio that was up, phones only.
	l := lookAtPhones([]bluez.Device{a, buds}, speaker, up("/a", "/c"), up(), true)
	if !l.audio["/a"] || len(l.audio) != 1 {
		t.Errorf("remembered audio %v, want only /a", l.audio)
	}
}

// A phone another took the speaker from takes it back once the speaker is free, if it is still
// sending; one that stopped sending is forgotten, and one taken back is not owed it again.
func TestADisplacedPhoneTakesTheSpeakerBack(t *testing.T) {
	pixel := received{item: "Bluetooth: Pixel"}
	g5 := received{item: "Bluetooth: G5"}
	displaced := map[string]bool{"Bluetooth: Pixel": true}

	if got := takeBack(displaced, []received{pixel, g5}, false); got != nil {
		t.Fatalf("speaker busy: took back %q", got.item)
	}
	if !displaced["Bluetooth: Pixel"] {
		t.Fatal("still sending, yet no longer owed the speaker")
	}
	got := takeBack(displaced, []received{g5, pixel}, true)
	if got == nil || got.item != pixel.item {
		t.Fatalf("speaker free: took back %v, want the Pixel", got)
	}
	if len(displaced) != 0 {
		t.Errorf("taken back, still displaced: %v", displaced)
	}
	if got := takeBack(displaced, []received{pixel}, true); got != nil {
		t.Errorf("taken back twice: %q", got.item)
	}

	displaced["Bluetooth: Pixel"] = true
	if got := takeBack(displaced, []received{g5}, true); got != nil || len(displaced) != 0 {
		t.Errorf("stopped sending: took %v, displaced %v", got, displaced)
	}
}

// Every volume step handed to a phone comes back from it as the same step, so the device's own level
// echoed back by the phone moves nothing; and the ends meet the ends.
func TestPhoneLevelsAndSteps(t *testing.T) {
	for step := 0; step <= media.VolumeSteps; step++ {
		if got := stepFor(levelFor(step)); got != step {
			t.Errorf("step %d -> level %d -> step %d", step, levelFor(step), got)
		}
	}
	if levelFor(0) != 0 || levelFor(media.VolumeSteps) != bluealsa.VolumeMax {
		t.Errorf("ends: %d, %d", levelFor(0), levelFor(media.VolumeSteps))
	}
	if stepFor(bluealsa.VolumeMax) != media.VolumeSteps || stepFor(1) != 0 {
		t.Errorf("phone ends: %d, %d", stepFor(bluealsa.VolumeMax), stepFor(1))
	}
}

// A device connecting rises, one going falls, a device this side is busy with is not chimed for, and
// nothing changing is silent.
func TestLinkTone(t *testing.T) {
	set := func(paths ...string) map[string]bool {
		m := map[string]bool{}
		for _, p := range paths {
			m[p] = true
		}
		return m
	}
	for name, c := range map[string]struct {
		was, now, busy map[string]bool
		want           []speaker.Note
	}{
		"earbuds connect":        {set(), set("/buds"), set(), speaker.ToneBTConnect},
		"a phone joins":          {set("/buds"), set("/buds", "/phone"), set(), speaker.ToneBTConnect},
		"earbuds go":             {set("/buds"), set(), set(), speaker.ToneBTDisconnect},
		"nothing changes":        {set("/buds"), set("/buds"), set(), nil},
		"remade by this side":    {set("/buds"), set(), set("/buds"), nil},
		"one in, one out":        {set("/a"), set("/b"), set(), speaker.ToneBTConnect},
		"busy one in, other out": {set("/a"), set("/b"), set("/b"), speaker.ToneBTDisconnect},
	} {
		if got := linkTone(c.was, c.now, c.busy); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// A level read just after one was handed to the phone, rounded by the phone to its own steps, is not
// the slider moving; a real move is, and so is any change once the phone has said its level back.
func TestSliderMove(t *testing.T) {
	for name, c := range map[string]struct {
		level, last int
		known, ours bool
		want        bool
	}{
		"nothing known":             {80, 0, false, false, false},
		"no change":                 {80, 80, true, false, false},
		"the slider moved":          {60, 80, true, false, true},
		"ours, rounded by a step":   {levelFor(8) + 3, levelFor(8), true, true, false},
		"ours, but moved far":       {levelFor(20), levelFor(8), true, true, true},
		"not ours, moved by a step": {levelFor(9), levelFor(8), true, false, true},
	} {
		if got := sliderMove(c.level, c.last, c.known, c.ours); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// Forget forgets what the device plays to when it plays to something, else the remembered device.
func TestForgetTarget(t *testing.T) {
	devices := []bluez.Device{{Path: "/a", Address: "AA:00:00:00:00:01"}, {Path: "/b", Address: "AA:00:00:00:00:02"}}
	if got := forgetTarget(devices, "/a", "AA:00:00:00:00:02"); got != "AA:00:00:00:00:01" {
		t.Errorf("playing to A, B remembered: forgets %q", got)
	}
	if got := forgetTarget(devices, "", "AA:00:00:00:00:02"); got != "AA:00:00:00:00:02" {
		t.Errorf("playing to nothing: forgets %q", got)
	}
	if got := forgetTarget(devices, "/gone", "AA:00:00:00:00:02"); got != "AA:00:00:00:00:02" {
		t.Errorf("playing to a device no longer listed: forgets %q", got)
	}
}

// Steps handed on faster than the phone is told keep only the latest, and handing one never waits.
func TestHandLevelKeepsTheLatest(t *testing.T) {
	f := &Feature{levelTo: make(chan int, 1)}
	for _, step := range []int{6, 7, 8, 9} {
		f.handLevel(step)
	}
	if got := <-f.levelTo; got != 9 {
		t.Errorf("handed on %d, want the latest, 9", got)
	}
	select {
	case got := <-f.levelTo:
		t.Errorf("another step waiting: %d", got)
	default:
	}
}

// A phone takes the speaker back only when nothing else has it.
func TestSpeakerFree(t *testing.T) {
	if !speakerFree(false, false, false) {
		t.Error("nothing playing: not free")
	}
	for name, c := range map[string][3]bool{"playing": {true, false, false}, "paused": {false, true, false}, "a remote playing": {false, false, true}} {
		if speakerFree(c[0], c[1], c[2]) {
			t.Errorf("%s: free", name)
		}
	}
}

// A phone joining phone pairing is chimed for by the pairing, not by the connect tone as well.
func TestHoldForJoin(t *testing.T) {
	if holdForJoin(speaker.ToneBTConnect, "AA:00:00:00:00:01") != nil {
		t.Error("connect tone for a phone that just joined")
	}
	if !slices.Equal(holdForJoin(speaker.ToneBTConnect, ""), speaker.ToneBTConnect) {
		t.Error("connect tone held with no phone joining")
	}
	if !slices.Equal(holdForJoin(speaker.ToneBTDisconnect, "AA:00:00:00:00:01"), speaker.ToneBTDisconnect) {
		t.Error("disconnect tone held for a join")
	}
}

// The names the diagnostics leave out are the paired devices' and the remembered one's, each once.
func TestPairedNames(t *testing.T) {
	got := pairedNames([]bluez.Device{
		{Name: "Phone A", Paired: true},
		{Name: "Earbuds", Paired: true},
		{Name: "A neighbor's speaker"},
		{Name: "Earbuds", Paired: true},
		{Paired: true},
	}, "Earbuds")
	if !slices.Equal(got, []string{"Earbuds", "Phone A"}) {
		t.Errorf("names %q", got)
	}
}

// AirPlay, Spotify Connect, DLNA or a remote playing leaves no phone owed the speaker; another phone
// playing, or nothing, does not clear what is owed.
func TestOthersHadIt(t *testing.T) {
	for name, c := range map[string]struct {
		receiving string
		external  bool
		want      bool
	}{
		"nothing":            {"", false, false},
		"another phone":      {media.BluetoothPrefix + "Phone B", false, false},
		"AirPlay":            {"AirPlay", false, true},
		"Spotify Connect":    {"Spotify", false, true},
		"Music Assistant":    {"", true, true},
		"a phone and remote": {media.BluetoothPrefix + "Phone B", true, true},
	} {
		if got := othersHadIt(c.receiving, c.external); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

// A level read just after one was handed to the phone may be it said back; a press after that, even
// one step, is the slider moving, though the phone never said the handed level back.
func TestEchoWindow(t *testing.T) {
	f := &Feature{ours: map[string]time.Time{}}
	handed := time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC)
	f.ours["/p"] = handed.Add(echoFor)
	last := levelFor(8)
	if sliderMove(last+3, last, true, f.echoing("/p", handed.Add(time.Second))) {
		t.Error("the handed level said back rounded, a second later, taken for the slider")
	}
	later := handed.Add(echoFor + time.Second)
	if !sliderMove(levelFor(9), last, true, f.echoing("/p", later)) {
		t.Error("a one-step press after the window not taken for the slider")
	}
	if f.echoing("/other", handed) {
		t.Error("a stream nothing was handed to is echoing")
	}
}

// A phone that starts sending is listened to before it takes the speaker only from something else that
// is playing: not from another phone, and not when nothing plays.
func TestOthersSounding(t *testing.T) {
	for name, c := range map[string]struct {
		receiving         string
		playing, external bool
		want              bool
	}{
		"nothing":                   {"", false, false, false},
		"Music Assistant":           {"", false, true, true},
		"a station of the device's": {"", true, false, true},
		"AirPlay":                   {"AirPlay", true, false, true},
		"another phone":             {media.BluetoothPrefix + "Phone B", true, false, false},
		"a phone and remote":        {media.BluetoothPrefix + "Phone B", true, true, false},
	} {
		if got := othersSounding(c.receiving, c.playing, c.external); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}
