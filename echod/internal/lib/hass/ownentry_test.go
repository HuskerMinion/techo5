package hass

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestRemoveOwnEntryDeletesOnlyItsESPHomeEntry(t *testing.T) {
	var mu sync.Mutex
	var deleted []string
	var template string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/template":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			template = body["template"]
			mu.Unlock()
			w.Write([]byte("esp1,ma1\n"))
		case r.Method == "GET" && r.URL.Path == "/api/config/config_entries/entry":
			w.Write([]byte(`[{"entry_id":"esp1","domain":"esphome"},{"entry_id":"esp2","domain":"esphome"}]`))
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/api/config/config_entries/entry/"):
			mu.Lock()
			deleted = append(deleted, strings.TrimPrefix(r.URL.Path, "/api/config/config_entries/entry/"))
			mu.Unlock()
			w.Write([]byte(`{"require_restart":false}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := newClient(srv.URL, "tok")

	n, err := c.RemoveOwnEntry(context.Background(), "aa:bb:cc:dd:ee:01")
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if n != 1 || len(deleted) != 1 || deleted[0] != "esp1" {
		t.Errorf("deleted %v (%d); want only esp1, its ESPHome entry", deleted, n)
	}
	if !strings.Contains(template, "('mac', 'aa:bb:cc:dd:ee:01')") {
		t.Errorf("the template does not look for this address:\n%s", template)
	}
}

func TestRemoveOwnEntryTakesOnlyAnAddress(t *testing.T) {
	c := newClient("http://ha.invalid:8123", "tok")
	for _, mac := range []string{"", "aa:bb", "AA:BB:CC:DD:EE:01", "aa:bb:cc:dd:ee:01') }}{{ x"} {
		if _, err := c.RemoveOwnEntry(context.Background(), mac); err == nil {
			t.Errorf("%q was taken", mac)
		}
	}
}
