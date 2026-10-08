//go:build !dot && !spot

package deck

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/zeroconf/v2"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/deckwire"
)

// The computers a deck is paired with, each running the TECHO5 Deck agent. A button press dials the
// agent, asks once and hangs up; in between, each paired computer is asked who it is every so often,
// for the setup page's lists and so a computer that is off grays its buttons.

// checkEvery is how often the paired computers are asked who they are.
const checkEvery = time.Minute

// ComputerState is what was last heard from a paired computer.
type ComputerState struct {
	Hello *deckwire.Hello // nil until it has answered
	Err   string          // why the last ask failed
	At    time.Time
}

type computers struct {
	mu    sync.Mutex
	state map[string]ComputerState
	found []Found
}

// Computers is what was last heard from each paired computer, by name.
func (f *Feature) Computers() map[string]ComputerState {
	f.pcs.mu.Lock()
	defer f.pcs.mu.Unlock()
	out := make(map[string]ComputerState, len(f.pcs.state))
	for k, v := range f.pcs.state {
		out[k] = v
	}
	return out
}

// checkComputers asks every paired computer who it is, all at once.
func (f *Feature) checkComputers() { f.askComputers(deckwire.OpHello) }

// askComputers asks every paired computer op (hello, or refresh to have it read its lists again).
func (f *Feature) askComputers(op string) {
	pcs := config.Get().Deck.Computers
	var wg sync.WaitGroup
	results := make([]ComputerState, len(pcs))
	for i, c := range pcs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = ask(c, op)
		}()
	}
	wg.Wait()
	state := map[string]ComputerState{}
	for i, c := range pcs {
		state[c.Name] = results[i]
	}
	f.pcs.mu.Lock()
	changed := len(state) != len(f.pcs.state)
	for k, v := range state {
		if old, ok := f.pcs.state[k]; !ok || (old.Hello == nil) != (v.Hello == nil) || old.Err != v.Err {
			changed = true
		}
	}
	f.pcs.state = state
	f.pcs.mu.Unlock()
	if changed {
		f.Changed.Emit(struct{}{})
	}
}

func ask(c config.DeckComputer, op string) ComputerState {
	r, err := deckwire.Do(c.Addr, c.Key, deckwire.Request{Op: op})
	st := ComputerState{At: time.Now()}
	switch {
	case errors.Is(err, deckwire.ErrKey):
		st.Err = "the pairing key doesn't match: pair it again"
	case err != nil:
		st.Err = friendly(err)
	default:
		st.Hello = r.Hello
		if st.Hello != nil {
			st.Hello.Apps = clipList(st.Hello.Apps)
			st.Hello.Scripts = clipList(st.Hello.Scripts)
		}
	}
	return st
}

func clipList(s []string) []string {
	if len(s) > deckwire.MaxNames {
		s = s[:deckwire.MaxNames]
	}
	return s
}

// friendly says a connection error the way the setup page says things.
func friendly(err error) string {
	var ne net.Error
	switch {
	case errors.As(err, &ne) && ne.Timeout():
		return "not answering (is it on, with the agent running?)"
	case strings.Contains(err.Error(), "refused"):
		return "the agent isn't running"
	case strings.Contains(err.Error(), "no route") || strings.Contains(err.Error(), "unreachable"):
		return "not on the network"
	}
	return err.Error()
}

// Pair checks that the agent at addr answers to key, then pairs it under the name it gives.
func (f *Feature) Pair(addr, key string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" || strings.ContainsAny(addr, "/ ") {
		return "", errors.New("that is not an address")
	}
	key = strings.TrimSpace(key)
	if len(deckwire.NormalizeKey(key)) < 16 {
		return "", errors.New("that key is too short: it's the 32 letters and digits the agent shows")
	}
	r, err := deckwire.Do(addr, key, deckwire.Request{Op: deckwire.OpHello})
	switch {
	case errors.Is(err, deckwire.ErrKey):
		return "", errors.New("the key doesn't match the one that computer shows")
	case err != nil:
		return "", fmt.Errorf("couldn't reach it: %s", friendly(err))
	case r.Hello == nil || strings.TrimSpace(r.Hello.Name) == "":
		return "", errors.New("it answered, but not as a TECHO5 Deck agent")
	}
	name := strings.TrimSpace(r.Hello.Name)
	if len(name) > 64 {
		name = name[:64]
	}
	// Two computers with the same name would share buttons: the second would quietly take the first's.
	// The same agent at a new address (its key is its own) is a re-pair, not a second computer.
	if old, ok := config.Get().Deck.Computer(name); ok && old.Addr != addr && deckwire.NormalizeKey(old.Key) != deckwire.NormalizeKey(key) {
		return "", fmt.Errorf("a computer called %q is already paired at %s: forget it first, or start this one's agent with -name", name, old.Addr)
	}
	if err := config.Set().Deck().PairComputer(config.DeckComputer{Name: name, Addr: addr, Key: key}); err != nil {
		return "", err
	}
	slog.Info("deck: paired a computer", "name", name)
	f.Reload()
	return name, nil
}

// Found is a computer advertising the agent on the network.
type Found struct{ Name, Addr string }

// Look looks on the network for agents and keeps what it found for LastFound.
func (f *Feature) Look(ctx context.Context) []Found {
	found := FindComputers(ctx)
	f.pcs.mu.Lock()
	f.pcs.found = found
	f.pcs.mu.Unlock()
	return found
}

// LastFound is what the last Look found.
func (f *Feature) LastFound() []Found {
	f.pcs.mu.Lock()
	defer f.pcs.mu.Unlock()
	return slices.Clone(f.pcs.found)
}

// Recheck has the paired computers read their app and script lists again and say them, for the setup
// page's Refresh.
func (f *Feature) Recheck() { f.askComputers(deckwire.OpRefresh) }

// FindComputers looks on the network for agents for a moment.
func FindComputers(ctx context.Context) []Found {
	entries := make(chan *zeroconf.ServiceEntry, 16)
	var out []Found
	done := make(chan struct{})
	go func() {
		defer close(done)
		for e := range entries {
			// IPv4 first; an IPv6 address only when it's a private one the agent takes connections
			// from and can be dialed without a zone (not link-local).
			var ip net.IP
			if len(e.AddrIPv4) > 0 {
				ip = e.AddrIPv4[0]
			} else {
				for _, a := range e.AddrIPv6 {
					if a.IsPrivate() {
						ip = a
						break
					}
				}
			}
			if ip == nil || len(out) >= 50 {
				continue
			}
			addr := ip.String()
			if e.Port != deckwire.Port {
				addr = net.JoinHostPort(addr, strconv.Itoa(e.Port))
			}
			name := strings.ReplaceAll(e.Instance, `\ `, " ")
			if !slices.ContainsFunc(out, func(f Found) bool { return f.Addr == addr }) {
				out = append(out, Found{Name: name, Addr: addr})
			}
		}
	}()
	look, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// Browse closes entries when it returns, however it ends (see announce/peers.go).
	if err := zeroconf.Browse(look, deckwire.Service, "local.", entries); err != nil {
		slog.Info("deck: looking for computers failed", "err", err)
		closeQuietly(entries)
	}
	<-done
	return out
}

// closeQuietly closes a channel Browse may already have closed: it does when it got far enough to own
// it, and doesn't when it failed first.
func closeQuietly(ch chan *zeroconf.ServiceEntry) {
	defer func() { _ = recover() }()
	close(ch)
}

// pcDo is a computer button's press: its request to its computer.
func (f *Feature) pcDo(b config.DeckButton) error {
	c, ok := config.Get().Deck.Computer(b.Computer)
	if !ok {
		return fmt.Errorf("deck: no computer called %q is paired", b.Computer)
	}
	op := map[config.DeckAction]string{config.DeckPCKeys: deckwire.OpKeys, config.DeckPCType: deckwire.OpType,
		config.DeckPCOpen: deckwire.OpOpen, config.DeckPCRun: deckwire.OpRun}[b.Action]
	_, err := deckwire.Do(c.Addr, c.Key, deckwire.Request{Op: op, Arg: b.Value})
	return err
}
