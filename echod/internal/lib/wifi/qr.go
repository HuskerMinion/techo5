package wifi

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// The Wi-Fi QR code: the one a phone shows to share its network, and many routers print on a sticker.
// Its text is WIFI: followed by fields, each a letter or two, a colon, a value and a semicolon, and
// one more semicolon at the end:
//
//	WIFI:T:WPA;S:Home;P:hunter2hunter;H:false;;
//
// T is the security (WPA, WEP, SAE, nopass, WPA2-EAP), S the network's name, P the passphrase and H
// whether the network is hidden. A backslash takes the character after it as it is, so a name or a
// passphrase can hold ; , : " and \ itself. A value in double quotes is the text between them.

// QR is a network as a Wi-Fi QR code describes it, ready to join.
type QR struct {
	SSID       string
	Passphrase string // empty for an open network
	Hidden     bool
}

// The ways a code is refused, so that the screen can say each in its own words.
var (
	// ErrQRNotWifi is a code that is not a Wi-Fi code at all: a web address, a Matter code.
	ErrQRNotWifi = errors.New("wifi: this is not a Wi-Fi QR code")
	// ErrQRInvalid is a Wi-Fi code that cannot be read: a field cut short, no network named.
	ErrQRInvalid = errors.New("wifi: this Wi-Fi QR code cannot be read")
	// ErrQRWPA3 is a network that only lets WPA3 (SAE) in, which the device cannot join yet.
	ErrQRWPA3 = errors.New("wifi: this network only takes WPA3, which this device cannot join yet")
	// ErrQREnterprise is a network that signs each person in, whose login is typed on the Wi-Fi page.
	ErrQREnterprise = errors.New("wifi: this network signs in with a username and password: choose it from the list instead")
	// ErrQRWEP is the old WEP security, which the device does not join.
	ErrQRWEP = errors.New("wifi: this network uses WEP, which this device does not join")
)

// ParseQR reads the text of a Wi-Fi QR code. It answers one of the ErrQR errors above for a code it
// will not join, wrapped with what was wrong where that helps, and the reason Join would give for a
// name or passphrase that could not be joined.
func ParseQR(code string) (QR, error) {
	const prefix = "WIFI:"
	if len(code) < len(prefix) || !strings.EqualFold(code[:len(prefix)], prefix) {
		return QR{}, ErrQRNotWifi
	}
	fields, err := qrFields(code[len(prefix):])
	if err != nil {
		return QR{}, err
	}

	q := QR{SSID: fields["S"]}
	switch h := strings.ToLower(fields["H"]); h {
	case "", "false":
	case "true":
		q.Hidden = true
	default:
		return QR{}, fmt.Errorf("%w: H:%s is neither true nor false", ErrQRInvalid, fields["H"])
	}

	_, eap := fields["E"]
	switch t := strings.ToUpper(fields["T"]); {
	case eap || strings.Contains(t, "EAP"):
		return QR{}, ErrQREnterprise
	case t == "NOPASS":
		// Open: a passphrase beside it is ignored, since the network asks for none.
	case t == "WEP":
		return QR{}, ErrQRWEP
	case t == "SAE" || t == "WPA3":
		return QR{}, ErrQRWPA3
	case t == "WPA" || t == "WPA2" || t == "WPA/WPA2" || t == "WPA2/WPA3" || t == "":
		// WPA and WPA2 personal, and a network that takes WPA3 beside WPA2 (Android says WPA for it).
		// No T at all is read by what follows it: open without a passphrase, WPA with one.
		q.Passphrase = fields["P"]
		if t != "" && q.Passphrase == "" {
			return QR{}, fmt.Errorf("%w: a %s network without a passphrase", ErrQRInvalid, fields["T"])
		}
	default:
		return QR{}, fmt.Errorf("%w: unknown security %q", ErrQRInvalid, fields["T"])
	}

	if q.SSID == "" {
		return QR{}, fmt.Errorf("%w: it names no network", ErrQRInvalid)
	}
	if err := checkJoin(q.SSID, q.Passphrase); err != nil {
		return QR{}, err
	}
	return q, nil
}

// qrFields splits what follows WIFI: into its fields, keyed by their names in capitals, every value
// unescaped and unquoted. A field must end in a semicolon: one that does not was cut short, and a
// passphrase cut short is one that never works. A name given twice is refused rather than guessed at.
func qrFields(s string) (map[string]string, error) {
	fields := map[string]string{}
	for i := 0; i < len(s); {
		if s[i] == ';' {
			// The closing semicolon, or an empty field some writers leave: nothing in it.
			i++
			continue
		}
		colon := strings.IndexByte(s[i:], ':')
		if colon <= 0 {
			return nil, fmt.Errorf("%w: a field without a name", ErrQRInvalid)
		}
		name := strings.ToUpper(s[i : i+colon])
		if strings.ContainsAny(name, ";\\\"") {
			return nil, fmt.Errorf("%w: a field without a name", ErrQRInvalid)
		}
		i += colon + 1

		var val strings.Builder
		quoted := i < len(s) && s[i] == '"' // a quote that opens the value, not one escaped
		raw := []bool{}                     // for each byte of val: whether it was written unescaped
		ended := false
		for i < len(s) {
			c := s[i]
			if c == '\\' {
				if i+1 == len(s) {
					return nil, fmt.Errorf("%w: it ends in the middle of an escape", ErrQRInvalid)
				}
				val.WriteByte(s[i+1])
				raw = append(raw, false)
				i += 2
				continue
			}
			i++
			if c == ';' {
				ended = true
				break
			}
			val.WriteByte(c)
			raw = append(raw, true)
		}
		if !ended {
			return nil, fmt.Errorf("%w: the %s field is cut short", ErrQRInvalid, name)
		}
		v := val.String()
		if quoted && len(v) >= 2 && v[len(v)-1] == '"' && raw[len(v)-1] {
			v = v[1 : len(v)-1]
		}
		if _, twice := fields[name]; twice {
			return nil, fmt.Errorf("%w: the %s field is given twice", ErrQRInvalid, name)
		}
		fields[name] = v
	}
	return fields, nil
}

// JoinQR joins the network a Wi-Fi QR code describes, as Join does. A hidden one needs nothing more:
// every network is written so that the supplicant asks for it by name (block).
func JoinQR(ctx context.Context, q QR) error {
	return Join(ctx, q.SSID, q.Passphrase)
}
