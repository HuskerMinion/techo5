package home

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode"
	"unicode/utf8"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Camera buttons: up to four buttons on a camera's view that each run a Home Assistant action, "Open
// gate" on the gate's camera, "Unlock" on the front door's. The camera page draws them and asks for a
// second tap before running one (feature/display/camera_buttons.go); here is how they are written and
// kept, as home_camera_buttons and the setup page take them: "Open gate=script.open_gate,
// Unlock=lock.unlock:lock.front_door", the entity after the action left off for one that names its own.

const (
	// CameraButtonsMax is how many buttons a camera's view has room for.
	CameraButtonsMax = 4

	// cameraButtonLabelMax is the most a button can say, in characters, so it fits on the bar.
	cameraButtonLabelMax = 24
)

// ParseCameraButtons reads a camera's buttons as home_camera_buttons takes them: items split by commas
// or new lines, each "Label=domain.service" or "Label=domain.service:domain.object". Empty is no
// buttons. One bad item refuses the whole set, saying which by its place and why, so half a set is never
// saved. The item's own text stays out of the error: a refused change is logged, and labels are never.
func ParseCameraButtons(s string) ([]config.CameraButton, error) {
	var out []config.CameraButton
	n := 0
	for _, item := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		n++
		b, err := parseCameraButton(item)
		if err != nil {
			return nil, fmt.Errorf("button %d: %w", n, err)
		}
		out = append(out, b)
	}
	if len(out) > CameraButtonsMax {
		return nil, fmt.Errorf("a camera has room for %d buttons, not %d", CameraButtonsMax, len(out))
	}
	return out, nil
}

// parseCameraButton reads one "Label=domain.service[:domain.object]".
func parseCameraButton(item string) (config.CameraButton, error) {
	label, action, ok := strings.Cut(item, "=")
	label, action = strings.TrimSpace(label), strings.TrimSpace(action)
	if !ok || label == "" || action == "" {
		return config.CameraButton{}, errors.New("write a button as Label=domain.service, or Label=domain.service:domain.object")
	}
	if n := utf8.RuneCountInString(label); n > cameraButtonLabelMax {
		return config.CameraButton{}, fmt.Errorf("a button's label can be %d characters at most, not %d", cameraButtonLabelMax, n)
	}
	if strings.IndexFunc(label, unicode.IsControl) >= 0 || !utf8.ValidString(label) {
		return config.CameraButton{}, errors.New("a button's label is one line of text")
	}
	service, entity, _ := strings.Cut(action, ":")
	service, entity = strings.TrimSpace(service), strings.TrimSpace(entity)
	if !dottedID(service) {
		return config.CameraButton{}, errors.New("the action has to be domain.service, as script.open_gate or lock.unlock")
	}
	if entity != "" && !dottedID(entity) {
		return config.CameraButton{}, errors.New("the entity has to be domain.object, as lock.front_door")
	}
	return config.CameraButton{Label: label, Service: service, Entity: entity}, nil
}

// dottedID is whether s is a Home Assistant id of two parts, "domain.name": lowercase letters, digits and
// underscores, one dot between them.
func dottedID(s string) bool {
	domain, name, ok := strings.Cut(s, ".")
	return ok && idPart(domain) && idPart(name)
}

func idPart(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_') {
			return false
		}
	}
	return true
}

// FormatCameraButtons writes buttons as ParseCameraButtons reads them, on one line.
func FormatCameraButtons(buttons []config.CameraButton) string {
	items := make([]string, 0, len(buttons))
	for _, b := range buttons {
		item := b.Label + "=" + b.Service
		if b.Entity != "" {
			item += ":" + b.Entity
		}
		items = append(items, item)
	}
	return strings.Join(items, ", ")
}

// validCameraEntity is whether camera can be a camera's entity as the list has it: one word.
func validCameraEntity(camera string) bool {
	return camera != "" && len(camera) <= 255 && strings.IndexFunc(camera, unicode.IsSpace) < 0 &&
		strings.IndexFunc(camera, unicode.IsControl) < 0
}

// SetCameraButtons saves the buttons on camera's view, none taking them off, and redraws what shows it.
func SetCameraButtons(camera string, buttons []config.CameraButton) error {
	camera = strings.TrimSpace(camera)
	if !validCameraEntity(camera) {
		return fmt.Errorf("%q: name the camera by its entity as the camera list has it, as camera.front_door", camera)
	}
	if len(buttons) > CameraButtonsMax {
		return fmt.Errorf("a camera has room for %d buttons, not %d", CameraButtonsMax, len(buttons))
	}
	if err := config.Set().Home().CameraButtons(camera, buttons); err != nil {
		return err
	}
	slog.Info("home: camera buttons set", "camera", camera, "count", len(buttons))
	Get().Changed.Emit(struct{}{})
	return nil
}

// SetAllCameraButtons saves every camera's buttons at once, as the setup page has them.
func SetAllCameraButtons(all map[string][]config.CameraButton) error {
	for camera, buttons := range all {
		if !validCameraEntity(camera) {
			return fmt.Errorf("%q: name the camera by its entity as the camera list has it, as camera.front_door", camera)
		}
		if len(buttons) > CameraButtonsMax {
			return fmt.Errorf("a camera has room for %d buttons, not %d", CameraButtonsMax, len(buttons))
		}
	}
	if err := config.Set().Home().AllCameraButtons(all); err != nil {
		return err
	}
	slog.Info("home: camera buttons set", "cameras", len(all))
	Get().Changed.Emit(struct{}{})
	return nil
}

// CameraButtons is the buttons on camera's view, in the order drawn.
func CameraButtons(camera string) []config.CameraButton {
	return config.Get().Home.CameraButtons[camera]
}

// cameraButtonsAction is home_camera_buttons: a camera's buttons, as ParseCameraButtons reads them;
// empty takes that camera's off.
func (f *Feature) cameraButtonsAction() *esphome.Action {
	return &esphome.Action{
		Name: "home_camera_buttons",
		Args: []esphome.Arg{{Name: "camera", Type: esphome.ArgString}, {Name: "buttons", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			buttons, err := ParseCameraButtons(c.String("buttons"))
			if err != nil {
				return nil, err
			}
			return nil, SetCameraButtons(c.String("camera"), buttons)
		},
	}
}
