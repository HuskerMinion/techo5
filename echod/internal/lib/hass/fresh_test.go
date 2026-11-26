package hass

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

// conditional answers 304 to a request that asks with If-None-Match "v1" and the picture otherwise,
// with its caching headers, keeping the last request's headers.
type conditional struct{ last http.Header }

func (c *conditional) RoundTrip(req *http.Request) (*http.Response, error) {
	c.last = req.Header.Clone()
	h := http.Header{"Cache-Control": {"no-cache"}, "Etag": {`"v1"`}, "Last-Modified": {"Mon, 02 Jan 2026 15:04:05 GMT"}}
	code, status, body := http.StatusOK, "200 OK", "picture"
	if req.Header.Get("If-None-Match") == `"v1"` {
		code, status, body = http.StatusNotModified, "304 Not Modified", ""
	}
	return &http.Response{StatusCode: code, Status: status, Header: h, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
}

// FetchFresh says what the server said about keeping a picture, asks with the validators it is given,
// and takes a 304 as the picture unchanged: on Home Assistant with the token, and on another host
// without it.
func TestFetchFreshAsksWhetherAPictureChanged(t *testing.T) {
	for _, url := range []string{"/local/a.png", "https://photos.example/a.png"} {
		rt := &conditional{}
		c := &Client{acc: access{URL: "http://ha:8123", Token: "secret"}, http: &http.Client{Transport: rt}}
		f, err := c.FetchFresh(context.Background(), url, "", "")
		if err != nil || string(f.Body) != "picture" || f.NotModified || f.ETag != `"v1"` ||
			f.CacheControl != "no-cache" || f.LastModified == "" {
			t.Fatalf("%s: first fetch %+v, %v", url, f, err)
		}
		if rt.last.Get("If-None-Match") != "" || rt.last.Get("If-Modified-Since") != "" {
			t.Errorf("%s: a first fetch asked with validators: %v", url, rt.last)
		}
		f, err = c.FetchFresh(context.Background(), url, f.ETag, f.LastModified)
		if err != nil || !f.NotModified || len(f.Body) != 0 {
			t.Fatalf("%s: asked again %+v, %v", url, f, err)
		}
		if rt.last.Get("If-None-Match") != `"v1"` || rt.last.Get("If-Modified-Since") == "" {
			t.Errorf("%s: asked without its validators: %v", url, rt.last)
		}
		if own := strings.HasPrefix(url, "/"); own != (rt.last.Get("Authorization") == "Bearer secret") {
			t.Errorf("%s: token %q", url, rt.last.Get("Authorization"))
		}
	}
}
