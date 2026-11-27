package dlna

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
	"golang.org/x/net/ipv4"
)

// SSDP: how controllers find the renderer. A controller asks the network who is there (M-SEARCH, to
// the multicast group), and the device answers it directly with where its description is; the device
// also says it is here on its own (NOTIFY alive) when it starts and every so often after, and goodbye
// (NOTIFY byebye) when it goes off.

const (
	ssdpAddr = "239.255.255.250:1900"
	maxAge   = 1800
	// aliveEvery is how often the device says it is here: well inside max-age, and often enough that a
	// controller which never hears the device's answers to its searches (a router that stopped passing
	// the search group on to the device, a phone that drops multicast while it dozes) still lists it in
	// a few minutes rather than a quarter of an hour (#779).
	aliveEvery = 3 * time.Minute
	// sayAgain is the gap before each announcement is sent a second time: multicast over Wi-Fi is not
	// acknowledged, and UPnP asks for each set to go out more than once.
	sayAgain = 300 * time.Millisecond

	deviceType = "urn:schemas-upnp-org:device:MediaRenderer:1"
	avtType    = "urn:schemas-upnp-org:service:AVTransport:1"
	rcType     = "urn:schemas-upnp-org:service:RenderingControl:1"
	cmType     = "urn:schemas-upnp-org:service:ConnectionManager:1"
	serverName = "Linux UPnP/1.0 TECHO5/1.0"
)

// targets are what the device is, as SSDP names it: each is announced, and each is an answer to a search.
func targets() []string {
	return []string{"upnp:rootdevice", udn(), deviceType, avtType, rcType, cmType}
}

func usn(target string) string {
	id := udn()
	if target == id {
		return id
	}
	return id + "::" + target
}

// matching is the targets a search for st is answered with: all of them for ssdp:all, else the one it names.
func matching(st string) []string {
	var answer []string
	for _, t := range targets() {
		if strings.EqualFold(st, "ssdp:all") || strings.EqualFold(st, t) {
			answer = append(answer, t)
		}
	}
	return answer
}

// announce answers searches and says the device is here, until ctx ends; then it says goodbye.
func (f *Feature) announce(ctx context.Context) {
	group, err := net.ResolveUDPAddr("udp4", ssdpAddr)
	if err != nil {
		return
	}
	// Tried again until it works: at boot the network may not be up yet, and a renderer that gave up
	// then would stay unfound until the switch was turned off and on.
	var listen *net.UDPConn
	for wait := 2 * time.Second; ; wait = min(wait*2, time.Minute) {
		listen, err = net.ListenMulticastUDP("udp4", nil, group)
		if err == nil {
			break
		}
		slog.Warn("dlna: listening for searches failed", "err", err, "again in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
	stop := context.AfterFunc(ctx, func() { listen.Close() })
	defer stop()
	slog.Info("dlna: renderer on", "name", friendlyName())

	pc := ipv4.NewPacketConn(listen)
	alongside(func() {
		herald(ctx, func() { rejoin(pc, group) }, func(nts string) { notify(metrics.Addresses(), group, nts) },
			func() time.Duration { return aliveEvery - 20*time.Second + rand.N(40*time.Second) })
	}, func() { respond(ctx, listen) }, heraldWait)
}

// heraldWait is the most going off waits for the goodbyes: two rounds of two, a moment apart, if an
// alive was still going out.
const heraldWait = 2 * time.Second

// alongside runs background while foreground runs, and when foreground returns waits for background
// to finish too, for at most bound. The renderer's announcements run beside its answers, and going
// off waits for their goodbyes: switched off and on again quickly, the next alive must not be sent
// before the last byebye (which would have a controller drop the device it was just told is here), and
// the daemon going down must not exit before they are sent.
func alongside(background, foreground func(), bound time.Duration) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		background()
	}()
	foreground()
	select {
	case <-done:
	case <-time.After(bound):
		slog.Warn("dlna: the goodbyes did not go out in time", "waited", bound)
	}
}

// herald says the device is here (twice, a moment apart) now and every so often until ctx ends, then
// says goodbye the same way. Before each round the search group is joined afresh on every network.
func herald(ctx context.Context, join func(), say func(nts string), every func() time.Duration) {
	twice := func(nts string) {
		say(nts)
		time.Sleep(sayAgain)
		say(nts)
	}
	for {
		join()
		twice("ssdp:alive")
		select {
		case <-ctx.Done():
			twice("ssdp:byebye")
			return
		case <-time.After(every()):
		}
	}
}

// rejoin joins the search group on each network the device is on. It leaves and joins again rather
// than only joining: the join is what tells the network's switches and access points to pass the
// group on to the device, and one that heard it only once at boot, with nothing on the network
// asking again, may forget the device after a few minutes and stop passing it searches.
func rejoin(pc *ipv4.PacketConn, group *net.UDPAddr) {
	for _, ifi := range multicastInterfaces() {
		_ = pc.LeaveGroup(&ifi, group)
		if err := pc.JoinGroup(&ifi, group); err != nil {
			slog.Debug("dlna: joining the search group failed", "interface", ifi.Name, "err", err)
		}
	}
}

// multicastInterfaces is the networks the device announces on: those up, able to multicast, and
// holding one of the device's own IPv4 addresses.
func multicastInterfaces() []net.Interface {
	own := metrics.Addresses()
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var keep []net.Interface
	for _, ifi := range ifs {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		if holdsOne(addrs, own) {
			keep = append(keep, ifi)
		}
	}
	return keep
}

func holdsOne(addrs []net.Addr, own []net.IP) bool {
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.To4() == nil {
			continue
		}
		for _, ip := range own {
			if ip.Equal(n.IP) {
				return true
			}
		}
	}
	return false
}

// respond answers the searches that reach conn until it is closed.
func respond(ctx context.Context, conn *net.UDPConn) {
	buf := make([]byte, 2048)
	allowed := newBucket(repliesPerSecond, replyBurst, time.Now)
	for {
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil {
			if ctx.Err() == nil {
				slog.Warn("dlna: reading searches failed", "err", err)
			}
			return
		}
		s, ok := parseSearch(buf[:n])
		if !ok {
			continue
		}
		answer := matching(s.st)
		if len(answer) == 0 || !local(from.IP) {
			continue
		}
		if !allowed.take() {
			continue // more searches than any home sends: the rest go unanswered
		}
		select {
		case replying <- struct{}{}:
			go func() {
				defer func() { <-replying }()
				reply(ctx, from, answer, s.wait())
			}()
		default: // more searches than any home sends at once: the rest go unanswered
		}
	}
}

// replying bounds the answers waiting to go out: each waits up to a few seconds, and a flood of
// searches must not become a flood of goroutines, or of answers sent at whoever the searches claim
// to be from. Room for several controllers' bursts at once: a phone app and a media server each send a
// handful of searches together, one per kind of thing they look for (#779).
var replying = make(chan struct{}, 32)

// repliesPerSecond and replyBurst bound how fast searches are answered, whoever they claim to be from:
// an answer to a search sent to the device alone goes at once, with no MX wait to spread it, so a
// flood of them, from a sender spoofing another address on the network, would otherwise be a flood of
// answers sent there. A home's controllers send a handful at a time, well inside the burst.
const (
	repliesPerSecond = 20
	replyBurst       = 10
)

// bucket is a token bucket: up to burst at once, refilled at rate a second.
type bucket struct {
	rate, burst float64
	tokens      float64
	last        time.Time
	now         func() time.Time
}

func newBucket(rate, burst float64, now func() time.Time) *bucket {
	return &bucket{rate: rate, burst: burst, tokens: burst, last: now(), now: now}
}

// take is whether there is a token now, and takes it if there is. One goroutine's (respond's).
func (b *bucket) take() bool {
	t := b.now()
	b.tokens = min(b.burst, b.tokens+t.Sub(b.last).Seconds()*b.rate)
	b.last = t
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// local is an address a search may come from: the home network's own ranges, never the internet.
func local(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback()
}

// search is an M-SEARCH: what is looked for (ST), how many seconds answers may be spread over (MX,
// at most 5 as UPnP caps it), and whether it was sent to the device alone rather than to the group.
type search struct {
	st      string
	mx      int
	unicast bool
}

// wait is how long an answer may wait: a random part of MX, so a network of devices does not all
// answer at once; nothing for a search sent to the device alone, which UPnP answers at once.
func (s search) wait() time.Duration {
	if s.unicast {
		return 0
	}
	return time.Duration(rand.Int64N(int64(s.mx) * int64(time.Second) / 2))
}

// parseSearch reads an M-SEARCH. Read by hand, not as HTTP: controllers send them loosely, with bare
// newlines, no blank line at the end, or headers in any case, and each of those is still a search.
func parseSearch(b []byte) (search, bool) {
	lines := strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
	if first := strings.Fields(lines[0]); len(first) == 0 || !strings.EqualFold(first[0], "M-SEARCH") {
		return search{}, false
	}
	h := map[string]string{}
	for _, l := range lines[1:] {
		if strings.TrimSpace(l) == "" {
			break
		}
		k, v, ok := strings.Cut(l, ":")
		if ok {
			h[strings.ToUpper(strings.TrimSpace(k))] = strings.TrimSpace(v)
		}
	}
	if !strings.EqualFold(strings.Trim(h["MAN"], `"`), "ssdp:discover") || h["ST"] == "" {
		return search{}, false
	}
	host := h["HOST"]
	if hh, _, err := net.SplitHostPort(host); err == nil {
		host = hh
	}
	mx, _ := strconv.Atoi(h["MX"])
	return search{
		st:      h["ST"],
		mx:      min(max(mx, 1), 5),
		unicast: host != "" && host != "239.255.255.250",
	}, true
}

// reply answers a search, after wait, once for each target it matched, to where the search came from.
func reply(ctx context.Context, to *net.UDPAddr, answer []string, wait time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(wait):
	}
	conn, err := net.DialUDP("udp4", nil, to)
	if err != nil {
		return
	}
	defer conn.Close()
	host := conn.LocalAddr().(*net.UDPAddr).IP.String()
	for _, t := range answer {
		msg := "HTTP/1.1 200 OK\r\n" +
			fmt.Sprintf("CACHE-CONTROL: max-age=%d\r\n", maxAge) +
			"DATE: " + time.Now().UTC().Format(http.TimeFormat) + "\r\n" +
			"EXT:\r\n" +
			"LOCATION: " + location(host) + "\r\n" +
			"SERVER: " + serverName + "\r\n" +
			"ST: " + t + "\r\n" +
			"USN: " + usn(t) + "\r\n\r\n"
		_, _ = conn.Write([]byte(msg))
	}
}

// notify announces every target from each of the given addresses.
func notify(from []net.IP, group *net.UDPAddr, nts string) {
	for _, ip := range from {
		if ip.To4() == nil {
			continue
		}
		conn, err := net.DialUDP("udp4", &net.UDPAddr{IP: ip}, group)
		if err != nil {
			continue
		}
		for _, t := range targets() {
			msg := "NOTIFY * HTTP/1.1\r\n" +
				"HOST: " + ssdpAddr + "\r\n" +
				fmt.Sprintf("CACHE-CONTROL: max-age=%d\r\n", maxAge) +
				"LOCATION: " + location(ip.String()) + "\r\n" +
				"NT: " + t + "\r\n" +
				"NTS: " + nts + "\r\n" +
				"SERVER: " + serverName + "\r\n" +
				"USN: " + usn(t) + "\r\n\r\n"
			_, _ = conn.Write([]byte(msg))
		}
		conn.Close()
	}
}

func location(host string) string {
	return "http://" + net.JoinHostPort(host, strconv.Itoa(web.Port)) + pathPrefix + "device.xml"
}
