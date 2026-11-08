package streaming

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/gorilla/websocket"
)

func soloistTestHome(t *testing.T) string {
	t.Helper()
	old := soloistHome
	home := t.TempDir()
	soloistHome = func() string { return home }
	t.Cleanup(func() { soloistHome = old })
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	return home
}

func TestSoloistPrivateKeyPersistence(t *testing.T) {
	soloistTestHome(t)
	if SoloistKeyConfigured() {
		t.Fatal("new device has a key")
	}
	if err := SaveSoloistKey("test-key-not-a-real-credential", false); err != nil {
		t.Fatal(err)
	}
	if err := SaveSoloistKey("", false); err != nil {
		t.Fatal(err)
	}
	c, err := readSoloistConfig()
	if err != nil || c.APIKey != "test-key-not-a-real-credential" {
		t.Fatal("blank Save lost key")
	}
	st, err := os.Stat(soloistConfigPath())
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatal("key is not private")
	}
	exported, err := config.Export(config.Get(), "test", "test")
	if err != nil || bytes.Contains(exported, []byte(c.APIKey)) {
		t.Fatal("key leaked into settings export")
	}
	if err := SaveSoloistKey("bad\nkey", false); err == nil {
		t.Fatal("multiline key accepted")
	}
	if err := SaveSoloistKey("", true); err != nil || SoloistKeyConfigured() {
		t.Fatal("remove retained key")
	}
}

func TestSoloistMissingKeyStartsNoReceiver(t *testing.T) {
	soloistTestHome(t)
	oldRuntime, oldDaemon, oldExec := soloistRuntime, soloistdPath, soloistExec
	t.Cleanup(func() { soloistRuntime, soloistdPath, soloistExec = oldRuntime, oldDaemon, oldExec })
	soloistRuntime = t.TempDir()
	soloistdPath = filepath.Join(t.TempDir(), "soloistd")
	soloistExec = filepath.Join(t.TempDir(), "exec")
	libs, loader := soloistLibraries()
	for _, path := range []string{soloistdPath, soloistExec, filepath.Join(libs, loader), filepath.Join(soloistRuntime, "usr/bin/pulseaudio"), filepath.Join(soloistRuntime, "usr/bin/patchelf")} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	runSoloistReceiver(ctx, "test")
	if !strings.Contains(SoloistStatus(), "API key required") {
		t.Fatal("missing key did not gate startup")
	}
}

func TestSoloistDirectoryRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := privateSoloistDir(link); err == nil {
		t.Fatal("directory symlink accepted")
	}
	st, err := os.Stat(real)
	if err != nil || st.Mode().Perm() != 0o755 {
		t.Fatal("symlink target permissions changed")
	}
}

func TestSoloistPCMDiscardsIdleSilence(t *testing.T) {
	var active atomic.Bool
	var out bytes.Buffer
	data := make([]byte, 4)
	binary.LittleEndian.PutUint32(data, math.Float32bits(0.5))
	forwardSoloistFloatPCM(context.Background(), bytes.NewReader(data), &out, &active, newLevel())
	if out.Len() != 0 {
		t.Fatal("idle sink acquired speaker")
	}
	active.Store(true)
	forwardSoloistFloatPCM(context.Background(), bytes.NewReader(data), &out, &active, newLevel())
	if out.Len() != 2 || int16(binary.LittleEndian.Uint16(out.Bytes())) != 16384 {
		t.Fatal("playing PCM was lost")
	}
}

func TestSoloistFloatVolumeNormalizationPreservesQuietPrecision(t *testing.T) {
	var active atomic.Bool
	active.Store(true)
	for percent := 1; percent <= 100; percent++ {
		gain := soloistVolumeGain(percent)
		lv := newLevel()
		lv.now = gain
		data := make([]byte, 4)
		// Input fixture follows PulseAudio's documented cubic pa_volume_t scaling, independently
		// of the normalization function. A linear-volume or S16-first converter must fail here.
		pulseGain := math.Pow(float64(percent*65535/100)/65536, 3)
		binary.LittleEndian.PutUint32(data, math.Float32bits(float32(0.25*pulseGain)))
		var out bytes.Buffer
		forwardSoloistFloatPCM(context.Background(), bytes.NewReader(data), &out, &active, lv)
		if out.Len() != 2 {
			t.Fatal("missing converted frame")
		}
		sample := int16(binary.LittleEndian.Uint16(out.Bytes()))
		if sample < 8191 || sample > 8193 {
			t.Fatalf("volume %d: unscaled sample=%d", percent, sample)
		}
		step := spotifyStep(soloistVolumeValue(percent))
		if step < 0 || step > media.VolumeSteps {
			t.Fatal("volume exceeds device steps")
		}
	}
}

func TestSoloistPercentEchoPreservesEveryDeviceStep(t *testing.T) {
	for step := 0; step <= media.VolumeSteps; step++ {
		value := spotifyValue(step)
		percent := (value*100 + spotifyFull/2) / spotifyFull
		if soloistVolumeValue(percent) != value {
			t.Fatalf("step %d loses its echo after percent conversion", step)
		}
	}
}

func TestSoloistControlRequiresAcknowledgement(t *testing.T) {
	soloistTestHome(t)
	up := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_ = c.WriteJSON(map[string]any{"type": "auth_state", "logged_in": true})
		var command map[string]string
		if c.ReadJSON(&command) != nil {
			return
		}
		if command["command"] == "pause" {
			_ = c.WriteJSON(map[string]string{"type": "command_result", "command": "pause"})
		} else {
			_ = c.WriteJSON(map[string]string{"type": "error", "message": "rejected"})
		}
	}))
	defer server.Close()
	if err := os.MkdirAll(soloistData(), 0o700); err != nil {
		t.Fatal(err)
	}
	port := strings.Split(server.Listener.Addr().String(), ":")[1]
	if err := os.WriteFile(filepath.Join(soloistData(), "ws.port"), []byte(port), 0o600); err != nil {
		t.Fatal(err)
	}
	if !sendSoloistCommand("pause") {
		t.Fatal("accepted pause failed")
	}
	if sendSoloistCommand("play") {
		t.Fatal("rejected command reported success")
	}
}

func TestSoloistMessagesDoNotExposeCredentials(t *testing.T) {
	w := &soloistMessages{}
	_, _ = w.Write([]byte("API key rejected: secret-which-must-not-appear\n"))
	if strings.Contains(SoloistStatus(), "secret-") {
		t.Fatal("secret in UI status")
	}
	if !strings.Contains(SoloistStatus(), "rejected") {
		t.Fatal("missing auth guidance")
	}
	var event soloistEvent
	if err := json.Unmarshal([]byte(`{"type":"auth_state","logged_in":true}`), &event); err != nil || !event.LoggedIn {
		t.Fatal("auth event not understood")
	}
}

func TestSoloistOnlyOpensItsOwnConnectListener(t *testing.T) {
	proc := t.TempDir()
	fd := filepath.Join(proc, "123/fd")
	if err := os.MkdirAll(fd, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(proc, "net"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[321]", filepath.Join(fd, "1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[322]", filepath.Join(fd, "2")); err != nil {
		t.Fatal(err)
	}
	rows := "0: 00000000:D59F 00000000:0000 0A 0:0 0:0 0 88 0 321\n" +
		"1: 0100007F:8801 00000000:0000 0A 0:0 0:0 0 88 0 322\n" +
		"2: 00000000:0016 00000000:0000 0A 0:0 0:0 0 0 0 333\n"
	if err := os.WriteFile(filepath.Join(proc, "net/tcp"), []byte(rows), 0o600); err != nil {
		t.Fatal(err)
	}
	ports := soloistListeningPorts(123, proc)
	if len(ports) != 1 || ports[0] != 54687 {
		t.Fatalf("unexpected allowed ports: %v", ports)
	}
}
