package camstream

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// MinPass is the shortest password the camera stream takes: it is the one thing between the network and
// the room.
const MinPass = 12

// CheckLogin is what a camera login may be, wherever it is set from (the setup page, Home Assistant's
// camera_credentials): one line each, no quotes, no colon in the user, and a password of at least
// MinPass characters. An empty password keeps the one set.
func CheckLogin(user, pass string) error {
	if strings.ContainsAny(user+pass, "\r\n\"") {
		return errors.New("the user and password are one line each, with no quotes")
	}
	if strings.Contains(user, ":") {
		return errors.New("the user cannot have a colon in it")
	}
	if pass != "" && utf8.RuneCountInString(pass) < MinPass {
		return fmt.Errorf("the password needs at least %d characters", MinPass)
	}
	return nil
}

// SetLogin checks a login (CheckLogin), saves it, and lets go whoever logged in with the old one.
func SetLogin(user, pass string) error {
	if err := CheckLogin(user, pass); err != nil {
		return err
	}
	if err := config.Set().CamStream().Login(user, pass); err != nil {
		return fmt.Errorf("could not save: %w", err)
	}
	Poke()
	return nil
}
