package security

import (
	"errors"
	"unicode"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The camera's login: the user name and password the stream takes (config.CamStream: the setup page or
// Home Assistant's camera_credentials action sets it, feature/camstream).

// CameraLogin is the camera's user name and password; ok is false while no password is set.
func CameraLogin() (user, pass string, ok bool) {
	user, pass = config.Get().CamStream.Login()
	return user, pass, pass != ""
}

// Encrypted is whether Home Assistant's link is encrypted with the device's key, which a login may be
// sent over.
func Encrypted() bool { return encrypted() }

// CheckLogin is what a camera login may be: printable, no spaces, colons, @, / or quotes (the
// rtsp://user:password@ form and the digests split on them), the password at least 12 long, as the
// setup page asks.
func CheckLogin(user, pass string) error {
	ok := func(s string) bool {
		for _, r := range s {
			if !unicode.IsPrint(r) || unicode.IsSpace(r) || r == ':' || r == '@' || r == '/' || r == '"' {
				return false
			}
		}
		return s != ""
	}
	if !ok(user) || !ok(pass) {
		return errors.New("a user name and password with no spaces, colons, @, / or quotes")
	}
	if len(pass) < 12 {
		return errors.New("a password of at least 12 characters")
	}
	return nil
}
