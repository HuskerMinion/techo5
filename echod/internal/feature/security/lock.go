package security

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The settings lock: a PIN the device asks for before its settings screen opens, so guests and children
// can use the device (the clock, the music, the voice assistant, the call button) without changing it.
// Off until a PIN is set, from Home Assistant (the settings_lock_pin action) or the setup page; the
// "Settings lock" switch shows whether one is set, and turning it off clears it, which is the way back in
// from a forgotten PIN. A right PIN opens the settings for a couple of minutes; wrong ones in a row make
// the device wait longer and longer before it takes another.

const (
	unlockFor   = 2 * time.Minute
	freeTries   = 5 // wrong PINs before the device starts making people wait
	firstWait   = 30 * time.Second
	longestWait = 15 * time.Minute
	pinMin      = 4
	pinMax      = 8
)

var lock struct {
	mu            sync.Mutex
	unlockedUntil time.Time
}

// ErrPIN is a PIN that is not 4 to 8 digits.
var ErrPIN = errors.New("a PIN is 4 to 8 digits")

// LockSet is whether a PIN is set.
func LockSet() bool { return webPages && config.Get().Security.LockPIN != "" }

// Locked is whether the settings screen asks for the PIN now.
func Locked() bool {
	if !LockSet() {
		return false
	}
	lock.mu.Lock()
	defer lock.mu.Unlock()
	return time.Now().After(lock.unlockedUntil)
}

// TryPIN checks a PIN typed on the screen. A right one opens the settings for a while; a wrong one counts
// toward the wait, and while the device is making people wait, wait is how much longer. The count and
// the wait are saved, so a restart does not start them over.
func TryPIN(pin string) (ok bool, wait time.Duration) {
	c := config.Get().Security
	if c.LockPIN == "" {
		return true, 0
	}
	now := time.Now()
	blocked := time.Unix(c.LockBlockedUntil, 0)
	if c.LockBlockedUntil > 0 && now.Before(blocked) {
		return false, blocked.Sub(now)
	}
	// The hashing is the slow part, and is done before the lock is taken: Locked, asked on every
	// frame and tap, does not wait behind it.
	matched := pinMatches(c.LockPIN, pin)
	upgraded := ""
	if matched && !strings.HasPrefix(c.LockPIN, "pbkdf2:") {
		upgraded = hashPIN(pin)
	}
	lock.mu.Lock()
	defer lock.mu.Unlock()
	stored := c.LockPIN
	c = config.Get().Security // the count as it is now: tries are one at a time from the pad, but not only
	if c.LockPIN != stored {
		return false, 0 // the PIN changed while this one was being checked: it is not the PIN now
	}
	if until := time.Unix(c.LockBlockedUntil, 0); c.LockBlockedUntil > 0 && now.Before(until) {
		return false, until.Sub(now) // a wrong try just before this one started the wait
	}
	if matched {
		lock.unlockedUntil = now.Add(unlockFor)
		if upgraded != "" {
			if err := config.Set().Security().LockPIN(upgraded); err != nil {
				slog.Warn("settings lock: keeping the PIN the slow way failed", "err", err)
			}
		}
		if c.LockFails != 0 || c.LockBlockedUntil != 0 {
			saveTries(0, 0)
		}
		slog.Info("settings lock: opened with the PIN")
		return true, 0
	}
	fails := c.LockFails + 1
	slog.Warn("settings lock: a wrong PIN", "in_a_row", fails)
	if fails >= freeTries {
		d := min(firstWait<<min(fails-freeTries, 10), longestWait)
		until := now.Add(d)
		saveTries(fails, until.Unix())
		return false, d
	}
	saveTries(fails, 0)
	return false, 0
}

func saveTries(fails int, until int64) {
	if err := config.Set().Security().LockTries(fails, until); err != nil {
		slog.Error("settings lock: saving the tries failed", "err", err)
	}
}

// Relock locks again now, as the settings screen closes.
func Relock() {
	lock.mu.Lock()
	lock.unlockedUntil = time.Time{}
	lock.mu.Unlock()
}

// SetPIN sets the PIN, or clears it with "".
func (f *Feature) SetPIN(pin string) error {
	pin = strings.TrimSpace(pin)
	saved := ""
	if pin != "" {
		if !validPIN(pin) {
			return ErrPIN
		}
		saved = hashPIN(pin)
	}
	if err := config.Set().Security().LockPIN(saved); err != nil {
		return err
	}
	lock.mu.Lock()
	lock.unlockedUntil = time.Time{}
	lock.mu.Unlock()
	saveTries(0, 0)
	f.lockSw.Set(saved != "")
	slog.Info("settings lock", "on", saved != "")
	f.Changed.Emit(struct{}{})
	return nil
}

func validPIN(pin string) bool {
	if len(pin) < pinMin || len(pin) > pinMax {
		return false
	}
	for _, r := range pin {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// pinRounds is PBKDF2's rounds for a PIN: a few tenths of a second on the device, once per try, so a
// copy of the config is slow to guess a PIN from. A PIN is only digits, so this slows a guesser down
// rather than stops one; the config itself is only readable as root.
const pinRounds = 20_000

// hashPIN is the PIN as it is kept: "pbkdf2:" then a random salt and PBKDF2-SHA-256 of the PIN, in hex.
func hashPIN(pin string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	return "pbkdf2:" + hex.EncodeToString(salt) + ":" + hex.EncodeToString(pinKey(pin, salt))
}

func pinKey(pin string, salt []byte) []byte {
	key, err := pbkdf2.Key(sha256.New, pin, salt, pinRounds, sha256.Size)
	if err != nil {
		return nil
	}
	return key
}

func pinMatches(stored, pin string) bool {
	rest, slow := strings.CutPrefix(stored, "pbkdf2:")
	s, h, ok := strings.Cut(rest, ":")
	if !ok {
		return false
	}
	salt, err1 := hex.DecodeString(s)
	want, err2 := hex.DecodeString(h)
	if err1 != nil || err2 != nil {
		return false
	}
	var got []byte
	if slow {
		got = pinKey(pin, salt)
	} else {
		// A PIN kept by a test build before 1.0: a single SHA-256, taken once and kept again the slow way.
		sum := sha256.Sum256(append(append([]byte{}, salt...), pin...))
		got = sum[:]
	}
	return got != nil && subtle.ConstantTimeCompare(got, want) == 1
}

func (f *Feature) buildLock() {
	f.lockSw = &esphome.Switch{
		Base: esphome.Base{ObjectID: "settings_lock", Name: "Settings lock", Icon: "mdi:lock", Category: esphome.CategoryConfig},
		OnCommand: func(on bool) {
			if on {
				// A PIN comes from the action or the setup page; the switch can only show it, or clear it.
				f.lockSw.Set(LockSet())
				if !LockSet() {
					slog.Warn("settings lock: set a PIN with the settings_lock_pin action first")
				}
				return
			}
			if !encrypted() {
				// As the action: a PIN is not cleared over a link anybody on the network could speak.
				// The setup page clears it too.
				slog.Warn("settings lock: not cleared while Home Assistant's link has no device key")
				f.lockSw.Set(LockSet())
				return
			}
			if err := f.SetPIN(""); err != nil {
				slog.Error("settings lock: clearing the PIN failed", "err", err)
			}
		},
	}
}

// lockAction sets the PIN from Home Assistant: 4 to 8 digits, or empty to clear it.
func (f *Feature) lockAction() *esphome.Action {
	return &esphome.Action{
		Name: "settings_lock_pin",
		Args: []esphome.Arg{{Name: "pin", Type: esphome.ArgString}},
		Run: func(c esphome.Call) (any, error) {
			if !encrypted() {
				return nil, errors.New("settings_lock_pin: the device has no API encryption key; set one first")
			}
			return nil, f.SetPIN(c.String("pin"))
		},
	}
}
