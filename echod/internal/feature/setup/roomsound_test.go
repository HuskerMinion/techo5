package setup

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/presence"
	"github.com/HuskerMinion/techo5/echod/internal/feature/sound"
)

func TestRoomSoundSaveAndPasswordHandling(t *testing.T) {
	wasDir := sound.ModelDir
	sound.ModelDir = t.TempDir()
	t.Cleanup(func() { sound.ModelDir = wasDir })

	wasCam := presence.Available
	presence.Available = func() bool { return true }
	t.Cleanup(func() { presence.Available = wasCam })

	f, c := in(t)

	// 1. Save initial settings + MQTT password
	to := post(t, f, c, url.Values{
		"what":           {"roomsound"},
		"tab":            {"sound"},
		"min_confidence": {"35"},
		"cooldown":       {"10"},
		"alert_dog":      {"yes"},
		"alert_doorbell": {"yes"},
		"alert_person":   {"yes"},
		"alarm_siren":    {"yes"},
		"cam_person":     {"yes"},
		"ha_service":     {"persistent_notification.create"},
		"mqtt_broker":    {"192.0.2.10:1883"},
		"mqtt_user":      {"home"},
		"mqtt_pass":      {"super-secret-mqtt-pass"},
		"mqtt_topic":     {"home/kitchen/sound"},
		"mqtt_discovery": {"yes"},
	})
	if p := to.Query().Get("problem"); p != "" {
		t.Fatalf("unexpected save problem: %s", p)
	}

	sc := config.Get().Microphone.Sound
	if sc.MinConfidence != 35 || sc.Cooldown != 10 || !sc.AlarmSiren || sc.HAService != "persistent_notification.create" ||
		sc.MQTTBroker != "192.0.2.10:1883" || sc.MQTTUser != "home" || sc.MQTTPass != "super-secret-mqtt-pass" ||
		sc.MQTTTopic != "home/kitchen/sound" || !sc.MQTTDiscovery {
		t.Fatalf("unexpected saved RoomSoundConfig: %+v", sc)
	}
	if !config.Get().Presence.On || !config.Get().Presence.Person {
		t.Fatalf("expected cam_person to enable both Presence.On and Presence.Person: %+v", config.Get().Presence)
	}
	if !sound.AlertEnabled(sc, "dog") || !sound.AlertEnabled(sc, "doorbell") || !sound.AlertEnabled(sc, "person") || sound.AlertEnabled(sc, "cat") {
		t.Fatalf("unexpected saved alerts: %+v", sc.Alerts)
	}

	// 2. Verify the password is never rendered on the setup page, while the "already set" hint is shown
	body := get(f, "/setup?tab=sound", c).Body.String()
	if strings.Contains(body, "super-secret-mqtt-pass") {
		t.Fatal("setup page leaked the MQTT password")
	}
	if !strings.Contains(body, "192.0.2.10:1883") || !strings.Contains(body, "persistent_notification.create") {
		t.Fatal("setup page did not render saved broker and HA service")
	}

	// 3. Saving again with empty password keeps the existing password
	to = post(t, f, c, url.Values{
		"what":           {"roomsound"},
		"tab":            {"sound"},
		"min_confidence": {"40"},
		"cooldown":       {"8"},
		"mqtt_broker":    {"192.0.2.10:1883"},
		"mqtt_user":      {"home"},
		"mqtt_pass":      {""},
	})
	if p := to.Query().Get("problem"); p != "" {
		t.Fatalf("unexpected save problem: %s", p)
	}
	if config.Get().Microphone.Sound.MQTTPass != "super-secret-mqtt-pass" {
		t.Fatal("saving with blank mqtt_pass overwrote the existing password")
	}

	// 4. Ticking nomqttpass clears the password
	to = post(t, f, c, url.Values{
		"what":           {"roomsound"},
		"tab":            {"sound"},
		"min_confidence": {"40"},
		"cooldown":       {"8"},
		"mqtt_broker":    {"192.0.2.10:1883"},
		"nomqttpass":     {"yes"},
	})
	if p := to.Query().Get("problem"); p != "" {
		t.Fatalf("unexpected save problem: %s", p)
	}
	if config.Get().Microphone.Sound.MQTTPass != "" {
		t.Fatal("nomqttpass did not clear the MQTT password")
	}
}

func TestRoomSoundEndpointsRequireSessionAndToken(t *testing.T) {
	wasDir := sound.ModelDir
	sound.ModelDir = t.TempDir()
	t.Cleanup(func() { sound.ModelDir = wasDir })

	f, c := in(t)
	send := func(r *http.Request) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		f.serve(w, r)
		return w
	}

	// Unauthenticated GET /setup/roomsound must be 403
	if w := send(httptest.NewRequest(http.MethodGet, "/setup/roomsound", nil)); w.Code != http.StatusForbidden {
		t.Fatalf("GET /setup/roomsound without cookie = %d, want 403", w.Code)
	}
	// Authenticated GET /setup/roomsound returns JSON status
	req := httptest.NewRequest(http.MethodGet, "/setup/roomsound", nil)
	req.AddCookie(c)
	if w := send(req); w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("GET /setup/roomsound with cookie = %d %v", w.Code, w.Header())
	}

	// Model upload requires both cookie and matching token
	files := map[string]bool{"file": true}
	up := func(token, body string, ck *http.Cookie) *http.Request {
		var fields [][2]string
		if token != "" {
			fields = append(fields, [2]string{"token", token})
		}
		fields = append(fields, [2]string{"file", body})
		return multipartPost("/setup/roomsound/model", fields, files, "yamnet.tflite", ck)
	}
	if w := send(up(c.Value, "not a model", nil)); w.Code != http.StatusForbidden {
		t.Fatalf("upload without cookie = %d, want 403", w.Code)
	}
	if w := send(up("wrong-token", "not a model", c)); w.Code != http.StatusForbidden {
		t.Fatalf("upload with wrong token = %d, want 403", w.Code)
	}
	if w := send(up(c.Value, "not a model", c)); !refused(w, "YAMNet") {
		t.Fatalf("upload of invalid model was not refused: %d %s", w.Code, w.Header().Get("Location"))
	}

	// Verify roomsound-action test-ha and remove actions
	to := post(t, f, c, url.Values{
		"what":   {"roomsound-action"},
		"tab":    {"sound"},
		"action": {"remove"},
	})
	if p := to.Query().Get("problem"); p != "" {
		t.Fatalf("unexpected remove model problem: %s", p)
	}
	to = post(t, f, c, url.Values{
		"what":   {"roomsound-action"},
		"tab":    {"sound"},
		"action": {"unknown"},
	})
	if p := to.Query().Get("problem"); !strings.Contains(p, "unknown room sound action") {
		t.Fatalf("expected unknown action problem, got %q", p)
	}
}
