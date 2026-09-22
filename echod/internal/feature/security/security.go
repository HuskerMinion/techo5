// Package security is what the device lets in besides Home Assistant's own link: an SSH server, and
// the camera and screen pages on the web port. Each is a switch in Home Assistant and a row on the
// settings sheet's Security tab, and each starts off.
//
// SSH keys only ever come from Home Assistant (the ssh_keys action), whose link is encrypted with
// the device key: the screen can open or close the server but cannot let anyone new in, and the
// image carries no key at all.
package security

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/component"
	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hook"
)

func init() {
	component.Register(component.Device, Get(), component.Order(12))
}

// recheck is how often a server that should be running is looked for, so one that died comes back.
const recheck = time.Minute

// Feature is the switches and what they control.
type Feature struct {
	ssh, camera, screen *esphome.Switch
	wake                chan struct{}

	// Changed fires when a setting changes or the keys do; listeners must not block.
	Changed hook.Hook[struct{}]
}

// State is what the Security tab shows.
type State struct {
	SSHAvailable bool // this is a slot boot of the Linux image, with dropbear in it
	SSH          bool
	SSHRunning   bool
	Keys         []string // one label per authorized key: its comment, or its type
	Camera       bool
	Screen       bool
	Encrypted    bool // Home Assistant's link has a real key
}

var shared = build()

func Get() *Feature { return shared }

func build() *Feature {
	f := &Feature{wake: make(chan struct{}, 1)}
	sw := func(id, name, icon string, set func(bool)) *esphome.Switch {
		s := &esphome.Switch{Base: esphome.Base{ObjectID: id, Name: name, Icon: icon, Category: esphome.CategoryConfig}}
		s.OnCommand = set
		return s
	}
	f.ssh = sw("ssh", "SSH", "mdi:ssh", f.SetSSH)
	f.camera = sw("camera_web_access", "Camera web access", "mdi:webcam", f.SetCamera)
	f.screen = sw("screen_web_access", "Screen web access", "mdi:monitor-screenshot", f.SetScreen)
	return f
}

func (f *Feature) Name() string { return "security" }

func (f *Feature) Entities() []esphome.Entity {
	var out []esphome.Entity
	if sshAvailable() {
		out = append(out, f.ssh)
	}
	if webPages {
		out = append(out, f.camera, f.screen)
	}
	return out
}

func (f *Feature) Restore(c config.Config) {
	f.ssh.Set(c.Security.SSH)
	f.camera.Set(c.Security.Camera)
	f.screen.Set(c.Security.Screen)
}

// Run keeps the SSH server matching the switch.
func (f *Feature) Run(ctx context.Context) error {
	t := time.NewTicker(recheck)
	defer t.Stop()
	for {
		f.settleSSH()
		select {
		case <-ctx.Done():
			return nil
		case <-f.wake:
		case <-t.C:
		}
	}
}

func (f *Feature) settleSSH() {
	if !sshAvailable() {
		return
	}
	want := config.Get().Security.SSH
	running := sshRunning()
	switch {
	case want && !running:
		if len(readKeys()) == 0 {
			return // nobody could log in; opening the port would only be a port
		}
		if !encrypted() {
			slog.Warn("ssh: not starting while Home Assistant's link has no device key")
			return
		}
		if err := startSSH(); err != nil {
			slog.Error("ssh: start failed", "err", err)
			return
		}
		slog.Info("ssh: listening", "port", 22)
		f.Changed.Emit(struct{}{})
	case !want && running:
		if err := stopSSH(); err != nil {
			slog.Error("ssh: stop failed", "err", err)
			return
		}
		slog.Info("ssh: stopped; open sessions stay until they end")
		f.Changed.Emit(struct{}{})
	}
}

// SetSSH, SetCamera and SetScreen are the switches, from Home Assistant or the screen.
func (f *Feature) SetSSH(on bool) {
	// Without a device key the link is plain text, and anyone on the network who connected first
	// could turn root's SSH on (and send the key, below). Off is always allowed.
	if on && !encrypted() {
		slog.Warn("ssh: refused to switch on while Home Assistant's link has no device key")
		f.ssh.Set(false)
		f.Changed.Emit(struct{}{})
		return
	}
	f.set(f.ssh, on, config.Set().Security().SSH)
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *Feature) SetCamera(on bool) { f.set(f.camera, on, config.Set().Security().Camera) }
func (f *Feature) SetScreen(on bool) { f.set(f.screen, on, config.Set().Security().Screen) }

func (f *Feature) set(s *esphome.Switch, on bool, save func(bool) error) {
	s.Set(on)
	if err := save(on); err != nil {
		slog.Error("saving a setting failed", "setting", s.ObjectID, "err", err)
	}
	slog.Info("security", "setting", s.ObjectID, "on", on)
	f.Changed.Emit(struct{}{})
}

// State is read by the screen each frame; a few small file reads.
func (f *Feature) State() State {
	c := config.Get().Security
	st := State{SSHAvailable: sshAvailable(), SSH: c.SSH, Camera: c.Camera, Screen: c.Screen, Encrypted: encrypted()}
	if st.SSHAvailable {
		st.SSHRunning = sshRunning()
		for _, k := range readKeys() {
			st.Keys = append(st.Keys, keyLabel(k))
		}
	}
	return st
}

// Actions: ssh_keys replaces the authorized keys, one per line; an empty value removes them all.
func (f *Feature) Actions() []*esphome.Action {
	if !sshAvailable() {
		return nil
	}
	return []*esphome.Action{{
		Name: "ssh_keys",
		Args: []esphome.Arg{{Name: "keys", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			if !encrypted() {
				slog.Warn("ssh: keys refused while Home Assistant's link has no device key")
				return nil, errors.New("ssh_keys: the device has no API encryption key; set one first")
			}
			keys, err := parseKeys(c.String("keys"))
			if err != nil {
				slog.Warn("ssh: keys refused, the old ones stay", "err", err)
				return nil, err
			}
			if err := writeKeys(keys); err != nil {
				return nil, err
			}
			slog.Info("ssh: authorized keys replaced", "count", len(keys))
			// The keys are saved and this action has succeeded whatever follows. What it cannot
			// promise on its own is that dropbear reads the file they went into.
			ensureDropbearSees(keys)
			if len(keys) == 0 && sshRunning() {
				_ = stopSSH()
			}
			f.Changed.Emit(struct{}{})
			select {
			case f.wake <- struct{}{}:
			default:
			}
			return nil, nil
		},
	}}
}

// keyTypes are the public key formats dropbear accepts.
var keyTypes = []string{"ssh-ed25519", "ssh-rsa", "ecdsa-sha2-nistp256", "ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521", "sk-ssh-ed25519@openssh.com", "sk-ecdsa-sha2-nistp256@openssh.com"}

// parseKeys checks each non-empty line is a public key and nothing else: no options in front, which
// could run a command, and never a private key pasted by mistake.
func parseKeys(s string) ([]string, error) {
	var out []string
	for _, line := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.Contains(line, "PRIVATE KEY") {
			return nil, errors.New("ssh_keys: that is a private key; send the .pub file's line")
		}
		fields := strings.Fields(line)
		ok := len(fields) >= 2
		if ok {
			ok = false
			for _, t := range keyTypes {
				if fields[0] == t {
					ok = true
				}
			}
		}
		if !ok {
			return nil, fmt.Errorf("ssh_keys: not a public key line: %.24q…", line)
		}
		out = append(out, strings.Join(fields, " "))
	}
	return out, nil
}

// keyLabel is how a key is named on the screen.
func keyLabel(k string) string {
	fields := strings.Fields(k)
	if len(fields) >= 3 {
		return strings.Join(fields[2:], " ")
	}
	return fields[0]
}

// encrypted reports whether the Home Assistant link has a key of its own rather than the reserved
// all-zeros one an unprovisioned device answers with.
func encrypted() bool {
	b, err := os.ReadFile(layout.KeyPath)
	if err != nil {
		return false
	}
	k, err := esphome.ParsePSK(strings.TrimSpace(string(b)))
	return err == nil && !k.IsZero()
}
