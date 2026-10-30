package takes

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
)

// The zip is a folder of WAVs a trainer reads as they are, one take each: RIFF, PCM, mono, 16 kHz,
// 16-bit, holding exactly the take cut from the recording, and a takes.json that says so.
func TestTheZipHoldsWAVsATrainerReads(t *testing.T) {
	d, f := newDevice(t)
	m := d.recording(t, f, "Hey Jarvis")
	d.takesOf(t, m, 3)
	f.Stop()
	st := settled(t, f, Kept)

	name, write, ok := f.Zip("browser")
	if !ok {
		t.Fatal("no zip for the browser that recorded")
	}
	var b bytes.Buffer
	if err := write(&b); err != nil {
		t.Fatal(err)
	}
	if b.Len() > st.Bytes+st.Bytes/10 || b.Len() < st.Bytes-st.Bytes/2 {
		t.Errorf("the zip is %d bytes, and the page said about %d", b.Len(), st.Bytes)
	}
	z, err := zip.NewReader(bytes.NewReader(b.Bytes()), int64(b.Len()))
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	pcm, cuts := f.pcm, f.cuts
	f.mu.Unlock()

	var names []string
	var file takesFile
	for _, e := range z.File {
		names = append(names, e.Name)
		r, err := e.Open()
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(r)
		if e.Name == "takes.json" {
			if err := json.Unmarshal(body, &file); err != nil {
				t.Fatalf("takes.json: %v", err)
			}
			continue
		}
		if e.Method != zip.Store {
			t.Errorf("%s is compressed", e.Name)
		}
		h := body[:44]
		le16 := func(at int) int { return int(binary.LittleEndian.Uint16(h[at:])) }
		le32 := func(at int) int { return int(binary.LittleEndian.Uint32(h[at:])) }
		if string(h[0:4]) != "RIFF" || string(h[8:16]) != "WAVEfmt " || string(h[36:40]) != "data" ||
			le16(20) != 1 || le16(22) != 1 || le32(24) != mic.Rate || le32(28) != 2*mic.Rate || le16(32) != 2 || le16(34) != 16 ||
			le32(40) != len(body)-44 || le32(4) != len(body)-8 {
			t.Errorf("%s has the header % x", e.Name, h)
		}
		n := len(names) - 1
		want := pcm[cuts[n].from*mic.FrameSamples : cuts[n].to*mic.FrameSamples]
		got := make([]int16, (len(body)-44)/2)
		binary.Read(bytes.NewReader(body[44:]), binary.LittleEndian, got)
		if !slices.Equal(got, want) {
			t.Errorf("%s does not hold the take cut from the recording", e.Name)
		}
	}
	wantNames := []string{"hey_jarvis/hey_jarvis-01.wav", "hey_jarvis/hey_jarvis-02.wav", "hey_jarvis/hey_jarvis-03.wav", "takes.json"}
	if !slices.Equal(names, wantNames) {
		t.Errorf("the zip holds %v", names)
	}
	if file.Version != 1 || file.Word != "Hey Jarvis" || file.Kind != "word" || file.Rate != mic.Rate ||
		len(file.Takes) != 3 || file.Takes[0].File != wantNames[0] || file.Takes[0].LengthMs == 0 {
		t.Errorf("takes.json says %+v", file)
	}
	if name != "techo5-takes-hey_jarvis-20261026-180000.zip" {
		t.Errorf("the zip is saved as %q", name)
	}
	one, _, ok := f.Take(2, "browser")
	if !ok || !bytes.Equal(one[44:], wavBody(pcm[cuts[1].from*mic.FrameSamples:cuts[1].to*mic.FrameSamples])) {
		t.Error("take 2 on its own is not take 2 of the zip")
	}
	for _, n := range []int{0, 4, -1} {
		if _, _, ok := f.Take(n, "browser"); ok {
			t.Errorf("take %d of 3 was given", n)
		}
	}
}

func wavBody(pcm []int16) []byte {
	var b bytes.Buffer
	binary.Write(&b, binary.LittleEndian, pcm)
	return b.Bytes()
}

// Takes of something like the word go in a folder of their own, for the model's negatives.
func TestNotTheWordGoesInAFolderOfItsOwn(t *testing.T) {
	if got := folder("Hey Jarvis", NotWord); got != "not_hey_jarvis" {
		t.Errorf("the negatives' folder is %q", got)
	}
}

// Whatever is typed, the zip's names are made only of letters, digits and underscores.
func TestAWordNeverNamesAPath(t *testing.T) {
	for in, want := range map[string]string{
		"../../etc/passwd":         "etc_passwd",
		"Hey/Jarvis":               "hey_jarvis",
		`C:\Windows`:               "c_windows",
		"":                         "word",
		"...":                      "word",
		"Ünïcode":                  "n_code",
		strings.Repeat("ab ", 30):  "ab_ab_ab_ab_ab_ab_ab_ab_ab_ab_ab",
		"Okay   Nabu!":             "okay_nabu",
		"turn it up\x00../../evil": "turn_it_up_evil",
	} {
		got := Slug(in)
		if got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
		if strings.ContainsAny(got, `/\.`) || len(got) > 32 {
			t.Errorf("Slug(%q) = %q could name a path", in, got)
		}
	}
}

// A download stops as soon as the takes are thrown away under it: the switch going off or the page
// closing takes them back from a browser still downloading too.
func TestADownloadStopsWhenTheTakesAreThrownAway(t *testing.T) {
	d, f := newDevice(t)
	m := d.recording(t, f, "Hey Jarvis")
	d.takesOf(t, m, 3)
	f.Stop()
	settled(t, f, Kept)
	_, write, ok := f.Zip("browser")
	if !ok {
		t.Fatal("no zip")
	}
	f.Discard("Record takes of a word was switched off")
	var b bytes.Buffer
	if err := write(&b); err != errGone {
		t.Errorf("writing after the takes were thrown away: %v", err)
	}
	if bytes.Contains(b.Bytes(), []byte("RIFF")) {
		t.Error("a take was written after the takes were thrown away")
	}
}

// takes.json says what shaped the sound as it was: leveled only when the leveler was on.
func TestTakesSayWhetherTheyWereLeveled(t *testing.T) {
	if s := heardAs(config.Microphone{Leveling: true}); !strings.Contains(s, "leveled") {
		t.Errorf("leveled takes say %q", s)
	}
	if s := heardAs(config.Microphone{}); strings.Contains(s, "and leveled") {
		t.Errorf("takes with the leveler off say %q", s)
	}
}
