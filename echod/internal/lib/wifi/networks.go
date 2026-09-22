package wifi

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"strings"
)

// The networks the supplicant keeps. A device holds more than one so that it can be set up on one
// network and taken to another — the network of whoever it is being given to, added before it goes —
// and so that joining a new one does not throw away the way back.

// Saved are the network names in the configuration, in the order the supplicant will try them.
func Saved() []string {
	b, err := os.ReadFile(Conf)
	if err != nil {
		return nil
	}
	var out []string
	for _, block := range blocks(string(b)) {
		if ssid := ssidOf(block); ssid != "" {
			out = append(out, ssid)
		}
	}
	return out
}

// Forget takes a network out of the configuration. The one in use can be forgotten, which drops the
// connection: that is what forgetting it means.
func Forget(ctx context.Context, ssid string) error {
	b, err := os.ReadFile(Conf)
	if err != nil {
		return err
	}
	var kept []string
	for _, block := range blocks(string(b)) {
		if ssidOf(block) != ssid {
			kept = append(kept, block)
		}
	}
	if err := os.WriteFile(Conf, []byte(conf(kept)), 0o600); err != nil {
		return err
	}
	_, err = cli(ctx, "reconfigure")
	return err
}

// conf is a whole configuration file: the header the boot scripts expect, then the networks.
func conf(networks []string) string {
	var b strings.Builder
	b.WriteString("ctrl_interface=" + ctrlDir + "\nupdate_config=0\n")
	for _, n := range networks {
		b.WriteString(n)
	}
	return b.String()
}

// block is one network= stanza.
//
// Both values go in as hex, which is the one form wpa_supplicant reads back exactly as it was
// written. A quoted value is not: the supplicant takes the text between the first quote on the line
// and the last one and copies it out as it stands, without looking at backslashes, so a passphrase
// written as "pass\"word" reaches the radio with the backslash still in it and a correct password is
// refused by the access point and reported here as the wrong one. Hex has nowhere for that to happen,
// it is what the Dot's wifi-set has always written, and it leaves the file with no quotes in it at
// all, which is one less thing for the reader below to get wrong.
func block(ssid, passphrase string) string {
	var b strings.Builder
	b.WriteString("network={\n\tssid=" + hex.EncodeToString([]byte(ssid)) + "\n")
	if passphrase == "" {
		b.WriteString("\tkey_mgmt=NONE\n")
	} else {
		b.WriteString("\tpsk=" + psk(ssid, passphrase) + "\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// psk is the key WPA makes of a passphrase and a network name: PBKDF2 over HMAC-SHA1, 4096 rounds,
// the name as the salt, 32 bytes out (IEEE 802.11i). It is the same key the supplicant would derive
// from the passphrase itself, so this is the same network said in a form nothing has to unquote — and
// the passphrase does not have to be kept on the device to say it.
func psk(ssid, passphrase string) string {
	key, err := pbkdf2.Key(sha1.New, passphrase, []byte(ssid), 4096, 32)
	if err != nil {
		// Only the round count and the key length can be wrong here and both are written above, so
		// this does not happen. If it ever did, a network joined by its passphrase beats one not
		// joined at all: quoted, which is verbatim to the last quote and so needs no escaping.
		return `"` + passphrase + `"`
	}
	return hex.EncodeToString(key)
}

// blocks splits a configuration into its network= stanzas, each kept exactly as written, so one this
// code did not write survives being read and put back. A line at a time, which is how the supplicant
// itself reads the file: a stanza opens on a line that is "network={" and closes on a line that is
// "}". Nothing here counts quotes, so a name that holds one cannot swallow the rest of the file.
func blocks(s string) []string {
	var out []string
	var cur []string
	for _, line := range strings.Split(s, "\n") {
		if cur == nil {
			if strings.TrimSpace(line) == "network={" {
				cur = []string{line}
			}
			continue
		}
		cur = append(cur, line)
		if strings.TrimSpace(line) == "}" {
			out = append(out, strings.Join(cur, "\n")+"\n")
			cur = nil
		}
	}
	return out
}

// ssidOf is the name in a stanza, empty when there is none to read. Hex, as block writes it, or a
// quoted string in one written by hand or by an older version of this file — and what the supplicant
// makes of a quoted string is the text between the first quote and the last, with nothing taken out.
func ssidOf(blockText string) string {
	v := setting(blockText, "ssid")
	if strings.HasPrefix(v, `"`) {
		if i := strings.LastIndex(v, `"`); i > 0 {
			return v[1:i]
		}
		return ""
	}
	if b, err := hex.DecodeString(v); err == nil && len(b) > 0 {
		return string(b)
	}
	return ""
}

// setting is what one key is set to in a stanza, empty when it is not there. A line at a time, so
// that looking for ssid does not find bssid instead.
func setting(blockText, key string) string {
	for _, line := range strings.Split(blockText, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && k == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
