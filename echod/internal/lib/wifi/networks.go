package wifi

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha1"
	"encoding/hex"
	"log/slog"
	"os"
	"path/filepath"
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
	if err := writeFile(Conf, conf(kept)); err != nil {
		return err
	}
	// The copy Join keeps must not bring it back either.
	if prev, err := os.ReadFile(Conf + ".prev"); err == nil {
		all := blocks(string(prev))
		var left []string
		for _, block := range all {
			if ssidOf(block) != ssid {
				left = append(left, block)
			}
		}
		if len(left) != len(all) {
			_ = writeFile(Conf+".prev", conf(left))
		}
	}
	_, err = cli(ctx, "reconfigure")
	return err
}

// writeFile replaces a file whole and has it on the flash before it returns: written beside it,
// synced, renamed over it, and the directory synced. A plain WriteFile leaves the new contents in
// memory for half a minute, and the device reboots without a sync (an update, a power cut), so a
// network forgotten on the setup page could come back with the old file, old date and all (#384).
func writeFile(path, body string) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	// Dated before the sync, which then carries the date to the disk with the rest (keptDate).
	if path == Conf {
		if t, ok := keptDate(); ok {
			if err := os.Chtimes(tmp, t, t); err != nil {
				slog.Warn("wifi: the networks saved may not outlast a restart", "err", err)
			}
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
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
