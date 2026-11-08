package wifi

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Networks that sign each person in (WPA-EAP, 802.1X): a school's, a college's (eduroam) or a
// workplace's. The radio's supplicant has spoken these all along; what was missing was a way to say
// one here.

// Login is how a device signs in to such a network.
type Login struct {
	// Method is how the password is checked, from the network's own instructions: "peap" (PEAP with
	// MSCHAPv2, the most common, and what "eduroam" usually means), "ttls-pap" or "ttls-mschapv2".
	Method string
	// Identity is the username, often a whole address (name@school.edu).
	Identity string
	Password string
	// Anonymous is the outer identity some networks ask for (anonymous@school.edu); empty for none.
	Anonymous string
	// Server is the name on the network's sign-in server certificate, or the end of it (school.edu),
	// from the network's instructions. The certificate has to come from a known authority and carry
	// that name before the password is sent, so a look-alike network cannot collect it.
	Server string
	// NoCheck leaves the server unchecked, for a network whose instructions say "do not validate" or
	// that use a certificate of their own. Anyone can then put up a network of the same name and
	// collect the password, so it has to be asked for.
	NoCheck bool
}

// Methods are the Login methods, in the order to offer them.
var Methods = []struct{ Name, Label string }{
	{"peap", "PEAP (MSCHAPv2)"},
	{"ttls-pap", "TTLS (PAP)"},
	{"ttls-mschapv2", "TTLS (MSCHAPv2)"},
}

// caBundle is the authorities a server certificate is checked against.
var caBundle = "/etc/ssl/certs/ca-certificates.crt"

// JoinLogin joins a network that signs in with a username and password, as Join does a passphrase
// network: first in the list, the others kept, and the old configuration back if it never comes up.
func JoinLogin(ctx context.Context, ssid string, l Login) error {
	if err := checkName(ssid); err != nil {
		return err
	}
	stanza, err := loginBlock(ssid, l)
	if err != nil {
		return err
	}
	if err := join(ctx, ssid, stanza); err != nil {
		if strings.TrimSpace(l.Server) != "" && !l.NoCheck {
			// Many schools sign their sign-in server with a certificate of their own, which no known
			// authority vouches for: the join then simply never completes, and says nothing of why.
			return fmt.Errorf("%w. Check the username and password; if they are right, the network may use a certificate of its own: try again with the server not checked", err)
		}
		return err
	}
	return nil
}

// loginBlock is the network= stanza for a Login. Every value the person typed goes in as hex, for the
// reason block gives; the supplicant reads identity, anonymous_identity, password and
// domain_suffix_match in either form.
func loginBlock(ssid string, l Login) (string, error) {
	l.Identity, l.Anonymous, l.Server = strings.TrimSpace(l.Identity), strings.TrimSpace(l.Anonymous), strings.TrimSpace(l.Server)
	var eap, phase2 string
	switch l.Method {
	case "peap", "":
		eap, phase2 = "PEAP", "auth=MSCHAPV2"
	case "ttls-pap":
		eap, phase2 = "TTLS", "auth=PAP"
	case "ttls-mschapv2":
		eap, phase2 = "TTLS", "auth=MSCHAPV2"
	default:
		return "", errors.New("wifi: unknown sign-in method " + l.Method)
	}
	switch {
	case l.Identity == "":
		return "", errors.New("wifi: a username is needed")
	case l.Password == "":
		return "", errors.New("wifi: a password is needed")
	case !oneLine(l.Identity) || !oneLine(l.Password) || !oneLine(l.Anonymous) || !oneLine(l.Server):
		return "", errors.New("wifi: a login cannot hold a line ending")
	case len(l.Identity) > 128 || len(l.Password) > 128 || len(l.Anonymous) > 128 || len(l.Server) > 253:
		return "", errors.New("wifi: a login is at most 128 characters")
	case l.Server == "" && !l.NoCheck:
		return "", errors.New("wifi: the sign-in server's name is needed (from the network's instructions), or say not to check it")
	}
	if l.Server != "" {
		if _, err := os.Stat(caBundle); err != nil {
			return "", errors.New("wifi: this device has no certificate authorities to check the server with")
		}
	}
	h := func(s string) string { return hex.EncodeToString([]byte(s)) }
	var b strings.Builder
	b.WriteString("network={\n\tssid=" + h(ssid) + "\n")
	b.WriteString("\tkey_mgmt=WPA-EAP\n\teap=" + eap + "\n")
	b.WriteString("\tidentity=" + h(l.Identity) + "\n")
	if l.Anonymous != "" {
		b.WriteString("\tanonymous_identity=" + h(l.Anonymous) + "\n")
	}
	b.WriteString("\tpassword=" + h(l.Password) + "\n")
	b.WriteString("\tphase2=\"" + phase2 + "\"\n")
	if l.Server != "" {
		b.WriteString("\tca_cert=\"" + caBundle + "\"\n")
		b.WriteString("\tdomain_suffix_match=" + h(l.Server) + "\n")
	}
	b.WriteString("}\n")
	return b.String(), nil
}

// MAC is the Wi-Fi interface's hardware address (aa:bb:cc:dd:ee:ff), empty when it can't be read.
// Many school networks want a device without a web browser registered by it before it is let on.
func MAC() string {
	b, err := os.ReadFile("/sys/class/net/" + iface() + "/address")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
