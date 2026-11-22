package wifi

import (
	"context"
	"regexp"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/lib/redact"
)

// Evidence is what is worth keeping about the link at the moment something is done to it: the
// supplicant's state and ciphers, and the kernel's lines about the security handshakes, where a
// group-key rekey shows as a short handshake long after the first (kept as they are logged, kmsg.go). It goes to the daemon's log, which
// is what a diagnostics download carries, so nothing that names the network or a device is kept:
// no SSID, no BSSID, no addresses.
func Evidence(ctx context.Context) []string {
	var out []string
	if st, err := cli(ctx, "status"); err == nil {
		out = append(out, "supplicant: "+statusEvidence(st))
	}
	FollowKernel()
	return append(out, kernelLines()...)
}

// statusKeys are the lines of wpa_cli status worth keeping; everything else names something.
var statusKeys = []string{"wpa_state", "freq", "key_mgmt", "pairwise_cipher", "group_cipher"}

func statusEvidence(status string) string {
	var kept []string
	for _, line := range strings.Split(status, "\n") {
		k, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		for _, want := range statusKeys {
			if k == want {
				kept = append(kept, strings.TrimSpace(line))
			}
		}
	}
	return strings.Join(kept, " ")
}

// kernelEvidence is the last n kernel lines about the link's security and carrier, with the network's
// name taken out of them.
func kernelEvidence(log string, n int) []string {
	var kept []string
	for _, line := range strings.Split(log, "\n") {
		if strings.Contains(line, "RSN") || strings.Contains(line, "netif_carrier") || strings.Contains(line, "GTK") {
			// The redactor as well as the name: the driver's lines can carry the access point's
			// address and the device's own, and this goes to the log as it is.
			kept = append(kept, "kernel: "+redact.New().Text(redactSSID(strings.TrimSpace(line))))
		}
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return kept
}

// A name in the kernel's brackets runs to the bracket, spaces and all; anywhere else, to the end of the
// line, since a name can hold any character and it is better to lose the rest of a line than leak it.
var (
	bracketedSSID = regexp.MustCompile(`(?i)\[ssid:[^\]]*\]`)
	looseSSID     = regexp.MustCompile(`(?i)ssid[:=].*$`)
)

func redactSSID(s string) string {
	s = bracketedSSID.ReplaceAllString(s, "[ssid:<ssid>]")
	if !strings.Contains(s, "ssid:<ssid>") {
		s = looseSSID.ReplaceAllString(s, "ssid:<ssid>")
	}
	return s
}
