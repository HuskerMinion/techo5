package asp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func b(o, l, r, f, s uint32, format, valid, shift, channels uint8, first uint16) Buffer {
	return Buffer{o, l, r, f, s, format, valid, shift, channels, first}
}
func c(bi, role uint8, idx uint16, off uint32) Channel { return Channel{bi, role, idx, off} }

// vectors are hand-specified PCM geometries, checked against committed independent golden bytes.
func vectors() map[string]Descriptor {
	maxBuffers := make([]Buffer, 255)
	maxChannels := make([]Channel, 255)
	for i := 0; i < 255; i++ {
		maxBuffers[i] = b(uint32(i*2), 2, 16000, 1, 2, FormatS16, 16, 0, 1, uint16(i))
		maxChannels[i] = c(uint8(i), RoleMIC, uint16(i), 0)
	}
	return map[string]Descriptor{
		"stereo_s24": {
			Buffers:  []Buffer{b(0, 36, 16000, 2, 18, FormatS24_3, 24, 0, 6, 0)},
			Channels: []Channel{c(0, RoleMIC, 0, 0), c(0, RoleMIC, 1, 3), c(0, RoleMIC, 2, 6), c(0, RoleMIC, 3, 9), c(0, RoleREF, 0, 12), c(0, RoleREF, 1, 15)},
		},
		"separate_three": {
			Buffers:  []Buffer{b(0, 8, 16000, 2, 4, FormatS16, 16, 0, 2, 0), b(8, 4, 16000, 2, 2, FormatS16, 16, 0, 1, 2), b(12, 4, 16000, 2, 2, FormatS16, 16, 0, 1, 3)},
			Channels: []Channel{c(0, RoleMIC, 0, 0), c(0, RoleMIC, 1, 2), c(1, RoleREF, 0, 0), c(2, RoleREF, 1, 0)},
		},
		"s32_low24": {
			Buffers:  []Buffer{b(0, 16, 48000, 2, 8, FormatS32, 24, 0, 2, 0)},
			Channels: []Channel{c(0, RoleMIC, 0, 0), c(0, RoleREF, 0, 4)},
		},
		"s32_high24": {
			Buffers:  []Buffer{b(0, 16, 48000, 2, 8, FormatS32, 24, 8, 2, 0)},
			Channels: []Channel{c(0, RoleMIC, 0, 0), c(0, RoleREF, 0, 4)},
		},
		"ref_absent": {
			Buffers:  []Buffer{b(0, 8, 16000, 2, 4, FormatS16, 16, 0, 2, 0)},
			Channels: []Channel{c(0, RoleMIC, 0, 0), c(0, RoleMIC, 1, 2)},
		},
		"packed_sparse": {
			Buffers:  []Buffer{b(0, 24, 16000, 2, 12, FormatS24_3, 20, 4, 2, 0)},
			Channels: []Channel{c(0, RoleREF, 0, 7), c(0, RoleMIC, 0, 0)},
		},
		"max_255": {Buffers: maxBuffers, Channels: maxChannels},
	}
}
func TestIndependentExpectedBytes(t *testing.T) {
	for name, desc := range vectors() {
		t.Run(name, func(t *testing.T) {
			got, e := Encode(desc)
			if e != nil {
				t.Fatal(e)
			}
			expected, e := os.ReadFile(filepath.Join("testdata", name+".bin"))
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(got, expected) {
				t.Fatalf("writer differs from independent PCM-only vector at %s", name)
			}
			if len(got) != HeaderSize+len(desc.Buffers)*BufferSize+len(desc.Channels)*ChannelSize ||
				len(got) > MaxDescriptorSize || got[4] != 1 || got[5] != 0 {
				t.Fatal("metadata/trailing fields or incorrect PCM-only header")
			}
			if HeaderSize != 16 || MaxDescriptorSize != 9196 {
				t.Fatal("PCM-only contract constants")
			}
			if name == "max_255" && len(got) != 9196 {
				t.Fatal("maximum descriptor not 9196")
			}

		})
	}
}
func TestReadyAndStereoREF(t *testing.T) {
	if ReadyByte != 0xa5 || OutputBytes != 640 {
		t.Fatal("startup/output contract")
	}
	pcm, e := os.ReadFile("testdata/stereo_s24.pcm")
	if e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(pcm[12:15], []byte{0xa1, 0xb2, 0xc3}) ||
		!bytes.Equal(pcm[15:18], []byte{0xd4, 0xe5, 0xf6}) {
		t.Fatal("independent REF source bytes lost")
	}
}
func TestRejectBadWriterInputs(t *testing.T) {
	base := vectors()["ref_absent"]
	bad := map[string]func(*Descriptor){
		"no buffers":      func(d *Descriptor) { d.Buffers = nil },
		"no mic":          func(d *Descriptor) { d.Channels[0].Role = RoleREF; d.Channels[1].Role = RoleREF },
		"invalid format":  func(d *Descriptor) { d.Buffers[0].Format = 4 },
		"bad shift":       func(d *Descriptor) { d.Buffers[0].LSBShift = 1 },
		"zero rate":       func(d *Descriptor) { d.Buffers[0].SampleRate = 0 },
		"bad offset":      func(d *Descriptor) { d.Buffers[0].Offset = 1 },
		"bad stride":      func(d *Descriptor) { d.Buffers[0].Stride = 3 },
		"last sample":     func(d *Descriptor) { d.Buffers[0].Length = 7 },
		"duplicate mic":   func(d *Descriptor) { d.Channels[1].RoleIndex = 0 },
		"bad role":        func(d *Descriptor) { d.Channels[1].Role = 3 },
		"buffer index":    func(d *Descriptor) { d.Channels[0].BufferIndex = 1 },
		"channel overlap": func(d *Descriptor) { d.Channels[1].FrameOffset = 1 },
	}
	for name, mutate := range bad {
		t.Run(name, func(t *testing.T) {
			d := Descriptor{Buffers: append([]Buffer(nil), base.Buffers...), Channels: append([]Channel(nil), base.Channels...)}
			mutate(&d)
			if _, e := Encode(d); e == nil {
				t.Fatal("invalid descriptor accepted")
			}
		})
	}
}

// These SHA-256 values are pinned to the independent, committed testdata
// golden files at 76379ddd10d635449fad0769e205715122642cef.
// Never derive these expected hashes from Encode or C decoder output.
var goldenBinarySHA256 = map[string]string{
	"max_255":        "5253975b3c0e1087c98bf848060d55e79c492dc2f58683852cd2b831be463868",
	"packed_sparse":  "ddf17ab5bdd236d17daf384255cf5bfca60e0e250385bc05363f16bb4dbefb63",
	"ref_absent":     "10bd7e60e0acf34fdeefc8cbc74ab7700e4e4f640efb790198d9c60a8ec35e2d",
	"s32_high24":     "127e31ed5d2af8dfb612b42a4c46105befe5152882e19656d65dc097b0b07500",
	"s32_low24":      "9263d7552556fbdc77253992d49556800a9b8370fa07aa5be908f4cde62b0556",
	"separate_three": "00349635bb1f12afba27cbf7145091d80326a4ca28648e8bd52144f479772f63",
	"stereo_s24":     "6eb97edd97f10a6ae114a973ed4ca8678602e47be9f2a31448e56aadc6d97251",
}

var transcriptSHA256 = map[string]struct {
	length int
	digest string
}{
	"max_255":        {length: 12019, digest: "e45cd99842f020f833080f1af7dbf2bcf01c4621a56b7f0969218438ea7de9d5"},
	"packed_sparse":  {length: 63, digest: "e6990cd1f0927d4457a7e77627bdd37c1fdc86302c6b11aee4bfea620f0dd78e"},
	"ref_absent":     {length: 60, digest: "68a13d9a459ff3dda65e99e43985a423c7c9c24026c1ffa92c74a6a18dd93e93"},
	"s32_high24":     {length: 62, digest: "33c4b8d0e36b38b73a3cb37b3fd616d83d58e1c969868af9870624fc0a2ea824"},
	"s32_low24":      {length: 62, digest: "cd0b0a7b2a67529fbf7a1c579670b7cdf9988ca0619df372279d5455e527e829"},
	"separate_three": {length: 141, digest: "674a18da0d37925711cc8c3a2c6019f4dcdbe23ac59a2238619273e15506e6bb"},
	"stereo_s24":     {length: 105, digest: "95edcc40aaa449eeea055a347e593976a229f90a3a46a416335f4e7b4294d494"},
}

// This test replaces SHA256SUMS while keeping independent byte-for-byte
// integrity checks for all seven immutable binary vectors and stereo PCM.
func TestFixedGoldenIntegrity(t *testing.T) {
	for name, want := range goldenBinarySHA256 {
		data, err := os.ReadFile(filepath.Join("testdata", name+".bin"))
		if err != nil {
			t.Fatal(err)
		}
		observed := sha256.Sum256(data)
		if got := hex.EncodeToString(observed[:]); got != want {
			t.Fatalf("%s: independent binary golden SHA-256 got %s, want %s", name, got, want)
		}
	}
	pcm, err := os.ReadFile("testdata/stereo_s24.pcm")
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(pcm)); got != "fb92925ff7da03671c55d4a544f53f23d6ea9ab2f9fdad333a97a6d9758a8d82" {
		t.Fatalf("stereo_s24 PCM changed: SHA-256 %s", got)
	}
}

// The encoder has no raw metadata fields and cannot append unknown sections.
func TestPCMOnlyNoExtraSection(t *testing.T) {
	d := vectors()["ref_absent"]
	got, err := Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 60 {
		t.Fatalf("unexpected descriptor length %d", len(got))
	}
	expect, err := os.ReadFile("testdata/ref_absent.bin")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, expect) {
		t.Fatal("exact PCM-only wire oracle mismatch")
	}
	// At 16 bytes the first buffer starts immediately: no extra 8-byte header.
	if got[16] != 0 || got[20] != 8 || got[24] != 0x80 || got[25] != 0x3e {
		t.Fatalf("16-byte boundary not PCM buffer record: %x", got[16:28])
	}
}
