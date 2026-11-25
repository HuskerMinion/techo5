package addons

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/addons/catalog"
)

// The firewall: each running add-on's ports, opened on Wi-Fi in a chain of the daemon's own, and only
// while it runs.
//
// The image's firewall (tools/linux/rootfs/usr/local/sbin/techo5-firewall, and the Dot's) drops on
// Wi-Fi anything it does not name, and it runs at boot before the daemon. So the add-ons' chain is
// jumped to from the top of INPUT, ahead of TECHO5's own chain and the drop it ends in, as the Plex
// add-on's README does by hand. The legacy tools are the ones that work (the kernel has no nftables),
// and it has no multiport match, so each port, or range of ports, is a rule of its own. Only wlan0: a
// tailnet reaches the device through a chain of its own, which is for TECHO5's own ports only.
//
// The Dot's firewall flushes INPUT when it is run again, which takes the jump with it, and so does
// anybody running the Show's by hand. check puts the jump and the rules back, and the supervisor calls
// it every minute while an add-on runs.

const (
	chain = "TECHO5-ADDONS"
	iface = "wlan0"
)

var tools = []string{"iptables-legacy", "ip6tables-legacy"}

// runTool runs one iptables command; a variable, for tests.
var runTool = func(tool string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, tool, append([]string{"-w"}, args...)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", tool, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

type firewall struct {
	run func(tool string, args ...string) error

	mu     sync.Mutex
	open   map[string][]catalog.Port // by add-on id: what is open for it now
	broken string                    // what check last could not put back, said once
}

func newFirewall(run func(string, ...string) error) *firewall {
	return &firewall{run: run, open: map[string][]catalog.Port{}}
}

// rule is the rule for one port entry, after the chain name.
func rule(p catalog.Port) []string {
	lo, hi := p.Range()
	port := strconv.Itoa(lo)
	if hi != lo {
		port += ":" + strconv.Itoa(hi)
	}
	return []string{"-p", p.Proto, "--dport", port, "-j", "ACCEPT"}
}

func jump() []string { return []string{"INPUT", "-i", iface, "-j", chain} }

// reset empties the chain of whatever a daemon that went down without closing it left open.
func (f *firewall) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range tools {
		_ = f.run(t, "-F", chain)
	}
	f.open = map[string][]catalog.Port{}
}

// ensure makes the chain and puts the jump to it at the top of INPUT, where they are missing.
func (f *firewall) ensure(tool string) error {
	_ = f.run(tool, "-N", chain) // there already, as often as not
	if f.run(tool, append([]string{"-C"}, jump()...)...) == nil {
		return nil
	}
	return f.run(tool, append([]string{"-I", "INPUT", "1"}, jump()[1:]...)...)
}

// openFor opens an add-on's ports. Every rule is tried, and what failed is said: an add-on whose ports
// could not all be opened still runs, reachable from the device itself, and the card says why it may
// not be from elsewhere.
func (f *firewall) openFor(id string, ports []catalog.Port) error {
	if len(ports) == 0 {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var failed []string
	for _, t := range tools {
		if err := f.ensure(t); err != nil {
			failed = append(failed, err.Error())
			continue
		}
		for _, p := range ports {
			r := append([]string{chain}, rule(p)...)
			if f.run(t, append([]string{"-C"}, r...)...) == nil {
				continue
			}
			if err := f.run(t, append([]string{"-A"}, r...)...); err != nil {
				failed = append(failed, err.Error())
			}
		}
	}
	f.open[id] = ports
	if len(failed) > 0 {
		return fmt.Errorf("the firewall did not open every port: %s", strings.Join(failed, "; "))
	}
	return nil
}

// closeFor takes an add-on's ports out again. Ports are never shared between add-ons (install refuses
// one that asks for another's), so a rule removed here is nobody else's.
func (f *firewall) closeFor(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ports := f.open[id]
	delete(f.open, id)
	for _, t := range tools {
		for _, p := range ports {
			r := append([]string{"-D", chain}, rule(p)...)
			for range 4 { // every copy, should one have gone in twice
				if f.run(t, r...) != nil {
					break
				}
			}
		}
	}
}

// check puts back what something else took out: the jump, which the Dot's firewall takes with it when
// it flushes INPUT, and the rules, should the chain have been flushed. It reports whether anything was
// put back, and what could not be, which is said once (the caller logs it) and not again every minute
// until it changes: a tool missing from an image is missing for good.
func (f *firewall) check() (repaired bool, broken string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.open) == 0 {
		return false, ""
	}
	var failed []string
	put := func(t string, check, fix func() error, what string) {
		if check() == nil {
			return
		}
		if fix() == nil && check() == nil {
			repaired = true
			return
		}
		failed = append(failed, t+" "+what)
	}
	for _, t := range tools {
		put(t, func() error { return f.run(t, append([]string{"-C"}, jump()...)...) },
			func() error { return f.ensure(t) }, "jump to "+chain)
		for _, ports := range f.open {
			for _, p := range ports {
				r := append([]string{chain}, rule(p)...)
				put(t, func() error { return f.run(t, append([]string{"-C"}, r...)...) },
					func() error { return f.run(t, append([]string{"-A"}, r...)...) }, p.String())
			}
		}
	}
	now := strings.Join(failed, ", ")
	if now == f.broken {
		return repaired, ""
	}
	f.broken = now
	return repaired, now
}
