package musicassistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ItemByURI asks for the item by its URI and reads the services it is mapped to, with whether each can
// be played; a mapping that does not say is taken as playable.
func TestItemByURI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var c struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		_ = json.Unmarshal(body, &c)
		if c.Command != "music/item_by_uri" || c.Args["uri"] != "library://track/22" {
			t.Errorf("asked %q with %v", c.Command, c.Args)
		}
		io.WriteString(w, `{"item_id":"22","provider":"library","name":"Song","uri":"library://track/22","media_type":"track",
			"provider_mappings":[
				{"item_id":"a","provider_domain":"spotify","provider_instance":"spotify--x","available":false},
				{"item_id":"b","provider_domain":"ytmusic","provider_instance":"ytmusic--y","available":true},
				{"item_id":"c","provider_domain":"filesystem_local","provider_instance":"filesystem_local--z"}]}`)
	}))
	defer srv.Close()
	it, err := Client{URL: srv.URL, Token: "t"}.ItemByURI(context.Background(), "library://track/22")
	if err != nil {
		t.Fatal(err)
	}
	if len(it.Mappings) != 3 {
		t.Fatalf("mappings: %+v", it.Mappings)
	}
	for i, want := range []bool{false, true, true} {
		if got := it.Mappings[i].Playable(); got != want {
			t.Errorf("mapping %d (%s) playable = %v, want %v", i, it.Mappings[i].Domain, got, want)
		}
	}
	if it.Mappings[1].Domain != "ytmusic" || it.Mappings[1].Instance != "ytmusic--y" {
		t.Errorf("mapping 1: %+v", it.Mappings[1])
	}
}
