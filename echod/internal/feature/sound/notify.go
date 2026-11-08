package sound

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// DefaultAlertCategories are the household alert sounds and camera person sightings enabled when Alerts is unset.
var DefaultAlertCategories = []string{
	"person", "smoke_alarm", "siren", "glass", "baby_cry", "doorbell", "alarm", "dog", "knock", "clapping",
}

// AlertChoices lists every category selectable as an alert on the setup page.
type AlertChoice struct {
	Category string
	Label    string
}

var AlertChoices = []AlertChoice{
	{Category: "person", Label: "Person seen (camera)"},
	{Category: "motion", Label: "Motion seen (camera)"},
	{Category: "smoke_alarm", Label: "Smoke / fire alarm"},
	{Category: "siren", Label: "Siren"},
	{Category: "glass", Label: "Glass breaking"},
	{Category: "baby_cry", Label: "Baby cry"},
	{Category: "doorbell", Label: "Doorbell"},
	{Category: "alarm", Label: "Alarm / ring"},
	{Category: "dog", Label: "Dog"},
	{Category: "cat", Label: "Cat"},
	{Category: "knock", Label: "Knock"},
	{Category: "clapping", Label: "Clapping"},
	{Category: "finger_snap", Label: "Finger snapping"},
	{Category: "whistle", Label: "Whistling"},
	{Category: "cough", Label: "Cough"},
	{Category: "sneeze", Label: "Sneeze"},
	{Category: "laughter", Label: "Laughter"},
	{Category: "door", Label: "Door"},
}

// AlertEnabled reports whether category is selected in sc.Alerts (or DefaultAlertCategories when nil).
func AlertEnabled(sc config.RoomSoundConfig, category string) bool {
	list := sc.Alerts
	if list == nil {
		list = DefaultAlertCategories
	}
	for _, c := range list {
		if c == category {
			return true
		}
	}
	return false
}

// DefaultTopic returns the MQTT topic prefix for devName.
func DefaultTopic(sc config.RoomSoundConfig, devName string) string {
	if t := strings.Trim(strings.TrimSpace(sc.MQTTTopic), "/"); t != "" {
		return t
	}
	slug := sanitizeNode(devName)
	return "techo5/" + slug + "/sound"
}

func sanitizeNode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "techo5"
	}
	return out
}

// SendHANotification calls the configured Home Assistant notification service (e.g.
// persistent_notification.create or notify.mobile_app_*) over techo5's ESPHome API link.
func SendHANotification(service, devName string, det Detection, rep EngineReply) error {
	service = strings.TrimSpace(service)
	if service == "" {
		return errors.New("no Home Assistant notification service is set")
	}
	if !strings.Contains(service, ".") {
		return errors.New("service must be domain.action, such as notify.notify or persistent_notification.create")
	}
	if devName == "" {
		devName = "TECHO5"
	}
	verb := "heard"
	if det.Category == "person" || det.Category == "motion" {
		verb = "saw"
	}
	title := fmt.Sprintf("%s %s %s", devName, verb, det.Label)
	var msg string
	switch {
	case rep.RMSDBFS == 0:
		msg = fmt.Sprintf("%s (%.0f%% confidence)", det.Label, det.Confidence*100)
	case det.RawClass == "" || det.RawClass == det.Label:
		msg = fmt.Sprintf("%s (%.0f%% confidence, %.1f dBFS)", det.Label, det.Confidence*100, rep.RMSDBFS)
	default:
		msg = fmt.Sprintf("%s (%.0f%% confidence, %.1f dBFS)", det.RawClass, det.Confidence*100, rep.RMSDBFS)
	}
	data := map[string]string{
		"title":   title,
		"message": msg,
	}
	if service == "persistent_notification.create" {
		data["notification_id"] = "techo5_sound_" + sanitizeNode(devName)
	}
	component.CallService.Emit(component.Call{
		Service: service,
		Data:    data,
	})
	return nil
}

// mqttMsg is one publish item in a single short-lived MQTT 3.1.1 session.
type mqttMsg struct {
	topic   string
	payload []byte
	retain  bool
}

// PublishMQTT publishes state/event messages (and optional Home Assistant MQTT Discovery config)
// to an MQTT 3.1.1 broker using only Go's standard library net package.
func PublishMQTT(dial func(network, addr string, timeout time.Duration) (net.Conn, error),
	sc config.RoomSoundConfig, devName string, det Detection, rep EngineReply, includeDiscovery bool) error {

	broker := strings.TrimSpace(sc.MQTTBroker)
	if broker == "" {
		return errors.New("no MQTT broker is set")
	}
	if _, _, err := net.SplitHostPort(broker); err != nil {
		broker = net.JoinHostPort(broker, "1883")
	}
	if devName == "" {
		devName = "TECHO5"
	}
	if dial == nil {
		dial = net.DialTimeout
	}

	prefix := DefaultTopic(sc, devName)
	node := sanitizeNode(devName)

	eventPayload, _ := json.Marshal(map[string]any{
		"category":     det.Category,
		"sound":        det.Label,
		"raw_class":    det.RawClass,
		"confidence":   float64(int(det.Confidence*1000+0.5)) / 1000,
		"rms_dbfs":     float64(int(rep.RMSDBFS*10)) / 10,
		"inference_ms": float64(int(rep.MS*10)) / 10,
		"device":       devName,
		"timestamp":    time.Now().UTC().Format(time.RFC3339),
	})

	var msgs []mqttMsg
	if sc.MQTTDiscovery && includeDiscovery {
		discSensor, _ := json.Marshal(map[string]any{
			"name":                  "Room sound",
			"unique_id":             node + "_room_sound_mqtt",
			"state_topic":           prefix + "/state",
			"json_attributes_topic": prefix + "/event",
			"icon":                  "mdi:waveform",
			"device": map[string]any{
				"identifiers":  []string{"techo5_" + node},
				"name":         devName,
				"manufacturer": "TECHO5",
			},
		})
		msgs = append(msgs, mqttMsg{
			topic:   "homeassistant/sensor/" + node + "_room_sound/config",
			payload: discSensor,
			retain:  true,
		})
	}
	msgs = append(msgs,
		mqttMsg{topic: prefix + "/state", payload: []byte(det.Label), retain: true},
		mqttMsg{topic: prefix + "/event", payload: eventPayload, retain: false},
	)

	return mqttSend(dial, broker, "techo5-"+node, sc.MQTTUser, sc.MQTTPass, msgs)
}

func mqttSend(dial func(string, string, time.Duration) (net.Conn, error),
	addr, clientID, user, pass string, msgs []mqttMsg) error {

	conn, err := dial("tcp", addr, 4*time.Second)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	// Build MQTT 3.1.1 CONNECT packet
	var body bytes.Buffer
	writeMQTTString(&body, "MQTT")
	body.WriteByte(4)     // Protocol Level 4 = MQTT 3.1.1
	var flags byte = 0x02 // Clean Session
	if user != "" {
		flags |= 0x80
		if pass != "" {
			flags |= 0x40
		}
	}
	body.WriteByte(flags)
	body.Write([]byte{0x00, 0x1e}) // KeepAlive = 30s
	writeMQTTString(&body, clientID)
	if user != "" {
		writeMQTTString(&body, user)
		if pass != "" {
			writeMQTTString(&body, pass)
		}
	}

	if err := writeMQTTPacket(conn, 0x10, body.Bytes()); err != nil {
		return err
	}

	var ack [4]byte
	if _, err := io.ReadFull(conn, ack[:]); err != nil {
		return fmt.Errorf("mqtt connack: %w", err)
	}
	if ack[0] != 0x20 || ack[3] != 0x00 {
		return fmt.Errorf("mqtt broker refused connection (code %d)", ack[3])
	}

	for _, m := range msgs {
		var pub bytes.Buffer
		writeMQTTString(&pub, m.topic)
		pub.Write(m.payload)
		hdr := byte(0x30)
		if m.retain {
			hdr |= 0x01
		}
		if err := writeMQTTPacket(conn, hdr, pub.Bytes()); err != nil {
			return err
		}
	}

	// DISCONNECT
	_, _ = conn.Write([]byte{0xe0, 0x00})
	return nil
}

func writeMQTTString(b *bytes.Buffer, s string) {
	n := len(s)
	b.WriteByte(byte(n >> 8))
	b.WriteByte(byte(n))
	b.WriteString(s)
}

func writeMQTTPacket(w io.Writer, hdr byte, body []byte) error {
	var pkt bytes.Buffer
	pkt.WriteByte(hdr)
	rem := len(body)
	for {
		enc := byte(rem % 128)
		rem /= 128
		if rem > 0 {
			enc |= 0x80
		}
		pkt.WriteByte(enc)
		if rem == 0 {
			break
		}
	}
	pkt.Write(body)
	_, err := w.Write(pkt.Bytes())
	return err
}
