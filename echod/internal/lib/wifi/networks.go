package wifi

import (
	"context"
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
func block(ssid, passphrase string) string {
	var b strings.Builder
	b.WriteString("network={\n\tssid=\"" + escape(ssid) + "\"\n")
	if passphrase == "" {
		b.WriteString("\tkey_mgmt=NONE\n")
	} else {
		b.WriteString("\tpsk=\"" + escape(passphrase) + "\"\n")
	}
	b.WriteString("}\n")
	return b.String()
}

// blocks splits a configuration into its network= stanzas, each kept exactly as written, so one this
// code did not write survives being read and put back. Quoted values are stepped over rather than
// searched, since a name may hold a brace or an escaped quote of its own.
func blocks(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "network={")
		if i < 0 {
			return out
		}
		s = s[i:]
		end := closingBrace(s[len("network={"):])
		if end < 0 {
			return out
		}
		end += len("network={")
		out = append(out, s[:end+1]+"\n")
		s = s[end+1:]
	}
}

// closingBrace is where the stanza's } is, counting from the character after its {, or -1 when the
// file ends first. Anything inside quotes is skipped, escapes and all.
func closingBrace(s string) int {
	inQuotes := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			if inQuotes {
				i++ // whatever follows a backslash is part of the value
			}
		case '"':
			inQuotes = !inQuotes
		case '}':
			if !inQuotes {
				return i
			}
		}
	}
	return -1
}

// ssidOf is the name in a stanza, empty when there is none to read. The closing quote is the first
// one that is not escaped.
func ssidOf(blockText string) string {
	i := strings.Index(blockText, "ssid=\"")
	if i < 0 {
		return ""
	}
	rest := blockText[i+len("ssid=\""):]
	for j := 0; j < len(rest); j++ {
		switch rest[j] {
		case '\\':
			j++
		case '"':
			return unescape(rest[:j])
		}
	}
	return ""
}

// unescape undoes escape, for a name read back out of the file.
func unescape(s string) string {
	s = strings.ReplaceAll(s, `\"`, `"`)
	return strings.ReplaceAll(s, `\`, `\`)
}
