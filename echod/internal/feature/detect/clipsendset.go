package detect

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// Where the sender's settings are changed and shown (clipsend.go does the sending): the setup page's
// form, the select, and the two sensors.

// sendClock is the time the target is stamped with; a test replaces it.
var sendClock = time.Now

// sendEntities are the select that says where clips go and the diagnostic sensors beside it.
type sendEntities struct {
	to     *esphome.Select
	unsent *esphome.TextSensor
	status *esphome.TextSensor
}

func newSendEntities(d *Detect) sendEntities {
	sel := &esphome.Select{
		Base: esphome.Base{
			ObjectID: "send_wake_clips_to",
			DeviceID: component.DeviceMicrophone,
			Name:     "Send wake clips to",
			Icon:     "mdi:cloud-upload-outline",
			Category: esphome.CategoryConfig,
		},
		Options: config.Labels(sendLabels()),
	}
	sel.Set(config.SendLabel(config.SendOff))
	sel.OnCommand = func(label string) {
		target, ok := config.SendTargetOf(label)
		if !ok {
			slog.Warn("send wake clips to: unknown choice", "choice", label)
			sel.Set(config.SendLabel(config.Get().Wake.Send.Target))
			return
		}
		if err := d.SetSendTarget(context.Background(), target); err != nil {
			slog.Warn("send wake clips to: not changed", "err", err)
		}
	}
	return sendEntities{
		to: sel,
		unsent: &esphome.TextSensor{Base: esphome.Base{
			ObjectID: "wake_clips_unsent", DeviceID: component.DeviceMicrophone, Name: "Wake clips unsent",
			Icon: "mdi:cloud-upload-outline", Category: esphome.CategoryDiagnostic,
		}},
		status: &esphome.TextSensor{Base: esphome.Base{
			ObjectID: "wake_clips_send_status", DeviceID: component.DeviceMicrophone, Name: "Wake clip sending",
			Icon: "mdi:cloud-alert-outline", Category: esphome.CategoryDiagnostic,
		}},
	}
}

// sendLabelled is a target the select lists, for config.Labels.
type sendLabelled string

func (t sendLabelled) Label() string { return config.SendLabel(string(t)) }

func sendLabels() []sendLabelled {
	out := make([]sendLabelled, 0, len(config.SendTargets))
	for _, t := range config.SendTargets {
		out = append(out, sendLabelled(t))
	}
	return out
}

// restoreSend shows the saved target, and wakes the sender to work on whatever is waiting.
func (d *Detect) restoreSend(c config.Config) {
	d.send.to.Set(config.SendLabel(c.Wake.Send.Target))
	d.sender.settingsChanged()
}

// publishSend is the sender telling the sensors.
func (d *Detect) publishSend(unsent int, status string) {
	d.send.unsent.Set(fmt.Sprint(unsent))
	d.send.status.Set(status)
}

// Sender is the sender, for the setup page's Test and the unsent count it shows.
func (d *Detect) Sender() *Sender { return &Sender{d.sender} }

// Sender is what the setup page may ask of the sender.
type Sender struct{ s *sender }

// Ping asks the trainer the settings name whether it is there.
func (s *Sender) Ping(ctx context.Context, cfg config.WakeSend) (string, error) {
	return s.s.Ping(ctx, cfg)
}

// Unsent is how many clips wait, and the last thing that went wrong with sending them.
func (s *Sender) Unsent() (int, string) { return s.s.Unsent() }

// Untrusted is a certificate the last Test or send refused for not verifying: its SHA-256, or empty.
func (s *Sender) Untrusted() string { return s.s.Untrusted() }

// Trust pins the certificate with this fingerprint for the saved address, once the trainer is asked again
// and shows that very certificate.
func (d *Detect) Trust(ctx context.Context, fingerprint string) error {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()
	cur := config.Get().Wake.Send
	if !cur.On() || !strings.HasPrefix(cur.URL, "https://") {
		return errors.New("only an https address has a certificate to trust")
	}
	fingerprint = strings.ToLower(strings.TrimSpace(fingerprint))
	cur.Pin = ""
	_, err := d.sender.pingWith(ctx, cur)
	var ce *CertError
	if !errors.As(err, &ce) || ce.Fingerprint != fingerprint {
		return errors.New("the trainer's certificate is not the one that was shown: test it again")
	}
	if err := config.Set().SendUpdate(func(w *config.WakeSend) { w.Pin = fingerprint }); err != nil {
		return fmt.Errorf("could not keep it: %w", err)
	}
	d.sender.mu.Lock()
	d.sender.cert = ""
	d.sender.mu.Unlock()
	d.sender.settingsChanged()
	slog.Info("wake clip sending: a certificate was trusted")
	return nil
}

// KeepOn is whether clips are being kept (Keep wake clips): nothing is sent while they are not. The
// same question the sender asks (clips.kept).
func (d *Detect) KeepOn() bool { return d.clips.kept() }

// SetSend saves where clips go, from the setup page: the address is checked as an address and as a home
// one, and Since, the moment from which clips are sent, is set when the target changes.
func (d *Detect) SetSend(ctx context.Context, next config.WakeSend) error {
	return d.setSend(ctx, func(config.WakeSend) (config.WakeSend, error) { return next, nil })
}

// setSend reads the saved settings, lets change make the new ones from them, and saves those, all under
// sendMu: a save from the setup page and one from the select cannot revert each other.
func (d *Detect) setSend(ctx context.Context, change func(cur config.WakeSend) (config.WakeSend, error)) error {
	d.sendMu.Lock()
	defer d.sendMu.Unlock()
	cur := config.Get().Wake.Send
	next, err := change(cur)
	if err != nil {
		return err
	}
	if next.Target != config.SendOff {
		if !slices.Contains(config.SendTargets, next.Target) {
			return errors.New("that is not a place clips can be sent")
		}
		u, err := CheckSendURL(next.URL)
		if err != nil {
			return err
		}
		next.URL = u
		if err := d.sender.CheckHome(ctx, u); err != nil {
			return err
		}
		// A token saved for another target stays: Tater Trainer is never sent one, and the other is not
		// asked to type it again.
	}
	for _, k := range next.Skip {
		if !slices.Contains(config.SendKinds, k) {
			return fmt.Errorf("%q is not a kind of clip", k)
		}
	}
	next.Pin = cur.Pin
	if next.URL != cur.URL {
		next.Pin = "" // a pin is for the address it was trusted at
	}
	switch {
	case next.Target == config.SendOff:
		next.Since = ""
	case next.Target != cur.Target || cur.Since == "":
		// Only what is kept from now on is sent: a clip kept before anybody said where it may go is not. A
		// device whose clock is not set yet has no moment to say.
		now := sendClock()
		if now.Year() < 2025 {
			return errors.New("the device's clock isn't set yet; try again in a minute")
		}
		next.Since = now.UTC().Format(time.RFC3339)
	default:
		next.Since = cur.Since
	}
	if err := config.Set().SendUpdate(func(w *config.WakeSend) { *w = next }); err != nil {
		return fmt.Errorf("could not keep it: %w", err)
	}
	if cur.Target == config.SendWakesmith && (next.Target != config.SendWakesmith || next.URL != cur.URL) {
		// Takes were recorded for that wakesmith: they don't wait for another target, or another address.
		d.sender.c.dropTakes("Send wake clips to no longer goes to that wakesmith")
	}
	if next.URL != cur.URL {
		// The refused certificate belonged to the old address; Trust must not show it for the new one.
		d.sender.mu.Lock()
		d.sender.cert = ""
		d.sender.mu.Unlock()
	}
	d.send.to.Set(config.SendLabel(next.Target))
	d.sender.settingsChanged()
	slog.Info("wake clip sending set", "target", next.Target, "kinds_skipped", len(next.Skip), "token", next.Token != "")
	return nil
}

// SetSendTarget changes only where clips go, from the select. It needs an address saved first, on the
// setup page.
func (d *Detect) SetSendTarget(ctx context.Context, target string) error {
	err := d.setSend(ctx, func(cur config.WakeSend) (config.WakeSend, error) {
		if target != config.SendOff && cur.URL == "" {
			return cur, errors.New("save the trainer's address on the setup page first, under Voice")
		}
		if target != config.SendOff && !strings.HasPrefix(cur.URL, "http") {
			return cur, errors.New("the saved address is not one clips can be sent to")
		}
		next := cur
		next.Skip = slices.Clone(cur.Skip) // not the saved slice
		next.Target = target
		return next, nil
	})
	if err != nil {
		d.send.to.Set(config.SendLabel(config.Get().Wake.Send.Target))
	}
	return err
}
