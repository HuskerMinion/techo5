package redact

import (
	"strings"
	"testing"
)

// The things the project's own rules say must never reach a public issue: addresses, names, keys,
// serial numbers. This test is the list, and it is the point of the package.
func TestTheThingsThatMustNotGetOut(t *testing.T) {
	r := New()
	r.Known("device", "Guest's Desk")
	r.Known("wifi", "HomeNet-5G")
	r.Known("serial", "G000AA0000000000")

	in := strings.Join([]string{
		`I [12.34] wifi joined ssid="HomeNet-5G" addrs=192.0.2.96`,
		`I [12.40] bluetooth up address=aa:bb:cc:dd:ee:ff`,
		`I [12.55] home assistant http://198.51.100.155:8123 token=eyJhbGciOiJIUzI1NiJ9.abc`,
		`I [13.02] this device is Guest's Desk (G000AA0000000000)`,
		`I [13.10] ipv6 2001:db8:abcd:1234:5678:9abc:def0:1111`,
		`I [13.20] phone registered peer=+15551234567 psk="s3cretpassphrase"`,
		`I [13.30] serving on 127.0.0.1:8899`,
	}, "\n")

	out := r.Text(in)
	for _, leak := range []string{
		"HomeNet-5G", "192.0.2.96", "aa:bb:cc:dd:ee:ff", "198.51.100.155",
		"eyJhbGciOiJIUzI1NiJ9", "Guest's Desk", "G000AA0000000000",
		"2001:db8", "+15551234567", "s3cretpassphrase",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("%q is still in the text:\n%s", leak, out)
		}
	}
	// Localhost says nothing about anybody and is left alone, so a log still reads.
	if !strings.Contains(out, "127.0.0.1") {
		t.Errorf("localhost was replaced, which helps nobody:\n%s", out)
	}
}

// The same address twice reads as the same thing, and two different ones stay different: a log about
// two devices talking has to still make sense afterwards.
func TestTheSameValueReadsTheSameTwice(t *testing.T) {
	r := New()
	out := r.Text("from 192.168.1.10 to 192.168.1.20, then 192.168.1.10 again")
	first := strings.Index(out, "<ip-1>")
	if first < 0 || strings.Count(out, "<ip-1>") != 2 {
		t.Errorf("the repeated address did not come back the same:\n%s", out)
	}
	if !strings.Contains(out, "<ip-2>") {
		t.Errorf("two different addresses became one thing:\n%s", out)
	}
}

// A key's name stays so the line still says what it was, and only the value goes.
func TestAKeyKeepsItsName(t *testing.T) {
	out := New().Text(`api_key="abcdef123456" psk=another-secret-value`)
	if strings.Contains(out, "abcdef123456") || strings.Contains(out, "another-secret-value") {
		t.Errorf("a key survived:\n%s", out)
	}
	if !strings.Contains(out, "api_key") || !strings.Contains(out, "psk") {
		t.Errorf("the line no longer says what was there:\n%s", out)
	}
}

// Something short is not worth replacing everywhere it appears, or a log becomes unreadable.
func TestShortValuesAreLeftAlone(t *testing.T) {
	r := New()
	r.Known("device", "A")
	if out := r.Text("A quick brown fox"); out != "A quick brown fox" {
		t.Errorf("a one-letter name was replaced through the text: %q", out)
	}
}

// Every shape Go prints an address in, because the compressed ones are the ones a device actually
// logs: the household's delegated prefix is in all of them, and the bundle says addresses are gone.
func TestAddressesInEveryShapeGo(t *testing.T) {
	for _, in := range []string{
		"global 2601:abc:def::1",
		"link local fe80::1%eth0",
		"delegated 2601:abc:def::/56",
		"written out 2601:0abc:0def:0001:0002:0003:0004:0005",
		"mixed case FE80::AB:CD",
	} {
		out := New().Text(in)
		if !strings.Contains(out, "<ipv6-") {
			t.Errorf("%q kept its address: %s", in, out)
		}
		for _, leak := range []string{"2601", "fe80", "FE80"} {
			if strings.Contains(out, leak) {
				t.Errorf("%q still shows %s: %s", in, leak, out)
			}
		}
	}
}

// And the things that merely look like an address are left alone, because a bundle nobody can read is
// no safer than one nobody should send.
func TestThingsThatOnlyLookLikeAddresses(t *testing.T) {
	for _, line := range []string{
		"I [ 13.30] alarm at 03:02:11 tomorrow",
		"I [ 13.31] woke after 1h2m3.5s, next in 250ms",
		"I [ 13.32] note: the thing is this: it takes a while",
		"I [ 13.33] ratio 12:34 and 1:2:3 are not addresses",
		// Prose and code fragments go into a bundle too, and a colon pair in the middle of a name is
		// not an address however willingly the parser reads one out of it.
		"I [ 13.35] parse error in std::string, see std::vector<int>",
		"I [ 13.36] the unspecified address is :: and it names nobody",
		"I [ 13.37] 999.999.999.999 is not a host",
		"I [ 13.38] running firmware v1.2.3.4 and payload 1.2.3.4.5",
	} {
		if got := New().Text(line); got != line {
			t.Errorf("a line that holds no address was changed:\n old: %s\n new: %s", line, got)
		}
	}
}

// The unspecified and loopback addresses say nothing about anybody, so they stay and the line still
// says where something was listening.
func TestLoopbackStays(t *testing.T) {
	line := "I [ 13.34] serving on ::1 and 127.0.0.1"
	if got := New().Text(line); got != line {
		t.Errorf("loopback was replaced, which helps nobody:\n old: %s\n new: %s", line, got)
	}
}

// A MAC address has its own rule and keeps it: it must not be read as an address and must not be left
// behind either.
func TestAMACIsStillAMAC(t *testing.T) {
	out := New().Text("bluetooth up address=aa:bb:cc:dd:ee:ff")
	if strings.Contains(out, "aa:bb:cc") {
		t.Errorf("a mac survived: %s", out)
	}
	if !strings.Contains(out, "<mac-1>") {
		t.Errorf("a mac was taken for something else: %s", out)
	}
}

// A kernel stack trace prints its addresses as runs of hex that can start with ten or more digits;
// they are no telephone number, and taking them for one left traces reading [<<phone-1>e3d9f7>].
func TestKernelAddressesAreNotPhones(t *testing.T) {
	for _, line := range []string{
		"[  242.667317] [<0000000012e3d9f7>] tz_service_call+0x94/0xe8",
		"[  242.668013] [<00000000c9eeac12>] KREE_TeeServiceCall+0xc0/0x258",
		"[  242.669537] [<ffffff800812345a>] kthread+0xf8/0x100",
		"[  242.670139] [<0000000001234567>] ret_from_fork+0x10/0x40",
	} {
		if got := New().Text(line); got != line {
			t.Errorf("a kernel address was changed:\n old: %s\n new: %s", line, got)
		}
	}
	if got := New().Text("call to +15551234567 failed"); got == "call to +15551234567 failed" {
		t.Error("a telephone number was left in")
	}
}

// A telephone number next to a letter is still a telephone number: escaped in a message, in a URL,
// with an extension, or run on to a name. Only words that are hex all through are left.
func TestPhonesNextToLetters(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`Call me\n5551234567`, `Call me\n<phone-1>`},
		{"to=%2B15551234567", "to=%2B<phone-1>"},
		{"tel%3A5551234567", "tel%3A<phone-1>"},
		{"5551234567x12", "<phone-1>x12"},
		{"5551234567ext", "<phone-1>ext"},
		{"caller=Bob5551234567", "caller=Bob<phone-1>"},
		{"call to +15551234567 failed", "call to <phone-1> failed"},
		{"phone 5551234567", "phone <phone-1>"},
		{"deadbeef1234567890 5551234567", "deadbeef1234567890 <phone-1>"},
		{"commit 1234567890abcdef 5551234567", "commit 1234567890abcdef <phone-1>"},
		{"call 5551234567.", "call <phone-1>."},
		{"pid 1234 then 5551234567", "pid 1234 then <phone-1>"},
		// Two numbers a character apart are two numbers, and the second is not lost to the first.
		{"5551234567 5559876543", "<phone-1> <phone-2>"},
		{"5551234567,5559876543", "<phone-1>,<phone-2>"},
		{"5551234567/+15559876543", "<phone-1>/<phone-2>"},
		{"5551234567-5559876543", "<phone-1>-<phone-2>"},
		{"5551234567 and 5551234567 again", "<phone-1> and <phone-1> again"},
		// A name made of hex letters glued to a number is still a name and a number.
		{"a5551234567", "a<phone-1>"},
		{"Ada5551234567", "Ada<phone-1>"},
		{"5551234567abc", "<phone-1>abc"},
	} {
		if got := New().Text(tc.in); got != tc.want {
			t.Errorf("%q came out as %q, want %q", tc.in, got, tc.want)
		}
	}
	for _, line := range []string{
		"[  242.667317] [<00000000ab12cd34>] tz_service_call+0x94/0xe8",
		"[  242.667317] [<0000000012e3d9f7>] tz_service_call+0x94/0xe8",
		"[  242.667317] [<ffffff8012345678>] kthread+0xf8/0x100",
		"pc : 0000000012e3d9f7 lr : ffffff8012345678",
		"commit 1234567890abcdef1234567890abcdef12345678",
		"per_second=49.98765432109876",
		"uptime 12345678901234567890",
		"[  242.667317] [<00000a1234567890>] kthread+0xf8/0x100",
		"x0 : 0x1234567890 x1 : 0X1234567890",
		"cookie 9b1234567890",
		"key deadbeef1234567890",
		"id=12345678901234567890g",
		"offset=-1234567890 ts=1697461234.567",
		"rate 1234567890.5 and 0.1234567890",
		"version 1.2.3.4567890123",
		"uuid 12345678-1234-1234-1234-123456789012",
		"build 1.2.19-1234567890",
		"year 2026-1234567890",
	} {
		if got := New().Text(line); got != line {
			t.Errorf("changed:\n old: %s\n new: %s", line, got)
		}
	}
}

// A login in an address goes whatever the address's scheme, and the address is still taken out after.
func TestALoginInAnyAddressGoes(t *testing.T) {
	r := New()
	for in, want := range map[string]string{
		"rtsp://viewer:hunter22@198.51.100.7:554/live": "rtsp://<login>@<ip-1>:554/live",
		"stream go2rtc+http://admin:pw@example.net/":   "stream go2rtc+http://<login>@example.net/",
		"rtmps://only-a-token@example.net/app":         "rtmps://<login>@example.net/app",
	} {
		got := r.Text(in)
		if strings.Contains(got, "hunter22") || strings.Contains(got, "admin:pw") || strings.Contains(got, "only-a-token") {
			t.Errorf("%q kept its login: %q", in, got)
		}
		if !strings.Contains(got, "<login>@") {
			t.Errorf("%q: got %q, want something like %q", in, got, want)
		}
	}
}

// A Home Assistant entity id names a room or a person in its object id, so it goes wherever the
// patterns find one, whatever the domain.
func TestEntityIdsGo(t *testing.T) {
	r := New()
	out := r.Text(strings.Join([]string{
		`I dashboard tap entity=media_player.guest_room service=media_player.play_media`,
		`I dashboard thermostat entity=climate.master_bedroom to=21`,
		`I dashboard color entity=light.kids_lamp kelvin=2700`,
		`I camera button camera=front entity=binary_sensor.side_door_motion`,
		`I zone.home person.a_householder sun.sun`,
	}, "\n"))
	for _, leak := range []string{
		"media_player.guest_room", "media_player.play_media", "climate.master_bedroom",
		"light.kids_lamp", "binary_sensor.side_door_motion", "zone.home", "person.a_householder", "sun.sun",
	} {
		if strings.Contains(out, leak) {
			t.Errorf("%q is still in the text:\n%s", leak, out)
		}
	}
	if !strings.Contains(out, "<entity-1>") {
		t.Errorf("no entity placeholder went in:\n%s", out)
	}
}

// The same entity reads the same twice, so a log about one device still makes sense.
func TestAnEntityReadsTheSameTwice(t *testing.T) {
	out := New().Text("play on media_player.den, then stop media_player.den")
	if strings.Count(out, "<entity-1>") != 2 {
		t.Errorf("the repeated entity did not come back the same:\n%s", out)
	}
}

// What only looks like an entity id stays: a source file whose base is a domain, a package selector,
// a config file, a dotted name whose base is no domain. A stack trace has to still read.
func TestThingsThatOnlyLookLikeEntities(t *testing.T) {
	for _, line := range []string{
		"panic at internal/feature/display/timer.go:42",
		"source=update.go:128 msg=restarting",
		"gofmt -l person.go camera.go text.go",
		"built from climate_sheet.go and color_sheet.go",
		"calls fmt.Println and strings.Cut and image.NewRGBA",
		"read config.yaml and names.txt",
		"host.local reached",
	} {
		if got := New().Text(line); got != line {
			t.Errorf("a non-entity was redacted:\n in:  %s\n out: %s", line, got)
		}
	}
}
