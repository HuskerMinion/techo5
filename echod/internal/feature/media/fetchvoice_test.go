package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// wavHeader is a WAVE header whose data size says "still being written", as a streamed body's does.
func wavHeader(channels, rate int) []byte {
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(0xFFFFFFFF))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate*channels*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels*2))
	_ = binary.Write(&b, binary.LittleEndian, uint16(16))
	b.WriteString("LIST")
	_ = binary.Write(&b, binary.LittleEndian, uint32(3))
	b.WriteString("abc\x00") // an odd chunk, padded
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(0xFFFFFFFF))
	return b.Bytes()
}

// A reply still being written arrives in pieces, some cut mid-sample, and is handed on as it comes,
// before the body has ended.
func TestFetchVoicePlaysAsItArrives(t *testing.T) {
	first := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(wavHeader(1, 16000))
		_, _ = w.Write([]byte{1, 0, 2})
		w.(http.Flusher).Flush()
		<-first
		<-release
		_, _ = w.Write([]byte{0, 3, 0})
	}))
	defer srv.Close()

	var got []byte
	done := make(chan error, 1)
	go func() {
		n := 0
		done <- FetchVoice(context.Background(), srv.URL, func(b []byte) {
			got = append(got, b...)
			if n++; n == 1 {
				close(first)
			}
		})
	}()

	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("nothing handed on before the body ended")
	}
	if !bytes.Equal(got, []byte{1, 0}) {
		t.Fatalf("first piece = %v, want the one whole sample", got)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if want := []byte{1, 0, 2, 0, 3, 0}; !bytes.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

// Stereo at another rate comes out as mono at the voice rate.
func TestFetchVoiceConverts(t *testing.T) {
	body := wavHeader(2, 32000)
	for range 3200 { // 0.1 s
		body = append(body, 100, 0, 44, 1) // 100 and 300: mono 200
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()

	var got []byte
	if err := FetchVoice(context.Background(), srv.URL, func(b []byte) { got = append(got, b...) }); err != nil {
		t.Fatal(err)
	}
	if n := len(got) / 2; n < 1500 || n > 1700 {
		t.Fatalf("%d samples, want about 1600", n)
	}
	if v := int16(binary.LittleEndian.Uint16(got[200:])); v != 200 {
		t.Fatalf("sample = %d, want 200", v)
	}
}
