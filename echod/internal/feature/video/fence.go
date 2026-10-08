//go:build !dot

package video

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// The decoder fetches whatever it is pointed at, and follows names and redirects itself, so checking
// the address first (CheckURL) cannot keep it off the device: a name can resolve to 127.0.0.1, a
// redirect can lead to the device's own address. The kernel can. Everything the decoder's user sends
// out goes through a chain of its own that refuses loopback (127.0.0.0/8, ::1, and the device's own
// addresses, which the kernel carries over loopback too) and link-local (169.254.0.0/16, fe80::/10),
// and leaves the rest of the network as it was: the media servers are on it.
//
// The image's firewall (tools/linux/rootfs/usr/local/sbin/techo5-firewall) makes it at boot for the
// image's own decoder user; this makes sure of it for whichever user the decoder runs as, before each
// start, and the decoder is not started without it.

// fenceChain is the decoder's chain, jumped to from OUTPUT for its user.
const fenceChain = "TECHO5-VIDEO"

// fence makes sure the decoder's user is fenced; a variable for the tests, which have no iptables.
var fence = ensureFence

var fenceMu sync.Mutex

// fenceRules are the chain's rules, IPv4 and IPv6. A connection is refused with a reset, so the
// decoder says at once that it was refused rather than timing out; anything else with an ICMP answer.
var fenceRules = map[string][][]string{
	"iptables-legacy": {
		{"-o", "lo", "-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset"},
		{"-o", "lo", "-j", "REJECT"},
		{"-d", "127.0.0.0/8", "-j", "REJECT"},
		{"-d", "169.254.0.0/16", "-j", "REJECT"},
	},
	"ip6tables-legacy": {
		{"-o", "lo", "-p", "tcp", "-j", "REJECT", "--reject-with", "tcp-reset"},
		{"-o", "lo", "-j", "REJECT"},
		{"-d", "::1/128", "-j", "REJECT"},
		{"-d", "fe80::/10", "-j", "REJECT"},
	},
}

// iptables runs one iptables command; a variable for the tests.
// -w waits for the tables' lock: the firewall at boot, or another start, can be holding it, and
// without it a busy moment would be taken for a fence that cannot be made.
var iptables = func(tool string, args ...string) error {
	out, err := exec.Command(tool, append([]string{"-w"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %v: %s", tool, args, err, clip(string(out), 200))
	}
	return nil
}

// ownerMissing is whether an error from iptables says the kernel has no owner match, which no later
// try will change. What iptables-legacy prints for it: "Extension owner revision 0 not supported,
// missing kernel module?" (the Spot's kernel, measured) or "Couldn't load match `owner'" (no
// extension at all). A bare "No chain/target/match by that name" is not enough: a chain gone missing
// says that too.
func ownerMissing(err error) bool {
	s := err.Error()
	return strings.Contains(s, "Extension owner") || strings.Contains(s, "load match `owner'")
}

// noOwner is the kernel having refused the owner match once: it will not take it later either, so the
// fence is not tried again (guard.go is used instead).
var noOwner error

// ensureFence adds what is missing of the fence for uid, and says whether it is all there.
func ensureFence(uid uint32) error {
	fenceMu.Lock()
	defer fenceMu.Unlock()
	if noOwner != nil {
		return noOwner
	}
	owner := []string{"-m", "owner", "--uid-owner", strconv.FormatUint(uint64(uid), 10), "-j", fenceChain}
	for _, tool := range []string{"iptables-legacy", "ip6tables-legacy"} {
		_ = iptables(tool, "-N", fenceChain) // there already, as often as not
		for _, rule := range fenceRules[tool] {
			if iptables(tool, append([]string{"-C", fenceChain}, rule...)...) == nil {
				continue
			}
			if err := iptables(tool, append([]string{"-A", fenceChain}, rule...)...); err != nil {
				return fmt.Errorf("the decoder cannot be kept off the device: %w", err)
			}
		}
		if iptables(tool, append([]string{"-C", "OUTPUT"}, owner...)...) == nil {
			continue
		}
		if err := iptables(tool, append([]string{"-I", "OUTPUT", "1"}, owner...)...); err != nil {
			err = fmt.Errorf("the kernel cannot match the decoder's packets to its user: %w", err)
			if ownerMissing(err) {
				noOwner = err // for good; anything else is tried again at the next start
			}
			return err
		}
	}
	return nil
}
