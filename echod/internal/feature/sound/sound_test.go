package sound

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

func TestDot4I8Exact(t *testing.T) {
	for _, n := range []int{0, 1, 2, 3, 4, 7, 8, 15, 16, 64, 257} {
		in := make([]int8, n)
		w0 := make([]int8, n)
		w1 := make([]int8, n)
		w2 := make([]int8, n)
		w3 := make([]int8, n)
		want := [4]int32{101, -202, 303, -404}
		got := want

		for i := 0; i < n; i++ {
			in[i] = int8(((i*37 + 13) % 256) - 128)
			w0[i] = int8(((i*17 + 255) % 256) - 128)
			w1[i] = int8(((i*53 + 7) % 256) - 128)
			w2[i] = int8(-128 + (i % 256))
			w3[i] = int8(127 - (i % 256))
			iv := int32(in[i])
			want[0] += iv * int32(w0[i])
			want[1] += iv * int32(w1[i])
			want[2] += iv * int32(w2[i])
			want[3] += iv * int32(w3[i])
		}
		dot4I8(in, w0, w1, w2, w3, &got)
		if got != want {
			t.Fatalf("len=%d: dot4I8 = %v, want %v", n, got, want)
		}
	}
}

func TestClassifyActionableAndAmbient(t *testing.T) {
	cases := []struct {
		name       string
		top        []TopClass
		minConf    float64
		wantCat    string
		wantLabel  string
		actionable bool
	}{
		{
			name: "clapping siblings combine",
			top: []TopClass{
				{ID: 0, Name: "Speech", Score: 0.25},
				{ID: 56, Name: "Hands", Score: 0.22},
				{ID: 58, Name: "Clapping", Score: 0.21},
			},
			minConf:    0.25,
			wantCat:    "clapping",
			wantLabel:  "Clapping",
			actionable: true,
		},
		{
			name: "raised minConfidence suppresses weak detection",
			top: []TopClass{
				{ID: 69, Name: "Dog", Score: 0.34},
			},
			minConf:    0.55,
			wantCat:    "quiet",
			wantLabel:  "Quiet",
			actionable: false,
		},
		{
			name: "dog bark beats ambient noise",
			top: []TopClass{
				{ID: 69, Name: "Dog", Score: 0.34},
				{ID: 70, Name: "Bark", Score: 0.29},
			},
			minConf:    0.25,
			wantCat:    "dog",
			wantLabel:  "Dog",
			actionable: true,
		},
		{
			name: "smoke detector priority",
			top: []TopClass{
				{ID: 393, Name: "Smoke detector, smoke alarm", Score: 0.41},
				{ID: 382, Name: "Alarm", Score: 0.38},
			},
			minConf:    0.25,
			wantCat:    "smoke_alarm",
			wantLabel:  "Smoke / fire alarm",
			actionable: true,
		},
		{
			name: "ordinary speech is not actionable",
			top: []TopClass{
				{ID: 0, Name: "Speech", Score: 0.91},
				{ID: 5, Name: "Speech synthesizer", Score: 0.08},
			},
			minConf:    0.25,
			wantCat:    "speech",
			wantLabel:  "Speech",
			actionable: false,
		},
		{
			name: "silence maps to quiet",
			top: []TopClass{
				{ID: 494, Name: "Silence", Score: 0.88},
				{ID: 500, Name: "Inside, small room", Score: 0.09},
			},
			minConf:    0.25,
			wantCat:    "quiet",
			wantLabel:  "Quiet",
			actionable: false,
		},
		{
			name: "other strong audioset class surfaces directly",
			top: []TopClass{
				{ID: 362, Name: "Microwave oven", Score: 0.52},
			},
			minConf:    0.25,
			wantCat:    "microwave_oven",
			wantLabel:  "Microwave oven",
			actionable: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyWithMin(tc.top, tc.minConf)
			if got.Category != tc.wantCat || got.Label != tc.wantLabel || got.Actionable != tc.actionable {
				t.Fatalf("ClassifyWithMin() = %+v, want cat=%q label=%q actionable=%v",
					got, tc.wantCat, tc.wantLabel, tc.actionable)
			}
		})
	}
}

func TestOffByDefault(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	wasDir := ModelDir
	ModelDir = t.TempDir()
	t.Cleanup(func() { ModelDir = wasDir })

	s := build()
	s.Restore(config.Defaults())
	if s.isOn() {
		t.Fatal("room sound classifier must be off by default")
	}
	st := s.ReadStatus(0)
	if st.Enabled || st.ModelInstalled || st.Label != "Off" {
		t.Fatalf("unexpected default status: %+v", st)
	}
}

func TestListenGatesQuietAndFiresSelectedAlerts(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	var fired []Detection
	calls := 0

	s := build()
	s.busy = func() bool { return false }
	s.muted = func() bool { return false }
	s.score = func(_ context.Context, _ []int16) (EngineReply, error) {
		calls++
		return EngineReply{
			MS:      32.5,
			RMSDBFS: -24.0,
			Top:     []TopClass{{ID: 69, Name: "Dog", Score: 0.78}},
		}, nil
	}
	s.fire = func(det Detection, _ EngineReply) {
		fired = append(fired, det)
	}
	s.set(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	frames := make(chan []int16, 8)
	done := make(chan struct{})
	go func() {
		s.listen(ctx, frames)
		close(done)
	}()

	// 1. Quiet frames (~ -84 dBFS) should NOT invoke the .tflite engine.
	quiet := make([]int16, stepSamples)
	for i := range quiet {
		quiet[i] = 2
	}
	frames <- quiet
	frames <- quiet
	frames <- quiet
	time.Sleep(20 * time.Millisecond)
	if calls != 0 {
		t.Fatalf("expected 0 engine calls on quiet floor, got %d", calls)
	}

	// 2. Active sound (~ -18 dBFS) SHOULD invoke the engine and fire once within cooldown.
	loud := make([]int16, stepSamples)
	for i := range loud {
		loud[i] = int16(4000 * math.Sin(2*math.Pi*1000*float64(i)/16000))
	}
	frames <- loud
	frames <- loud // second hop within cooldown should not double-fire
	close(frames)
	<-done

	if calls != 2 {
		t.Fatalf("expected 2 engine calls on active hops, got %d", calls)
	}
	if len(fired) != 1 || fired[0].Category != "dog" {
		t.Fatalf("expected 1 dog alert, got %+v", fired)
	}
	st := s.ReadStatus(0)
	if len(st.Events) == 0 || st.Events[0].Category != "dog" || !st.Events[0].Alert {
		t.Fatalf("expected recorded dog alert event in status, got %+v", st.Events)
	}
}

func TestCameraPersonAndMotionAlertsAndCooldown(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "state.json"))
	// Enable "person" (on by default) and keep "motion" off by default.
	var fired []Detection
	s := build()
	s.fire = func(det Detection, _ EngineReply) {
		fired = append(fired, det)
	}

	// 1. Person detection (enabled by default in DefaultAlertCategories) fires once and respects cooldown.
	s.handleCameraDetection(Detection{
		Category:   "person",
		Label:      "Person seen",
		RawClass:   "MobileNetV1 96x96 person",
		Confidence: 0.86,
		Actionable: true,
	})
	s.handleCameraDetection(Detection{
		Category:   "person",
		Label:      "Person seen",
		RawClass:   "MobileNetV1 96x96 person",
		Confidence: 0.91,
		Actionable: true,
	})
	if len(fired) != 1 || fired[0].Category != "person" {
		t.Fatalf("expected 1 person alert within cooldown, got %+v", fired)
	}

	// 2. Motion detection is off by default in DefaultAlertCategories, so it records an ambient event without firing an alert.
	s.handleCameraDetection(Detection{
		Category:   "motion",
		Label:      "Motion seen",
		RawClass:   "Camera 32x24 motion",
		Confidence: 1.0,
		Actionable: true,
	})
	if len(fired) != 1 {
		t.Fatalf("expected motion alert to stay suppressed by default, got %+v", fired)
	}

	// 3. Verify ReadStatus(after) incremental event cursor.
	st0 := s.ReadStatus(0)
	if len(st0.Events) != 2 || !st0.Events[0].Alert || st0.Events[1].Alert {
		t.Fatalf("unexpected events in ReadStatus(0): %+v", st0.Events)
	}
	st1 := s.ReadStatus(st0.Events[0].Seq)
	if len(st1.Events) != 1 || st1.Events[0].Category != "motion" {
		t.Fatalf("unexpected incremental events in ReadStatus(%d): %+v", st0.Events[0].Seq, st1.Events)
	}
}

func TestHANotificationAndMQTTPublish(t *testing.T) {
	// 1. Verify Home Assistant CallService emission
	var gotCalls []component.Call
	stop := component.CallService.Listen(func(c component.Call) {
		gotCalls = append(gotCalls, c)
	})
	defer stop()

	det := Detection{Category: "baby_cry", Label: "Baby cry", RawClass: "Baby cry, infant cry", Confidence: 0.84, Actionable: true}
	rep := EngineReply{MS: 61.2, RMSDBFS: -22.4}

	if err := SendHANotification("badservice", "Bedroom Show", det, rep); err == nil {
		t.Fatal("expected error for malformed HA service")
	}
	if err := SendHANotification("persistent_notification.create", "Bedroom Show", det, rep); err != nil {
		t.Fatalf("SendHANotification failed: %v", err)
	}
	if len(gotCalls) != 1 || gotCalls[0].Service != "persistent_notification.create" ||
		!strings.Contains(gotCalls[0].Data["title"], "Bedroom Show heard Baby cry") {
		t.Fatalf("unexpected HA service call: %+v", gotCalls)
	}

	// Verify camera person detection uses "saw" and omits dBFS when RMSDBFS == 0
	camDet := Detection{Category: "person", Label: "Person seen", RawClass: "MobileNetV1 96x96 person", Confidence: 0.87, Actionable: true}
	if err := SendHANotification("persistent_notification.create", "Bedroom Show", camDet, EngineReply{}); err != nil {
		t.Fatalf("SendHANotification(person) failed: %v", err)
	}
	if len(gotCalls) != 2 || !strings.Contains(gotCalls[1].Data["title"], "Bedroom Show saw Person seen") ||
		strings.Contains(gotCalls[1].Data["message"], "dBFS") {
		t.Fatalf("unexpected camera HA service call: %+v", gotCalls[1])
	}

	// 2. Verify MQTT 3.1.1 wire protocol against an in-process TCP broker
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	type rxPub struct {
		topic   string
		payload string
		retain  bool
	}
	pubCh := make(chan []rxPub, 1)
	errCh := make(chan error, 1)

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			errCh <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))

		readPkt := func() (byte, []byte, error) {
			var h [1]byte
			if _, err := io.ReadFull(conn, h[:]); err != nil {
				return 0, nil, err
			}
			mul, rem := 1, 0
			for {
				var b [1]byte
				if _, err := io.ReadFull(conn, b[:]); err != nil {
					return 0, nil, err
				}
				rem += int(b[0]&0x7f) * mul
				if b[0]&0x80 == 0 {
					break
				}
				mul *= 128
			}
			body := make([]byte, rem)
			if _, err := io.ReadFull(conn, body); err != nil {
				return 0, nil, err
			}
			return h[0], body, nil
		}

		hdr, body, err := readPkt()
		if err != nil {
			errCh <- err
			return
		}
		if hdr != 0x10 || !bytes.Contains(body, []byte("mqttuser")) || !bytes.Contains(body, []byte("mqttpass")) {
			errCh <- io.ErrUnexpectedEOF
			return
		}
		// Send CONNACK (0x20, len=2, flags=0, rc=0)
		_, _ = conn.Write([]byte{0x20, 0x02, 0x00, 0x00})

		var pubs []rxPub
		for {
			hdr, body, err := readPkt()
			if err != nil {
				errCh <- err
				return
			}
			if hdr == 0xe0 {
				break
			}
			if hdr&0xf0 == 0x30 {
				tlen := int(body[0])<<8 | int(body[1])
				pubs = append(pubs, rxPub{
					topic:   string(body[2 : 2+tlen]),
					payload: string(body[2+tlen:]),
					retain:  hdr&0x01 != 0,
				})
			}
		}
		pubCh <- pubs
	}()

	sc := config.RoomSoundConfig{
		MQTTBroker:    ln.Addr().String(),
		MQTTUser:      "mqttuser",
		MQTTPass:      "mqttpass",
		MQTTDiscovery: true,
	}
	if err := PublishMQTT(nil, sc, "Bedroom Show", det, rep, true); err != nil {
		t.Fatalf("PublishMQTT failed: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("mock broker error: %v", err)
	case pubs := <-pubCh:
		if len(pubs) != 3 {
			t.Fatalf("expected 3 MQTT publishes (discovery + state + event), got %d: %+v", len(pubs), pubs)
		}
		if pubs[0].topic != "homeassistant/sensor/bedroom_show_room_sound/config" || !pubs[0].retain {
			t.Errorf("unexpected discovery publish: %+v", pubs[0])
		}
		if pubs[1].topic != "techo5/bedroom_show/sound/state" || pubs[1].payload != "Baby cry" || !pubs[1].retain {
			t.Errorf("unexpected state publish: %+v", pubs[1])
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(pubs[2].payload), &ev); err != nil || ev["category"] != "baby_cry" {
			t.Errorf("unexpected event publish: %+v (err=%v)", pubs[2], err)
		}
	}
}

func TestInProcessYAMNetEngineRealModel(t *testing.T) {
	candidates := []string{
		os.Getenv("YAMNET_TFLITE"),
		filepath.Join("..", "..", "..", "..", "sound-ai", "yamnet.tflite"),
	}
	var modelPath string
	for _, p := range candidates {
		if p != "" {
			if _, err := os.Stat(p); err == nil {
				modelPath = p
				break
			}
		}
	}
	if modelPath == "" {
		t.Skip("local yamnet.tflite not present; skipping real model accuracy check")
	}

	eng, err := LoadEngine(modelPath)
	if err != nil {
		t.Fatalf("LoadEngine(%s): %v", modelPath, err)
	}
	defer eng.Close()

	// 1. Digital silence -> Silence (#494)
	silence := make([]int16, windowSamples)
	rep0, err := eng.Score(silence)
	if err != nil {
		t.Fatalf("Score(silence): %v", err)
	}
	if len(rep0.Top) == 0 || rep0.Top[0].ID != 494 || rep0.Top[0].Score < 0.80 {
		t.Fatalf("silence top-1 = %+v, want #494 Silence >= 0.80", rep0.Top)
	}

	// 2. Synthesized two-tone alarm/siren sweep (880 Hz <-> 1320 Hz) -> non-silence AudioSet class
	tone := make([]int16, windowSamples)
	var phase float64
	for i := range tone {
		tm := float64(i) / 16000.0
		freq := 880.0
		if int(tm*4)%2 == 1 {
			freq = 1320.0
		}
		phase += 2 * math.Pi * freq / 16000.0
		w := math.Sin(phase) + 0.4*math.Sin(2*phase) + 0.2*math.Sin(3*phase)
		tone[i] = int16(9000 * w)
	}
	rep1, err := eng.Score(tone)
	if err != nil {
		t.Fatalf("Score(tone): %v", err)
	}
	t.Logf("in-process YAMNet inference: %.2f ms, top1=#%d %s (%.3f), top2=#%d %s (%.3f)",
		rep1.MS, rep1.Top[0].ID, rep1.Top[0].Name, rep1.Top[0].Score,
		rep1.Top[1].ID, rep1.Top[1].Name, rep1.Top[1].Score)

	if len(rep1.Top) == 0 || rep1.Top[0].ID == 494 || rep1.Top[0].Score < 0.20 {
		t.Fatalf("synthesized tone failed to score distinctly: top=%+v", rep1.Top)
	}

	// 3. Also verify InstallModel and RemoveModel lifecycle with a real model
	wasDir := ModelDir
	ModelDir = t.TempDir()
	t.Cleanup(func() { ModelDir = wasDir })
	config.Use(filepath.Join(t.TempDir(), "state.json"))

	s := build()
	if err := s.InstallModel(strings.NewReader("not a tflite model")); err == nil {
		t.Fatal("expected InstallModel to reject invalid model bytes")
	}
	rawModel, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.InstallModel(bytes.NewReader(rawModel)); err != nil {
		t.Fatalf("InstallModel(real yamnet.tflite): %v", err)
	}
	if ok, sz := ModelInfo(); !ok || sz != int64(len(rawModel)) {
		t.Fatalf("ModelInfo() = %v, %d; want true, %d", ok, sz, len(rawModel))
	}
	if err := s.RemoveModel(); err != nil {
		t.Fatalf("RemoveModel(): %v", err)
	}
	if ok, _ := ModelInfo(); ok {
		t.Fatal("expected model to be removed")
	}
}
