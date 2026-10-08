package update

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// gone checks that nothing of a refused download is left, neither where it would be installed from
// nor beside it.
func gone(t *testing.T, to string, b Binary) {
	t.Helper()
	if _, err := os.Stat(to); err == nil {
		t.Error("a refused download was left where it would be installed from")
	}
	if _, err := os.Stat(partPath(to, b)); err == nil {
		t.Error("a refused download's partial file was kept")
	}
}

// A 206 is allowed to cover less than was asked for. It ends cleanly, short, and the next try carries
// on from there rather than throwing away what arrived.
func TestAShortRangeIsCarriedOnFrom(t *testing.T) {
	body := payload()
	half := len(body) / 2
	a := &asset{body: body, before: func(w http.ResponseWriter, _ *http.Request, n int) bool {
		switch n {
		case 0:
			cut(w, body, half)
		case 1:
			end := half + 1000
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", half, end-1, len(body)))
			w.Header().Set("Content-Length", strconv.Itoa(end-half))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(body[half:end])
			return true
		}
		return false
	}}
	b := offered(serve(t, a)+"/echod", body)
	to := filepath.Join(t.TempDir(), "echod")
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
	want := []string{"", "bytes=" + strconv.Itoa(half) + "-", "bytes=" + strconv.Itoa(half+1000) + "-"}
	if !slices.Equal(a.asked(), want) {
		t.Errorf("asked for %q, want %q", a.asked(), want)
	}
}

// A body with no length that the server closes partway (an old proxy) ends cleanly too, and is
// carried on from in the same way.
func TestABodyClosedCleanlyPartwayIsCarriedOnFrom(t *testing.T) {
	body := payload()
	half := len(body) / 2
	a := &asset{body: body, before: func(w http.ResponseWriter, _ *http.Request, n int) bool {
		if n > 0 {
			return false
		}
		c, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return true
		}
		defer c.Close()
		_, _ = rw.WriteString("HTTP/1.1 200 OK\r\nConnection: close\r\n\r\n")
		_, _ = rw.Write(body[:half])
		_ = rw.Flush()
		return true
	}}
	b := offered(serve(t, a)+"/echod", body)
	to := filepath.Join(t.TempDir(), "echod")
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
	if want := []string{"", "bytes=" + strconv.Itoa(half) + "-"}; !slices.Equal(a.asked(), want) {
		t.Errorf("asked for %q, want %q", a.asked(), want)
	}
}

// A whole, correct partial file, left by a crash before the rename, is used without asking again.
func TestAWholePartialIsUsedWithoutAsking(t *testing.T) {
	body := payload()
	a := &asset{body: body}
	b := offered(serve(t, a)+"/echod", body)
	to := filepath.Join(t.TempDir(), "echod")
	if err := os.WriteFile(partPath(to, b), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
	if got := a.asked(); len(got) != 0 {
		t.Errorf("asked for %q with the whole file already here", got)
	}
}

// A whole partial file of the wrong bytes is fetched again from nothing, and not installed.
func TestAWholePartialOfTheWrongBytesIsFetchedAgain(t *testing.T) {
	body := payload()
	a := &asset{body: body}
	b := offered(serve(t, a)+"/echod", body)
	to := filepath.Join(t.TempDir(), "echod")
	if err := os.WriteFile(partPath(to, b), make([]byte, len(body)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
	if want := []string{""}; !slices.Equal(a.asked(), want) {
		t.Errorf("asked for %q, want %q", a.asked(), want)
	}
}

// A resume whose body runs past the end of the file is the wrong file, and nothing of it is kept.
func TestAResumeThatRunsPastTheEndIsRefused(t *testing.T) {
	body := payload()
	half := len(body) / 2
	a := &asset{body: body, before: func(w http.ResponseWriter, r *http.Request, _ int) bool {
		if r.Header.Get("Range") == "" {
			return false
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/*", half, len(body)-1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body[half:])
		_, _ = w.Write(make([]byte, 64<<10))
		return true
	}}
	b := offered(serve(t, a)+"/echod", body)
	to := filepath.Join(t.TempDir(), "echod")
	if err := os.WriteFile(partPath(to, b), body[:half], 0o644); err != nil {
		t.Fatal(err)
	}
	err := download(context.Background(), b, to, nil)
	if err == nil || !strings.Contains(err.Error(), "offered as") {
		t.Fatalf("%v, want a refusal for the length", err)
	}
	gone(t, to, b)
}

// A server that sends a tampered tail on every resume never gets a file installed, and what it sent
// is not kept.
func TestATamperedTailNeverLands(t *testing.T) {
	body := payload()
	bad := append([]byte(nil), body...)
	bad[len(bad)-1] ^= 0xff
	a := &asset{body: bad, before: func(w http.ResponseWriter, _ *http.Request, n int) bool {
		if n == 0 {
			cut(w, body, len(body)/2)
		}
		return false
	}}
	b := offered(serve(t, a)+"/echod", body)
	to := filepath.Join(t.TempDir(), "echod")
	if err := download(context.Background(), b, to, nil); err == nil {
		t.Fatal("a tampered file was accepted")
	}
	gone(t, to, b)
}

// A server that cuts every connection and refuses every resume gets one plain fetch from the
// beginning after badResumes refusals, and then no more: not a loop until downloadCap.
func TestAServerThatWillNotResumeIsNotLoopedOn(t *testing.T) {
	for _, refuse := range []string{"416", "a 206 for another part"} {
		t.Run(refuse, func(t *testing.T) {
			body := payload()
			a := &asset{body: body, before: func(w http.ResponseWriter, r *http.Request, _ int) bool {
				if r.Header.Get("Range") == "" {
					cut(w, body, len(body)/2)
				}
				if refuse == "416" {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", len(body)))
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					return true
				}
				w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-99/%d", len(body)))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(body[:100])
				return true
			}}
			b := offered(serve(t, a)+"/echod", body)
			to := filepath.Join(t.TempDir(), "echod")
			if err := download(context.Background(), b, to, nil); err == nil {
				t.Fatal("a download that never completed succeeded")
			}
			// cut, refused, cut, refused, and the one plain fetch, cut.
			if got := len(a.asked()); got != 2*badResumes+1 {
				t.Errorf("asked %d times (%q), want %d", got, a.asked(), 2*badResumes+1)
			}
			gone(t, to, b)
		})
	}
}

// The device's own storage failing (full, or past the file size limit here) is not a link to try
// again over: the partial file is removed and the download ends on the first try.
func TestAWriteFailureIsFinal(t *testing.T) {
	if os.Getenv("UPDATE_TEST_FSIZE") == "" {
		if runtime.GOOS != "linux" {
			t.Skip("needs ulimit -f")
		}
		cmd := exec.Command("sh", "-c", `ulimit -f 100 && exec "$0" -test.run '^TestAWriteFailureIsFinal$' -test.v`, os.Args[0])
		cmd.Env = append(os.Environ(), "UPDATE_TEST_FSIZE=1")
		out, err := cmd.CombinedOutput()
		if err != nil || !bytes.Contains(out, []byte("--- PASS")) {
			t.Fatalf("%v\n%s", err, out)
		}
		return
	}

	body := bytes.Repeat(payload(), 2) // 512 kB, past the limit whichever block size ulimit counts in
	a := &asset{body: body}
	b := offered(serve(t, a)+"/echod", body)
	to := filepath.Join(t.TempDir(), "echod")
	err := download(context.Background(), b, to, nil)
	if err == nil || !strings.Contains(err.Error(), "writing") {
		t.Fatalf("%v, want a write failure", err)
	}
	if got := len(a.asked()); got != 1 {
		t.Errorf("asked %d times after the storage failed", got)
	}
	gone(t, to, b)
}

// On a slot device the rootfs directory is cleared of other releases before its room is measured,
// and a whole tarball of this release left by a crash during slotctl is checked and used again.
func TestTheRootfsDirectoryIsSwept(t *testing.T) {
	dir := t.TempDir()
	body := payload()
	a := &asset{body: body}
	b := offered(serve(t, a)+"/rootfs", body)
	to := filepath.Join(dir, "techo5-rootfs-v1.0.3.tar.gz")

	others := []string{
		filepath.Join(dir, "techo5-rootfs-v1.0.2.tar.gz"),
		filepath.Join(dir, "techo5-rootfs-v1.0.2.tar.gz.0123456789abcdef.part"),
	}
	for _, p := range append(others, to) {
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	keep := filepath.Join(dir, "amd-trace.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	sweepRootfs(to, b)
	for _, p := range others {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was kept", filepath.Base(p))
		}
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error("the sweep removed a file that is not a rootfs")
	}
	if got := partial(to, b); got != b.Size {
		t.Fatalf("this release's tarball was not kept to check: %d bytes", got)
	}
	if err := download(context.Background(), b, to, nil); err != nil {
		t.Fatal(err)
	}
	arrived(t, to, body, b)
	if got := a.asked(); len(got) != 0 {
		t.Errorf("asked for %q with the whole tarball already here", got)
	}
}

// An https asset is not followed onto plain http.
func TestARedirectToPlainHTTPIsRefused(t *testing.T) {
	req := func(u string) *http.Request {
		p, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		return &http.Request{URL: p, Header: http.Header{}}
	}
	from := req("https://example.com/echod")
	if err := client.CheckRedirect(req("http://example.net/echod"), []*http.Request{from}); err == nil {
		t.Error("followed an https asset onto plain http")
	}
	if err := client.CheckRedirect(req("https://example.net/echod"), []*http.Request{from}); err != nil {
		t.Errorf("refused an https redirect: %v", err)
	}
}

// A hash that is not lowercase hex could never match, and is not let near a file name.
func TestAHashThatIsNotLowercaseHexIsRefused(t *testing.T) {
	good := strings.Repeat("ab", 32)
	for name, h := range map[string]string{
		"upper case": strings.ToUpper(good),
		"a path":     "../../" + good[6:],
		"not hex":    strings.Repeat("g", 64),
	} {
		b := Binary{URL: "https://example.com/echod", SHA256: h, Size: 1}
		if err := b.valid("v1.0.0", arch); err == nil {
			t.Errorf("%s: %q accepted", name, h)
		}
	}
	if err := (Binary{URL: "https://example.com/echod", SHA256: good, Size: 1}).valid("v1.0.0", arch); err != nil {
		t.Errorf("a good hash was refused: %v", err)
	}
}
