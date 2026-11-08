// Package sound classifies room sounds locally on the device using Google's AudioSet-calibrated
// int8 YAMNet .tflite model and reports room sound state, events (esphome.techo5_sound), optional
// Home Assistant notifications, and optional MQTT messages.
//
// Off by default: when RoomSound is false, no model is downloaded or mapped, no buffers are
// allocated, and nothing subscribes to the microphones. When turned on, 0.975s windows of raw
// 16 kHz PCM are scored in-process over a read-only mmap only when room energy rises above the
// quiet floor and while the speaker is silent and the microphones are unmuted.
package sound

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/media"
	"github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	"github.com/HuskerMinion/techo5/echod/internal/feature/person"
	"github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	"github.com/HuskerMinion/techo5/echod/internal/feature/presence"
	"github.com/HuskerMinion/techo5/echod/internal/feature/ring"
	"github.com/HuskerMinion/techo5/echod/internal/feature/talkback"
	"github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/mic"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/speaker"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/update"
)

func init() {
	component.Register(component.Device, Get(), component.Order(8))
}

// SoundEvent is fired in Home Assistant when an enabled alert sound is recognized.
const SoundEvent = "esphome.techo5_sound"

// MaxModelUpload is the largest .tflite file the setup page will accept (8 MiB).
const MaxModelUpload = 8 << 20

const (
	modelFileName   = "yamnet.tflite"
	legacyModelPath = "/data/sound-ai/yamnet.tflite"

	windowSamples = 15600 // 0.975s @ 16 kHz
	stepSamples   = 7800  // 0.4875s hop (50% overlap)
	playingLevel  = 64    // speaker loopback RMS threshold

	busyAfter = 350 * time.Millisecond
	holdFor   = 6 * time.Second
	maxEvents = 32

	// Energy gate: skip inference when the room sits quietly on its noise floor.
	minActiveDBFS = -68.0
	alwaysRunDBFS = -52.0
	aboveFloorDB  = 4.5
)

// officialModel is Google's AudioSet int8-quantized YAMNet .tflite model (Apache-2.0).
var officialModel = update.Binary{
	URL:    "https://storage.googleapis.com/mediapipe-models/audio_classifier/yamnet/float32/1/yamnet.tflite",
	SHA256: "4d8b4a53282dc83ef04e3e7dbc4fbc98082e34e44ed798e16c3a0cdd4c584faf",
	Size:   4126810,
}

var (
	// ModelDir is where yamnet.tflite is stored on /data; replaceable in tests.
	ModelDir = filepath.Join(layout.StateDir, "yamnet")
	download = update.Download
	mqttDial = net.DialTimeout
)

// ModelPath returns the path to the active yamnet.tflite file.
func ModelPath() string {
	return filepath.Join(ModelDir, modelFileName)
}

// HeardEvent is one recognized room sound kept in memory for the setup page's Watch view.
type HeardEvent struct {
	Seq        uint64  `json:"seq"`
	At         string  `json:"at"`
	Category   string  `json:"category"`
	Label      string  `json:"label"`
	RawClass   string  `json:"raw_class"`
	Confidence int     `json:"confidence"`
	DBFS       float64 `json:"dbfs"`
	MS         float64 `json:"ms"`
	Alert      bool    `json:"alert"`
}

// Status summarizes the room sound and camera activity detector for the setup page (numbers only, never audio/video).
type Status struct {
	Enabled          bool         `json:"enabled"`
	Muted            bool         `json:"muted"`
	CameraAvailable  bool         `json:"camera_available"`
	MotionEnabled    bool         `json:"motion_enabled"`
	PersonEnabled    bool         `json:"person_enabled"`
	MotionNow        bool         `json:"motion_now"`
	PersonNow        bool         `json:"person_now"`
	PersonConfidence int          `json:"person_confidence"`
	ModelInstalled   bool         `json:"model_installed"`
	ModelBytes       int64        `json:"model_bytes"`
	Downloading      bool         `json:"downloading"`
	LastError        string       `json:"last_error,omitempty"`
	Label            string       `json:"label"`
	Confidence       int          `json:"confidence"`
	FloorDBFS        float64      `json:"floor_dbfs"`
	LastMS           float64      `json:"last_ms"`
	Top              []TopClass   `json:"top,omitempty"`
	Events           []HeardEvent `json:"events"`
}

type mqttJob struct {
	sc      config.RoomSoundConfig
	devName string
	det     Detection
	rep     EngineReply
}

// Sound manages the Home Assistant entities, on-demand model lifecycle, and in-process YAMNet engine.
type Sound struct {
	on         *esphome.Switch
	state      *esphome.TextSensor
	confidence *esphome.Sensor

	busy  func() bool
	muted func() bool
	score func(ctx context.Context, pcm []int16) (EngineReply, error)
	fire  func(det Detection, rep EngineReply)

	mu          sync.Mutex
	enabled     bool
	downloading bool
	lastErr     string
	curLabel    string
	curConf     int
	floorDB     float64
	lastMS      float64
	lastTop     []TopClass
	lastFired   map[string]time.Time
	seq         uint64
	events      []HeardEvent
	wake        chan struct{}
	mqttCh      chan mqttJob

	engMu sync.Mutex
	dlMu  sync.Mutex
	eng   *Engine

	played atomic.Int64
}

var (
	once   sync.Once
	shared *Sound
)

// Get returns the singleton room sound component.
func Get() *Sound {
	once.Do(func() { shared = build() })
	return shared
}

func build() *Sound {
	s := &Sound{
		on: &esphome.Switch{Base: esphome.Base{
			ObjectID: "room_sound_classifier",
			Name:     "Room sound classifier",
			Icon:     "mdi:waveform",
			Category: esphome.CategoryConfig,
			DeviceID: component.DeviceMicrophone,
		}},
		state: &esphome.TextSensor{Base: esphome.Base{
			ObjectID: "room_sound",
			Name:     "Room sound",
			Icon:     "mdi:ear-hearing",
			DeviceID: component.DeviceMicrophone,
		}},
		confidence: &esphome.Sensor{
			Base: esphome.Base{
				ObjectID: "room_sound_confidence",
				Name:     "Room sound confidence",
				Icon:     "mdi:percent",
				DeviceID: component.DeviceMicrophone,
			},
			Unit:       "%",
			StateClass: esphome.StateClassMeasurement,
		},
		busy:     deviceBusy,
		muted:    micMuted,
		curLabel: "Off",
		floorDB:  -72.0,
		wake:     make(chan struct{}, 1),
		mqttCh:   make(chan mqttJob, 2),
	}
	s.score = s.scoreInProcess
	s.fire = s.emitAlert

	s.on.OnCommand = func(on bool) {
		s.set(on)
		if err := config.Set().Microphone().RoomSound(on); err != nil {
			slog.Error("saving room sound classifier failed", "err", err)
		}
	}
	return s
}

func deviceBusy() bool {
	playing, _ := media.Get().Playing()
	return voice.Get().Busy() || phone.Get().Busy() || talkback.Get().Busy() || ring.IsSounding() ||
		speaker.Sound().Busy() || playing || media.Get().ExternalPlaying()
}

func micMuted() bool {
	m, err := mute.Get().Muted()
	return err == nil && m
}

func (s *Sound) Name() string { return "room sound" }

func (s *Sound) Entities() []esphome.Entity {
	return []esphome.Entity{s.on, s.state, s.confidence}
}

func (s *Sound) Restore(cfg config.Config) {
	s.set(cfg.Microphone.RoomSound)
	slog.Info("restored", "what", s.on.ObjectID, "using", cfg.Microphone.RoomSound)
}

// SetOn turns the room sound classifier on or off and persists the choice.
func (s *Sound) SetOn(on bool) error {
	s.set(on)
	return config.Set().Microphone().RoomSound(on)
}

func (s *Sound) set(on bool) {
	s.on.Set(on)
	s.mu.Lock()
	s.enabled = on
	if !on {
		s.curLabel = "Off"
		s.curConf = 0
		s.lastTop = nil
	} else if s.curLabel == "Off" {
		s.curLabel = "Quiet"
		s.curConf = 0
	}
	s.mu.Unlock()

	if !on {
		s.closeEngine()
		s.state.Set("Off")
		s.confidence.Set(float32(math.NaN()))
	} else {
		s.state.Set("Quiet")
		s.confidence.Set(0)
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *Sound) isOn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enabled
}

// ModelInfo reports whether yamnet.tflite is on disk and its size in bytes.
func ModelInfo() (bool, int64) {
	st, err := os.Stat(ModelPath())
	if err != nil || st.IsDir() || st.Size() < 64 {
		return false, 0
	}
	return true, st.Size()
}

// ReadStatus returns a snapshot of the classifier's state and any events after seq.
func (s *Sound) ReadStatus(after uint64) Status {
	installed, size := ModelInfo()
	muted := s.muted()
	pc := config.Get().Presence
	personNow, personConf := person.State()

	s.mu.Lock()
	defer s.mu.Unlock()

	evs := make([]HeardEvent, 0, len(s.events))
	for _, ev := range s.events {
		if ev.Seq > after {
			evs = append(evs, ev)
		}
	}
	var top []TopClass
	if len(s.lastTop) > 0 {
		top = make([]TopClass, len(s.lastTop))
		copy(top, s.lastTop)
	}
	return Status{
		Enabled:          s.enabled,
		Muted:            muted,
		CameraAvailable:  presence.HasCamera(),
		MotionEnabled:    pc.On,
		PersonEnabled:    pc.Person,
		MotionNow:        presence.Present(),
		PersonNow:        personNow,
		PersonConfidence: personConf,
		ModelInstalled:   installed,
		ModelBytes:       size,
		Downloading:      s.downloading,
		LastError:        s.lastErr,
		Label:            s.curLabel,
		Confidence:       s.curConf,
		FloorDBFS:        math.Round(s.floorDB*10) / 10,
		LastMS:           math.Round(s.lastMS*10) / 10,
		Top:              top,
		Events:           evs,
	}
}

// DownloadModel fetches the official YAMNet .tflite model (verified by size and SHA-256) if not already installed.
func (s *Sound) DownloadModel(ctx context.Context) error {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()

	if ok, _ := ModelInfo(); ok {
		return nil
	}
	if adoptLegacyModel() {
		return nil
	}

	s.mu.Lock()
	s.downloading = true
	s.lastErr = ""
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.downloading = false
		s.mu.Unlock()
	}()

	if err := os.MkdirAll(ModelDir, 0o755); err != nil {
		s.setErr(err)
		return err
	}
	slog.Info("room sound: downloading official YAMNet model", "bytes", officialModel.Size)
	if err := download(ctx, officialModel, ModelPath(), nil); err != nil {
		s.setErr(err)
		return fmt.Errorf("downloading YAMNet model: %w", err)
	}
	slog.Info("room sound: YAMNet model installed", "path", ModelPath())
	return nil
}

// adoptLegacyModel migrates /data/sound-ai/yamnet.tflite if present and matching the official SHA-256.
func adoptLegacyModel() bool {
	if legacyModelPath == ModelPath() {
		return false
	}
	f, err := os.Open(legacyModelPath)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.Size() != officialModel.Size {
		return false
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return false
	}
	if hex.EncodeToString(h.Sum(nil)) != officialModel.SHA256 {
		return false
	}
	if err := os.MkdirAll(ModelDir, 0o755); err != nil {
		return false
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return false
	}
	if err := writeAtomic(ModelPath(), f); err != nil {
		return false
	}
	slog.Info("room sound: adopted existing YAMNet model", "from", legacyModelPath)
	return true
}

// InstallModel validates a uploaded .tflite model with LoadEngine before placing it at ModelPath().
func (s *Sound) InstallModel(r io.Reader) error {
	s.dlMu.Lock()
	defer s.dlMu.Unlock()

	if err := os.MkdirAll(ModelDir, 0o755); err != nil {
		return err
	}
	tmp := ModelPath() + ".upload"
	defer os.Remove(tmp)

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, copyErr := io.Copy(f, io.LimitReader(r, MaxModelUpload+1))
	syncErr := f.Sync()
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if n > MaxModelUpload {
		return fmt.Errorf("that file is too big: a YAMNet model is at most %d MB", MaxModelUpload>>20)
	}
	eng, err := LoadEngine(tmp)
	if err != nil {
		return errors.New("that file is not a compatible 16 kHz AudioSet YAMNet TensorFlow Lite model")
	}
	eng.Close()

	s.closeEngine()
	if err := os.Rename(tmp, ModelPath()); err != nil {
		return err
	}
	s.mu.Lock()
	s.lastErr = ""
	s.mu.Unlock()
	slog.Info("room sound: uploaded YAMNet model installed", "bytes", n)
	return nil
}

// RemoveModel turns the classifier off, unmaps any loaded engine, and deletes yamnet.tflite from disk.
func (s *Sound) RemoveModel() error {
	if s.isOn() {
		if err := s.SetOn(false); err != nil {
			return err
		}
	}
	s.closeEngine()
	if err := os.Remove(ModelPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	slog.Info("room sound: removed YAMNet model")
	return nil
}

// TestHANotification sends a test notification using the configured Home Assistant service.
func (s *Sound) TestHANotification() error {
	c := config.Get()
	return SendHANotification(
		c.Microphone.Sound.HAService,
		c.Device.Name,
		Detection{Category: "test", Label: "Test sound", RawClass: "Finger snapping", Confidence: 0.99, Actionable: true},
		EngineReply{MS: 65.0, RMSDBFS: -28.0},
	)
}

// TestMQTT publishes a test event (and discovery config if enabled) to the configured MQTT broker.
func (s *Sound) TestMQTT() error {
	c := config.Get()
	return PublishMQTT(
		mqttDial,
		c.Microphone.Sound,
		c.Device.Name,
		Detection{Category: "test", Label: "Test sound", RawClass: "Finger snapping", Confidence: 0.99, Actionable: true},
		EngineReply{MS: 65.0, RMSDBFS: -28.0},
		true,
	)
}

func writeAtomic(dest string, r io.Reader) error {
	tmp := dest + ".part"
	w, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, r)
	if err == nil {
		err = w.Sync()
	}
	if cerr := w.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dest)
}

func (s *Sound) setErr(err error) {
	if err == nil {
		return
	}
	s.mu.Lock()
	s.lastErr = err.Error()
	s.mu.Unlock()
}

func (s *Sound) closeEngine() {
	s.engMu.Lock()
	defer s.engMu.Unlock()
	if s.eng != nil {
		s.eng.Close()
		s.eng = nil
	}
}

func (s *Sound) scoreInProcess(ctx context.Context, pcm []int16) (EngineReply, error) {
	if err := ctx.Err(); err != nil {
		return EngineReply{}, err
	}
	if ok, _ := ModelInfo(); !ok {
		if err := s.DownloadModel(ctx); err != nil {
			return EngineReply{}, err
		}
	}
	s.engMu.Lock()
	defer s.engMu.Unlock()

	if s.eng == nil {
		eng, err := LoadEngine(ModelPath())
		if err != nil {
			s.setErr(err)
			return EngineReply{}, err
		}
		s.eng = eng
		slog.Info("room sound: loaded in-process YAMNet engine", "path", ModelPath())
	}
	return s.eng.Score(pcm)
}

func (s *Sound) emitAlert(det Detection, rep EngineReply) {
	c := config.Get()
	sc := c.Microphone.Sound
	devName := c.Device.Name

	slog.Info("room sound detected",
		"category", det.Category,
		"label", det.Label,
		"raw_class", det.RawClass,
		"confidence", math.Round(det.Confidence*100)/100,
		"rms_dbfs", math.Round(rep.RMSDBFS*10)/10,
		"inference_ms", math.Round(rep.MS*10)/10,
	)
	component.Fire.Emit(component.Event{Name: SoundEvent, Data: map[string]string{
		"category":     det.Category,
		"sound":        det.Label,
		"raw_class":    det.RawClass,
		"confidence":   strconv.FormatFloat(det.Confidence, 'f', 2, 64),
		"rms_dbfs":     strconv.FormatFloat(rep.RMSDBFS, 'f', 1, 64),
		"inference_ms": strconv.FormatFloat(rep.MS, 'f', 1, 64),
		"device":       devName,
	}})

	if strings.TrimSpace(sc.HAService) != "" {
		if err := SendHANotification(sc.HAService, devName, det, rep); err != nil {
			slog.Warn("room sound: Home Assistant notification failed", "err", err)
		}
	}
	if strings.TrimSpace(sc.MQTTBroker) != "" {
		select {
		case s.mqttCh <- mqttJob{sc: sc, devName: devName, det: det, rep: rep}:
		default:
		}
	}
	if sc.AlarmSiren {
		ring.Sample(speaker.AlarmSound(c.Alarms.Sound))
	}
}

func (s *Sound) shouldFire(category string, now time.Time, cooldown time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastFired == nil {
		s.lastFired = make(map[string]time.Time)
	}
	if now.Sub(s.lastFired[category]) < cooldown {
		return false
	}
	s.lastFired[category] = now
	return true
}

func (s *Sound) handleCameraDetection(det Detection) {
	now := time.Now()
	sc := config.Get().Microphone.Sound
	cooldown := time.Duration(sc.CooldownSec()) * time.Second
	alert := AlertEnabled(sc, det.Category)
	fired := false
	if alert && s.shouldFire(det.Category, now, cooldown) {
		fired = true
		s.fire(det, EngineReply{})
	}
	s.recordEvent(now, det, EngineReply{}, alert && fired)
}

// Run listens on the raw microphone capture while enabled and forwards camera motion/person alerts.
func (s *Sound) Run(ctx context.Context) error {
	defer s.closeEngine()

	unsubMotion := presence.OnMotion(func(present bool) {
		if present {
			s.handleCameraDetection(Detection{
				Category:   "motion",
				Label:      "Motion seen",
				RawClass:   "Camera 32x24 motion",
				Confidence: 1.0,
				Actionable: true,
			})
		}
	})
	defer unsubMotion()

	unsubPerson := person.OnPerson(func(conf float64) {
		s.handleCameraDetection(Detection{
			Category:   "person",
			Label:      "Person seen",
			RawClass:   "MobileNetV1 96x96 person",
			Confidence: conf,
			Actionable: true,
		})
	})
	defer unsubPerson()

	mqttDone := make(chan struct{})
	go func() {
		defer close(mqttDone)
		s.runMQTT(ctx)
	}()
	defer func() { <-mqttDone }()

	for {
		if !s.isOn() {
			select {
			case <-ctx.Done():
				return nil
			case <-s.wake:
			}
			continue
		}
		raw, unlisten := mic.Get().ListenRaw()
		s.listen(ctx, s.monoOf(raw))
		unlisten()
		if ctx.Err() != nil {
			return nil
		}
	}
}

func (s *Sound) runMQTT(ctx context.Context) {
	var discKey string
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.mqttCh:
			key := job.sc.MQTTBroker + "|" + DefaultTopic(job.sc, job.devName) + "|" + job.devName
			includeDisc := job.sc.MQTTDiscovery && key != discKey
			if err := PublishMQTT(mqttDial, job.sc, job.devName, job.det, job.rep, includeDisc); err != nil {
				slog.Warn("room sound: MQTT publish failed", "err", err)
				s.setErr(err)
			} else if includeDisc {
				discKey = key
			}
		}
	}
}

func (s *Sound) monoOf(raw <-chan []byte) <-chan []int16 {
	out := make(chan []int16, 16)
	go func() {
		defer close(out)
		for b := range raw {
			if sounding(mic.Reference(b)) {
				s.played.Store(time.Now().UnixNano())
			}
			select {
			case out <- mic.Mono(b):
			default:
			}
		}
	}()
	return out
}

func sounding(refs [][]int16) bool {
	for _, ch := range refs {
		if len(ch) == 0 {
			continue
		}
		var sum float64
		for _, v := range ch {
			sum += float64(v) * float64(v)
		}
		if math.Sqrt(sum/float64(len(ch))) > playingLevel {
			return true
		}
	}
	return false
}

func rmsDBFS(samples []int16) float64 {
	if len(samples) == 0 {
		return -120.0
	}
	var sum float64
	for _, v := range samples {
		f := float64(v) / 32768.0
		sum += f * f
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	if rms <= 1e-9 {
		return -120.0
	}
	return 20.0 * math.Log10(rms)
}

func (s *Sound) listen(ctx context.Context, frames <-chan []int16) {
	ringBuf := make([]int16, 0, windowSamples*2)
	sinceStep := 0
	floorDB := -72.0
	floorInit := false

	var quietFrom time.Time
	var heldUntil time.Time
	var nextRetry time.Time
	lastLabel := "Quiet"
	lastConf := float32(0)
	wasMuted := false

	publish := func(label string, conf float32) {
		s.mu.Lock()
		s.curLabel = label
		if math.IsNaN(float64(conf)) {
			s.curConf = 0
		} else {
			s.curConf = int(math.Round(float64(conf)))
		}
		s.floorDB = floorDB
		s.mu.Unlock()

		if label != lastLabel {
			s.state.Set(label)
			lastLabel = label
		}
		if (math.IsNaN(float64(conf)) != math.IsNaN(float64(lastConf))) ||
			(!math.IsNaN(float64(conf)) && math.Abs(float64(conf-lastConf)) >= 1) {
			s.confidence.Set(conf)
			lastConf = conf
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
			if !s.isOn() {
				return
			}
		case f, ok := <-frames:
			if !ok {
				return
			}
			now := time.Now()
			if s.muted() {
				if !wasMuted {
					wasMuted = true
					ringBuf = ringBuf[:0]
					sinceStep = 0
					publish("Muted", float32(math.NaN()))
				}
				continue
			}
			if wasMuted {
				wasMuted = false
				publish("Quiet", 0)
			}

			if s.busy() {
				quietFrom = now.Add(busyAfter)
			}
			if p := s.played.Load(); p != 0 {
				if after := time.Unix(0, p).Add(busyAfter); after.After(quietFrom) {
					quietFrom = after
				}
			}
			if now.Before(quietFrom) {
				ringBuf = ringBuf[:0]
				sinceStep = 0
				continue
			}

			ringBuf = append(ringBuf, f...)
			sinceStep += len(f)
			if len(ringBuf) < windowSamples || sinceStep < stepSamples {
				continue
			}

			win := ringBuf[len(ringBuf)-windowSamples:]
			hop := win[windowSamples-stepSamples:]
			sinceStep = 0
			if len(ringBuf) > windowSamples+stepSamples {
				copy(ringBuf[:windowSamples], win)
				ringBuf = ringBuf[:windowSamples]
				win = ringBuf
			}

			hopDB := rmsDBFS(hop)
			if !floorInit {
				floorDB = hopDB
				floorInit = true
			}
			active := hopDB >= minActiveDBFS && (hopDB >= alwaysRunDBFS || hopDB >= floorDB+aboveFloorDB)
			if !active {
				floorDB = 0.92*floorDB + 0.08*hopDB
				if now.After(heldUntil) {
					publish("Quiet", 0)
				} else {
					s.mu.Lock()
					s.floorDB = floorDB
					s.mu.Unlock()
				}
				continue
			}
			if hopDB < floorDB {
				floorDB = 0.95*floorDB + 0.05*hopDB
			}
			if now.Before(nextRetry) {
				continue
			}

			rep, err := s.score(ctx, win)
			if err != nil {
				slog.Warn("room sound score failed", "err", err)
				nextRetry = now.Add(15 * time.Second)
				continue
			}

			sc := config.Get().Microphone.Sound
			minConf := float64(sc.ConfidencePct()) / 100.0
			cooldown := time.Duration(sc.CooldownSec()) * time.Second

			det := ClassifyWithMin(rep.Top, minConf)
			s.mu.Lock()
			s.lastMS = rep.MS
			s.floorDB = floorDB
			if len(rep.Top) > 5 {
				s.lastTop = append(s.lastTop[:0], rep.Top[:5]...)
			} else {
				s.lastTop = append(s.lastTop[:0], rep.Top...)
			}
			s.mu.Unlock()

			if det.Category == "quiet" {
				floorDB = 0.96*floorDB + 0.04*hopDB
				if now.After(heldUntil) {
					publish("Quiet", 0)
				}
				continue
			}

			pct := float32(math.Round(det.Confidence * 100))
			heldUntil = now.Add(holdFor)
			publish(det.Label, pct)

			alert := AlertEnabled(sc, det.Category)
			fired := false
			if alert && s.shouldFire(det.Category, now, cooldown) {
				fired = true
				s.fire(det, rep)
			}

			s.recordEvent(now, det, rep, alert && fired)
		}
	}
}

func (s *Sound) recordEvent(at time.Time, det Detection, rep EngineReply, alert bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Coalesce identical consecutive ambient updates within 3 seconds unless an alert fired.
	if !alert && len(s.events) > 0 {
		last := s.events[len(s.events)-1]
		if last.Category == det.Category {
			if t, err := time.Parse(time.RFC3339, last.At); err == nil && at.Sub(t) < 3*time.Second {
				return
			}
		}
	}
	s.seq++
	ev := HeardEvent{
		Seq:        s.seq,
		At:         at.UTC().Format(time.RFC3339),
		Category:   det.Category,
		Label:      det.Label,
		RawClass:   det.RawClass,
		Confidence: int(math.Round(det.Confidence * 100)),
		DBFS:       math.Round(rep.RMSDBFS*10) / 10,
		MS:         math.Round(rep.MS*10) / 10,
		Alert:      alert,
	}
	if len(s.events) >= maxEvents {
		copy(s.events, s.events[1:])
		s.events[len(s.events)-1] = ev
	} else {
		s.events = append(s.events, ev)
	}
}
