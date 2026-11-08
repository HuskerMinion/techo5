package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/person"
	"github.com/HuskerMinion/techo5/echod/internal/feature/presence"
	"github.com/HuskerMinion/techo5/echod/internal/feature/sound"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

func init() {
	saves["roomsound"] = saveRoomSound
	saves["roomsound-action"] = roomSoundAction
	routes["/setup/roomsound"] = (*Feature).roomSoundState
	routes["/setup/roomsound/model"] = (*Feature).roomSoundUpload
	watching["/setup/roomsound"] = true
}

var readRoomSound = func(after uint64) sound.Status {
	return sound.Get().ReadStatus(after)
}

// roomSoundState answers the Watch view's poll for a let-in browser without extending the idle timer.
func (f *Feature) roomSoundState(w http.ResponseWriter, r *http.Request) {
	if _, in := f.session(r); !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "read room sound state with GET", http.StatusMethodNotAllowed)
		return
	}
	after, _ := strconv.ParseUint(r.URL.Query().Get("after"), 10, 64)
	st := readRoomSound(after)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = json.NewEncoder(w).Encode(st)
}

// roomSoundUpload accepts a .tflite model uploaded from the setup page, checking the session cookie
// and CSRF token before reading the file, and validating the FlatBuffer graph before keeping it.
func (f *Feature) roomSoundUpload(w http.ResponseWriter, r *http.Request) {
	token, in := f.session(r)
	if !in {
		http.Error(w, "not let in", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "post a model", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, sound.MaxModelUpload+64<<10)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "that was not a model sent from this page", http.StatusBadRequest)
		return
	}
	p, err := mr.NextPart()
	if err != nil || p.FormName() != "token" {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	got, err := io.ReadAll(io.LimitReader(p, 256))
	if err != nil || !sameToken(string(got), token) {
		http.Error(w, "that did not come from this page", http.StatusForbidden)
		return
	}
	problem := receiveRoomSoundModel(mr)
	if problem == "" {
		http.Redirect(w, r, "/setup?saved=1&tab=sound#roomsound", http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/setup?tab=sound&problem="+url.QueryEscape(problem)+"#roomsound", http.StatusSeeOther)
}

func receiveRoomSoundModel(mr *multipart.Reader) string {
	p, err := mr.NextPart()
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return fmt.Sprintf("that file is too big: a YAMNet model is at most %d MB", sound.MaxModelUpload>>20)
	}
	if err != nil || p.FormName() != "file" {
		return "choose a .tflite file first"
	}
	if err := sound.Get().InstallModel(p); err != nil {
		if errors.As(err, &maxErr) {
			return fmt.Sprintf("that file is too big: a YAMNet model is at most %d MB", sound.MaxModelUpload>>20)
		}
		return err.Error()
	}
	return ""
}

func roomSoundSection(w http.ResponseWriter, token string) {
	c := config.Get()
	sc := c.Microphone.Sound
	st := readRoomSound(0)

	fmt.Fprint(w, `<fieldset id="roomsound"><legend>Room sounds</legend>
	 <p class="note" style="margin-top:0">Classifies household sounds on the device (smoke alarms, sirens, glass breaking,
	  baby cry, doorbells, dogs, knocks, claps) using an int8 YAMNet TensorFlow Lite model, and optionally watches the camera
	  for motion and people (96×96 int8 MobileNetV1). <strong>Off by default:</strong> while off, no model is downloaded or
	  mapped in memory and nothing subscribes to the microphones or camera. Only category names and confidence numbers are
	  reported, never audio or video.</p>`)

	// Live status + Watch panel
	nowText := html.EscapeString(st.Label)
	if st.Enabled && st.Confidence > 0 {
		nowText = fmt.Sprintf(`%s (%d%%, %.1f ms)`, html.EscapeString(st.Label), st.Confidence, st.LastMS)
	}
	fmt.Fprintf(w, `<p style="margin:.4rem 0"><strong>Sound:</strong> <span id="rsnow">%s</span>
	 <span class="note" id="rsmeta">(floor %.1f dBFS)</span></p>`, nowText, st.FloorDBFS)
	if st.CameraAvailable {
		camText := "Off"
		switch {
		case st.PersonEnabled && st.PersonNow:
			camText = fmt.Sprintf("Person seen (%d%%)", st.PersonConfidence)
		case st.MotionEnabled && st.MotionNow:
			camText = "Motion seen"
		case st.PersonEnabled:
			camText = "Watching (motion + person AI)"
		case st.MotionEnabled:
			camText = "Watching (motion)"
		}
		fmt.Fprintf(w, `<p style="margin:.2rem 0"><strong>Camera:</strong> <span id="rscam">%s</span></p>`, html.EscapeString(camText))
	}
	if st.LastError != "" {
		fmt.Fprintf(w, `<p class="bad" id="rserr">%s</p>`, html.EscapeString(st.LastError))
	} else {
		fmt.Fprint(w, `<p class="bad" id="rserr" hidden></p>`)
	}
	fmt.Fprint(w, `<p class="note" id="rstop" style="margin:.2rem 0" hidden></p>`)

	var seen uint64
	for _, ev := range st.Events {
		seen = max(seen, ev.Seq)
	}
	fmt.Fprintf(w, `<p style="margin-bottom:0"><strong>Last activity</strong></p><ul class="scevents" id="rsevents" data-after="%d">`, seen)
	events := st.Events
	if len(events) > 12 {
		events = events[len(events)-12:]
	}
	for i := len(events) - 1; i >= 0; i-- {
		ev := events[i]
		tag := ""
		if ev.Alert {
			tag = " [alert]"
		}
		at := ev.At
		if t, err := time.Parse(time.RFC3339, ev.At); err == nil {
			at = t.Local().Format("15:04:05")
		}
		if ev.DBFS == 0 && ev.MS == 0 {
			fmt.Fprintf(w, `<li>%s %s (%s): %d%%%s</li>`,
				html.EscapeString(at), html.EscapeString(ev.Label), html.EscapeString(ev.RawClass),
				ev.Confidence, tag)
		} else {
			fmt.Fprintf(w, `<li>%s %s (%s): %d%%, %.1f dBFS, %.0f ms%s</li>`,
				html.EscapeString(at), html.EscapeString(ev.Label), html.EscapeString(ev.RawClass),
				ev.Confidence, ev.DBFS, ev.MS, tag)
		}
	}
	if len(events) == 0 {
		fmt.Fprint(w, `<li id="rsquiet" class="note">Nothing yet.</li>`)
	}
	fmt.Fprintf(w, `</ul>
	 <div class="btns"><button type="button" id="rswatch">Watch</button><a href="/setup?tab=sound&amp;rs=%d#roomsound">Refresh</a>
	  <span id="rssays" class="note"></span></div>`, time.Now().Unix())

	// Model status & actions (0 KB in firmware; stored on /data only when wanted)
	modelDesc := `<span class="note">not installed (0 KB on disk; fetched automatically when turned on, or upload offline)</span>`
	switch {
	case st.Downloading:
		modelDesc = `<span class="note">downloading yamnet.tflite (4.0 MB)...</span>`
	case st.ModelInstalled:
		modelDesc = fmt.Sprintf(`<span class="ok">installed (%.1f MB on /data)</span>`, float64(st.ModelBytes)/(1024*1024))
	}
	fmt.Fprintf(w, `<p style="margin:1rem 0 .4rem"><strong>YAMNet model:</strong> %s</p><div class="btns">`, modelDesc)
	if !st.ModelInstalled && !st.Downloading {
		fmt.Fprint(w, `<form method="post" action="/setup/save" style="margin:0">`)
		hidden(w, token, "roomsound-action", "sound")
		fmt.Fprint(w, `<input type="hidden" name="action" value="download"><button type="submit" class="quiet">Download official model (4 MB)</button></form>`)
	}
	fmt.Fprintf(w, `<form method="post" action="/setup/roomsound/model" enctype="multipart/form-data"
	 style="margin:0;display:flex;gap:.5rem;flex:1;flex-wrap:wrap;align-items:center">
	 <input type="hidden" name="token" value="%s">
	 <input type="file" name="file" accept=".tflite" required style="width:auto;flex:1;min-width:11rem" aria-label="YAMNet .tflite model">
	 <button type="submit" class="quiet">Upload .tflite</button></form>`, html.EscapeString(token))
	if st.ModelInstalled {
		fmt.Fprint(w, `<form method="post" action="/setup/save" style="margin:0">`)
		hidden(w, token, "roomsound-action", "sound")
		fmt.Fprint(w, `<input type="hidden" name="action" value="remove"><button type="submit" class="quiet">Remove model</button></form>`)
	}
	fmt.Fprint(w, `</div>`)

	// Main configuration form
	fmt.Fprint(w, `<form method="post" action="/setup/save" style="margin-top:1rem">`)
	hidden(w, token, "roomsound", "sound")
	fmt.Fprintf(w, `<p><label><input type="checkbox" name="enabled" value="yes" style="width:auto"%s>
	  <strong>Enable room sound classifier</strong></label></p>`, checkedIf(c.Microphone.RoomSound))
	if presence.HasCamera() {
		fmt.Fprintf(w, `<p><label><input type="checkbox" name="cam_motion" value="yes" style="width:auto"%s>
	  <strong>Enable camera motion detection</strong> (32×24 frame grid)</label></p>
	 <p><label><input type="checkbox" name="cam_person" value="yes" style="width:auto"%s>
	  <strong>Enable camera AI person detection</strong> (embedded 96×96 int8 MobileNetV1)</label></p>`,
			checkedIf(c.Presence.On), checkedIf(c.Presence.Person))
	}
	fmt.Fprintf(w, `<label for="rsconf">Minimum confidence, %d to %d%%</label>
	 <input id="rsconf" name="min_confidence" type="number" min="%d" max="%d" value="%d">
	 <label for="rscool">Alert cooldown, %d to %d seconds</label>
	 <input id="rscool" name="cooldown" type="number" min="%d" max="%d" value="%d">`,
		config.MinSoundConfidence, config.MaxSoundConfidence, config.MinSoundConfidence, config.MaxSoundConfidence, sc.ConfidencePct(),
		config.MinSoundCooldown, config.MaxSoundCooldown, config.MinSoundCooldown, config.MaxSoundCooldown, sc.CooldownSec())

	fmt.Fprint(w, `<p style="margin:.8rem 0 .3rem"><strong>Alert triggers</strong> (fire <code>esphome.techo5_sound</code>, Home Assistant notification, MQTT, and optional speaker alarm):</p>
	 <div style="display:grid;grid-template-columns:repeat(auto-fill,minmax(11rem,1fr));gap:.2rem .8rem">`)
	for _, ch := range sound.AlertChoices {
		fmt.Fprintf(w, `<label><input type="checkbox" name="alert_%s" value="yes" style="width:auto"%s> %s</label>`,
			html.EscapeString(ch.Category), checkedIf(sound.AlertEnabled(sc, ch.Category)), html.EscapeString(ch.Label))
	}
	fmt.Fprintf(w, `</div>
	 <p style="margin:.6rem 0 0"><label><input type="checkbox" name="alarm_siren" value="yes" style="width:auto"%s>
	  <strong>Sound alarm chime on this device's speaker when an alert triggers</strong> (house alarm)</label></p>`,
		checkedIf(sc.AlarmSiren))

	defTopic := "techo5/" + layout.EntitySlug(deviceName()) + "/sound"
	fmt.Fprintf(w, `<label for="rsha" style="margin-top:.8rem">Home Assistant notification service (optional)</label>
	 <input id="rsha" name="ha_service" value="%s" placeholder="persistent_notification.create or notify.notify" autocomplete="off">
	 <p class="note">Called over the device's Home Assistant link when an alert sound is heard. In Home Assistant, enable
	  <strong>Allow the device to perform Home Assistant actions</strong> on this ESPHome device entry.</p>
	 <label for="rsmqtt">MQTT broker (optional)</label>
	 <input id="rsmqtt" name="mqtt_broker" value="%s" placeholder="192.168.1.10:1883" autocomplete="off">
	 <label for="rsmqttu">MQTT username (optional)</label>
	 <input id="rsmqttu" name="mqtt_user" value="%s" autocomplete="off">
	 <label for="rsmqttp">MQTT password (optional)</label>
	 <input id="rsmqttp" name="mqtt_pass" type="password" value="" placeholder="%s" autocomplete="off">
	 <p><label><input type="checkbox" name="nomqttpass" value="yes" style="width:auto"> Remove the MQTT password</label></p>
	 <label for="rstopic">MQTT topic prefix (optional)</label>
	 <input id="rstopic" name="mqtt_topic" value="%s" placeholder="%s" autocomplete="off">
	 <p><label><input type="checkbox" name="mqtt_discovery" value="yes" style="width:auto"%s>
	  Publish Home Assistant MQTT discovery sensor</label></p>
	 <p><button type="submit">Save</button></p></form>`,
		html.EscapeString(sc.HAService),
		html.EscapeString(sc.MQTTBroker),
		html.EscapeString(sc.MQTTUser),
		keyHint(sc.MQTTPass != ""),
		html.EscapeString(sc.MQTTTopic),
		html.EscapeString(defTopic),
		checkedIf(sc.MQTTDiscovery))

	// Test buttons for HA Notification and MQTT
	if sc.HAService != "" || sc.MQTTBroker != "" {
		fmt.Fprint(w, `<div class="btns" style="margin-top:.4rem">`)
		if sc.HAService != "" {
			fmt.Fprint(w, `<form method="post" action="/setup/save" style="margin:0">`)
			hidden(w, token, "roomsound-action", "sound")
			fmt.Fprint(w, `<input type="hidden" name="action" value="test-ha"><button type="submit" class="quiet">Test Home Assistant notification</button></form>`)
		}
		if sc.MQTTBroker != "" {
			fmt.Fprint(w, `<form method="post" action="/setup/save" style="margin:0">`)
			hidden(w, token, "roomsound-action", "sound")
			fmt.Fprint(w, `<input type="hidden" name="action" value="test-mqtt"><button type="submit" class="quiet">Test MQTT publish</button></form>`)
		}
		fmt.Fprint(w, `</div>`)
	}

	fmt.Fprint(w, roomSoundScript)
	fmt.Fprint(w, `</fieldset>`)
}

func saveRoomSound(r *http.Request) string {
	v := func(k string) string { return strings.TrimSpace(r.PostFormValue(k)) }

	conf, err := strconv.Atoi(v("min_confidence"))
	if err != nil || conf < config.MinSoundConfidence || conf > config.MaxSoundConfidence {
		return fmt.Sprintf("minimum confidence is %d to %d%%", config.MinSoundConfidence, config.MaxSoundConfidence)
	}
	cool, err := strconv.Atoi(v("cooldown"))
	if err != nil || cool < config.MinSoundCooldown || cool > config.MaxSoundCooldown {
		return fmt.Sprintf("cooldown is %d to %d seconds", config.MinSoundCooldown, config.MaxSoundCooldown)
	}

	haSvc := v("ha_service")
	if haSvc != "" && (!strings.Contains(haSvc, ".") || strings.ContainsAny(haSvc, " \t\r\n")) {
		return "Home Assistant service should look like persistent_notification.create or notify.notify"
	}
	broker, user, topic := v("mqtt_broker"), v("mqtt_user"), strings.Trim(v("mqtt_topic"), "/")
	if strings.ContainsAny(broker+user+topic+r.PostFormValue("mqtt_pass"), "\r\n") {
		return "MQTT fields must be one line each"
	}

	var alerts []string
	for _, ch := range sound.AlertChoices {
		if r.PostFormValue("alert_"+ch.Category) == "yes" {
			alerts = append(alerts, ch.Category)
		}
	}
	if len(alerts) == 0 {
		alerts = []string{"none"}
	}

	sc := config.RoomSoundConfig{
		MinConfidence: conf,
		Cooldown:      cool,
		Alerts:        alerts,
		AlarmSiren:    r.PostFormValue("alarm_siren") == "yes",
		HAService:     haSvc,
		MQTTBroker:    broker,
		MQTTUser:      user,
		MQTTTopic:     topic,
		MQTTDiscovery: r.PostFormValue("mqtt_discovery") == "yes",
	}
	if err := config.Set().Microphone().SoundSettings(sc); err != nil {
		return "could not save room sound settings: " + err.Error()
	}
	switch pass := r.PostFormValue("mqtt_pass"); {
	case r.PostFormValue("nomqttpass") == "yes":
		_ = config.Set().Microphone().SoundMQTTPass("")
	case pass != "":
		_ = config.Set().Microphone().SoundMQTTPass(pass)
	}

	if presence.HasCamera() {
		cur := config.Get().Presence
		wantPerson := r.PostFormValue("cam_person") == "yes"
		wantMotion := r.PostFormValue("cam_motion") == "yes" || wantPerson
		if wantMotion != cur.On {
			presence.SetMotion(wantMotion)
		}
		if wantPerson != cur.Person {
			person.SetPerson(wantPerson)
		}
	}

	on := r.PostFormValue("enabled") == "yes"
	if err := sound.Get().SetOn(on); err != nil {
		return "could not save room sound switch: " + err.Error()
	}
	if on {
		if ok, _ := sound.ModelInfo(); !ok {
			safe.Go("download yamnet model", func() {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				_ = sound.Get().DownloadModel(ctx)
			})
		}
	}
	slog.Info("setup page: room sound saved", "enabled", on, "min_confidence", conf, "cooldown", cool)
	return ""
}

func roomSoundAction(r *http.Request) string {
	switch r.PostFormValue("action") {
	case "download":
		safe.Go("download yamnet model", func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if err := sound.Get().DownloadModel(ctx); err != nil {
				slog.Warn("setup page: downloading YAMNet model failed", "err", err)
			}
		})
		return ""
	case "remove":
		if err := sound.Get().RemoveModel(); err != nil {
			return "could not remove the model: " + err.Error()
		}
		return ""
	case "test-ha":
		if err := sound.Get().TestHANotification(); err != nil {
			return err.Error()
		}
		return ""
	case "test-mqtt":
		if err := sound.Get().TestMQTT(); err != nil {
			return "MQTT test failed: " + err.Error()
		}
		return ""
	}
	return "unknown room sound action"
}

const roomSoundScript = `<script>
(() => {
  const btn = document.getElementById('rswatch'), says = document.getElementById('rssays');
  const nowEl = document.getElementById('rsnow'), metaEl = document.getElementById('rsmeta');
  const camEl = document.getElementById('rscam');
  const topEl = document.getElementById('rstop'), errEl = document.getElementById('rserr');
  const evEl = document.getElementById('rsevents');
  if (!btn || !evEl) return;
  let gen = 0, on = false, timer = null, failed = 0;
  let after = Number(evEl.dataset.after || 0);
  btn.addEventListener('click', () => { if (on) stop('Stopped.'); else start(); });
  function start() {
    on = true; failed = 0; btn.textContent = 'Stop watching'; says.textContent = 'Watching...';
    poll(++gen);
  }
  function stop(why) {
    on = false; gen++; if (timer) clearTimeout(timer); timer = null;
    btn.textContent = 'Watch'; says.textContent = why;
  }
  function again(g, ms) { if (on && g === gen) timer = setTimeout(() => poll(g), ms); }
  async function poll(g) {
    let s;
    try {
      const r = await fetch('/setup/roomsound?after=' + after, {cache: 'no-store'});
      if (!on || g !== gen) return;
      if (r.status === 403) { stop('This browser is no longer let in. Reload the page to ask again.'); return; }
      if (r.status === 404) { stop('The setup page has closed.'); return; }
      if (!r.ok) throw new Error(String(r.status));
      s = await r.json();
    } catch (e) {
      if (!on || g !== gen) return;
      if (++failed >= 5) { stop('The device stopped answering.'); return; }
      says.textContent = 'Trying again...';
      again(g, 2000);
      return;
    }
    if (!on || g !== gen) return;
    failed = 0;
    draw(s);
    says.textContent = s.downloading ? 'Downloading model...' : 'Watching...';
    again(g, 750);
  }
  function draw(s) {
    nowEl.textContent = (s.enabled && s.confidence > 0)
      ? (s.label + ' (' + s.confidence + '%, ' + (s.last_ms || 0).toFixed(1) + ' ms)')
      : (s.label || 'Off');
    metaEl.textContent = '(floor ' + (s.floor_dbfs || 0).toFixed(1) + ' dBFS' + (s.muted ? ', muted' : '') + ')';
    if (camEl) {
      if (s.person_enabled && s.person_now) camEl.textContent = 'Person seen (' + (s.person_confidence || 0) + '%)';
      else if (s.motion_enabled && s.motion_now) camEl.textContent = 'Motion seen';
      else if (s.person_enabled) camEl.textContent = 'Watching (motion + person AI)';
      else if (s.motion_enabled) camEl.textContent = 'Watching (motion)';
      else camEl.textContent = 'Off';
    }
    if (s.last_error) { errEl.textContent = s.last_error; errEl.hidden = false; }
    else { errEl.hidden = true; }
    if (s.top && s.top.length > 0) {
      topEl.textContent = 'Top classes: ' + s.top.map(c => c.name + ' ' + Math.round(c.score * 100) + '%').join(', ');
      topEl.hidden = false;
    } else {
      topEl.hidden = true;
    }
    for (const ev of (s.events || [])) {
      after = Math.max(after, ev.seq);
      const q = document.getElementById('rsquiet'); if (q) q.remove();
      const li = document.createElement('li');
      const at = new Date(ev.at).toLocaleTimeString([], {hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false});
      const tail = (!ev.dbfs && !ev.ms) ? '' : (', ' + (ev.dbfs || 0).toFixed(1) + ' dBFS, ' + Math.round(ev.ms || 0) + ' ms');
      li.textContent = at + ' ' + ev.label + ' (' + ev.raw_class + '): ' + ev.confidence + '%' + tail + (ev.alert ? ' [alert]' : '');
      evEl.prepend(li);
      while (evEl.children.length > 15) evEl.lastElementChild.remove();
    }
  }
})();
</script>`
