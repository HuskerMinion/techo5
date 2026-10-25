package timezone

import (
	"errors"
	"os"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
)

// A time server of the home's own, for a device that cannot reach the internet's: boot's ntpd asks it
// before the usual ones (techo5-lib.sh t5_ntp_peers, and the Dot's boot.sh). It lives on userdata, one
// name or address in a file, because boot reads it before the daemon runs.
var serverFile = layout.StateDir + "/ntp-server"

// Server is the time server set on the device; empty is the usual internet ones only.
func Server() string {
	b, err := os.ReadFile(serverFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// SetServer saves the time server, or none with an empty one, and has ntpd start again with it.
func SetServer(s string) error {
	s = strings.TrimSpace(s)
	if s == Server() {
		return nil
	}
	if s == "" {
		if err := os.Remove(serverFile); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		if !validServer(s) {
			return errors.New("a time server is a name or an address, like 192.168.1.1 or ntp.lan")
		}
		if err := os.WriteFile(serverFile, []byte(s+"\n"), 0o644); err != nil {
			return err
		}
	}
	restartNTP()
	return nil
}

// validServer is a host name, an IPv4 or an IPv6 address: nothing a shell or ntpd could read as more.
func validServer(s string) bool {
	if len(s) > 253 || strings.HasPrefix(s, "-") {
		return false
	}
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == ':'
		if !ok {
			return false
		}
	}
	return true
}
