package announce

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/libp2p/zeroconf/v2"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// Finding the other devices. They advertise themselves the same way they advertise everything else,
// over mDNS, and each one keeps the list it heard. Nothing central, nothing to configure, and a
// device that is switched off simply is not in the list.

const (
	service = "_techo5._tcp"
	domain  = "local."

	// browseFor is how long a look around takes, and browseEvery how often one happens.
	browseFor   = 3 * time.Second
	browseEvery = 2 * time.Minute

	// advertiseRetry is how long to wait before trying again, while the network is still coming up.
	advertiseRetry = 3 * time.Second

	// forgetAfter is how long a device may go unheard before it stops being one of the others. It is
	// several looks' worth: missing one is ordinary and missing three in a row is gone.
	forgetAfter = 3*browseEvery + browseFor
)

// Peer is another device in the house.
type Peer struct {
	Name    string
	Address string
	Port    int
}

// known is one device and when it was last heard from.
type known struct {
	Peer
	at time.Time
}

var peers struct {
	sync.Mutex
	// by lowercase name, so a device that answers twice in one look is still one device.
	by map[string]known
}

// Peers is the other devices in the house.
func Peers() []Peer {
	peers.Lock()
	defer peers.Unlock()

	out := make([]Peer, 0, len(peers.by))
	for _, k := range peers.by {
		out = append(out, k.Peer)
	}
	// A stable order, so the log and the screen do not reshuffle the house every couple of minutes.
	slices.SortFunc(out, func(a, b Peer) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// Run advertises this device and keeps the list of the others.
//
// The advertiser is started once however many times Run is called. Run is supervised and restarts
// on an error, and the advertiser outlives it — it stops with ctx, not with Run — so starting one
// per call stacked up a second and a third registration of the same device on the network.
func (f *Feature) Run(ctx context.Context) error {
	f.once.Do(func() { go f.advertise(ctx) })
	for {
		f.browse(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(browseEvery):
		}
	}
}

// advertise says this device is here, and says it again whenever its addresses change.
func (f *Feature) advertise(ctx context.Context) {
	name := config.Get().Device.Name
	for {
		ips := metrics.Addresses()
		if len(ips) == 0 {
			if !pause(ctx, advertiseRetry) {
				return
			}
			continue
		}
		addrs := make([]string, 0, len(ips))
		for _, ip := range ips {
			addrs = append(addrs, ip.String())
		}
		srv, err := zeroconf.RegisterProxy(name, service, domain, web.Port, name, addrs,
			[]string{"name=" + name}, nil)
		if err != nil {
			slog.Debug("announce: advertising failed", "err", err)
			if !pause(ctx, advertiseRetry) {
				return
			}
			continue
		}
		slog.Info("announce: advertising", "name", name, "port", web.Port)
		for metrics.AddressKey(metrics.Addresses()) == metrics.AddressKey(ips) {
			if !pause(ctx, advertiseRetry) {
				srv.Shutdown()
				return
			}
		}
		srv.Shutdown()
	}
}

// browse listens for the others and replaces the list with what it heard, so a device that has gone
// stops being one of them.
func (f *Feature) browse(ctx context.Context) {
	found := make(chan *zeroconf.ServiceEntry, 16)
	var heard []Peer
	done := make(chan struct{})
	seen := map[string]bool{}
	go func() {
		defer close(done)
		me := config.Get().Device.Name
		for e := range found {
			name := nameOf(e)
			if name == "" || strings.EqualFold(name, me) {
				continue // this device hears itself; it is not one of the others
			}
			addr := addressOf(e)
			if addr == "" {
				continue
			}
			// One device answers more than once - two interfaces, or a record repeated as the
			// browse runs - and a peer listed twice is an announcement played twice in one room.
			if seen[strings.ToLower(name)] {
				continue
			}
			seen[strings.ToLower(name)] = true
			heard = append(heard, Peer{Name: name, Address: addr, Port: e.Port})
		}
	}()

	look, cancel := context.WithTimeout(ctx, browseFor)
	defer cancel()
	// Browse owns the channel: it blocks until the look ends and closes it on the way out. Closing it
	// here as well is a second close, which panics, and the panic landed before the list below was
	// ever assigned — so every device decided it was the only one in the house and played its own
	// announcements to itself. The supervisor caught it and restarted the feature every few seconds,
	// which is why nothing looked broken from outside.
	if err := zeroconf.Browse(look, service, domain, found); err != nil {
		slog.Warn("announce: looking for the other devices failed", "err", err)
		closeQuietly(found)
		<-done
		return
	}
	<-done

	now := time.Now()
	peers.Lock()
	was := len(peers.by)
	if peers.by == nil {
		peers.by = map[string]known{}
	}
	for _, p := range heard {
		peers.by[strings.ToLower(p.Name)] = known{Peer: p, at: now}
	}
	// A device is forgotten only after it has missed several looks in a row, not after one. A look
	// lasts three seconds and a device that is busy, or whose answer is lost the way multicast loses
	// things, misses one now and then: dropping it for that made the house flap between one device
	// and two, and an announcement sent in the gap quietly did not reach it.
	for name, k := range peers.by {
		if now.Sub(k.at) > forgetAfter {
			delete(peers.by, name)
			slog.Info("announce: a device has gone quiet", "name", k.Name, "silent_for", now.Sub(k.at).Round(time.Second))
		}
	}
	count := len(peers.by)
	peers.Unlock()

	if count != was {
		slog.Info("announce: other devices in the house", "count", count, "answered_this_look", len(heard))
	}
}

// closeQuietly closes a channel that may already be closed.
//
// It is only for the failing Browse above. Browse closes the channel when it got far enough to own
// it and leaves it open when it did not — a socket it could not bind — and which of those happened
// is not visible from out here. Leaving it open strands the goroutine reading it; closing it blind
// is the panic this whole feature was losing itself to.
func closeQuietly(ch chan *zeroconf.ServiceEntry) {
	defer func() { _ = recover() }()
	close(ch)
}

func nameOf(e *zeroconf.ServiceEntry) string {
	for _, t := range e.Text {
		if v, ok := strings.CutPrefix(t, "name="); ok {
			return v
		}
	}
	return e.Instance
}

// addressOf is the first routable address the entry carries, IPv4 for preference: an announcement
// goes to one address, not to whichever the resolver feels like.
func addressOf(e *zeroconf.ServiceEntry) string {
	for _, ip := range e.AddrIPv4 {
		if ip.IsGlobalUnicast() {
			return ip.String()
		}
	}
	for _, ip := range e.AddrIPv6 {
		if ip.IsGlobalUnicast() {
			return ip.String()
		}
	}
	return ""
}

func pause(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
