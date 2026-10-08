// techo5-deck is the TECHO5 Deck agent: run on a computer, it lets a paired Echo Show's deck buttons
// press shortcuts and media keys, type text, open apps and websites, and run the scripts the
// computer's owner lists, over the local network (docs/deck.md).
//
// Pairing a Show gives it the run of this computer's signed-in session: a deck that can press keys and
// type can open a terminal and type into it. The script list and the app list are what buttons pick
// from, not a fence. Pair only Shows you trust, and keep their setup page behind the settings lock.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/libp2p/zeroconf/v2"

	"github.com/HuskerMinion/techo5/echod/internal/lib/deckwire"
)

// version is set at build time.
var version = "dev"

func main() {
	port := flag.Int("port", deckwire.Port, "the port to listen on")
	name := flag.String("name", "", "the name the Show lists this computer by (default: the computer's name)")
	dir := flag.String("dir", "", "where the key and the script list are kept (default: your settings folder)")
	newKey := flag.Bool("new-key", false, "make a new pairing key; every paired Show has to be paired again")
	startup := flag.String("startup", "", `"on" to start when you sign in, "off" to stop that`)
	flag.Parse()

	if *startup != "" {
		// The other options given with -startup are the ones it starts with.
		var keep []string
		flag.Visit(func(f *flag.Flag) {
			v := f.Value.String()
			if f.Name == "dir" && v != "" {
				// Started at sign-in, it runs from another folder.
				if abs, err := filepath.Abs(v); err == nil {
					v = abs
				}
			}
			if f.Name != "startup" && f.Name != "new-key" {
				keep = append(keep, "-"+f.Name+"="+v)
			}
		})
		if err := setStartup(*startup == "on", keep); err != nil {
			log.Fatalf("start at sign-in: %v", err)
		}
		fmt.Printf("Start at sign-in: %s.\n", *startup)
		return
	}

	home, err := agentDir(*dir)
	if err != nil {
		log.Fatal(err)
	}
	key, err := loadKey(filepath.Join(home, "key"), *newKey)
	if err != nil {
		log.Fatal(err)
	}
	scripts := filepath.Join(home, "scripts.txt")
	if err := ensureScriptsFile(scripts); err != nil {
		log.Fatal(err)
	}
	if *name == "" {
		*name, _ = os.Hostname()
	}

	a := &agent{name: *name, scriptsPath: scripts, keys: make(chan struct{}, 1)}
	a.refresh()

	l, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		log.Fatalf("listening on port %d: %v (is another agent running?)", *port, err)
	}
	if srv, err := zeroconf.Register(*name, deckwire.Service, "local.", *port, []string{"v=1", "os=" + runtime.GOOS}, nil); err == nil {
		defer srv.Shutdown()
	} else {
		log.Printf("not advertised on the network (%v); add this computer on the Show by its address", err)
	}

	fmt.Printf(`TECHO5 Deck agent %s

  Computer:     %s
  Pairing key:  %s
  Listening:    port %d, your local network only
  Scripts:      %s (%d)
  Keys:         %s

On the Show's setup page, Screen & Photos -> Deck -> Computers: pick this computer and type the key.
Leave this window open while you use the deck.

`, version, *name, key, *port, scripts, len(a.hello().Scripts), keysNote())

	// Eight connections at once, four from any one address: a host on the network that opens and holds
	// connections can't keep the paired Shows out, and a Show pressing quickly isn't turned away.
	sem := make(chan struct{}, 8)
	var perMu sync.Mutex
	per := map[string]int{}
	for {
		c, err := l.Accept()
		if err != nil {
			log.Fatal(err)
		}
		if !local(c.RemoteAddr()) {
			log.Printf("refused %s: not on the local network", c.RemoteAddr())
			c.Close()
			continue
		}
		from := hostOf(c.RemoteAddr())
		perMu.Lock()
		busy := per[from] >= 4
		if !busy {
			select {
			case sem <- struct{}{}:
				per[from]++
			default:
				busy = true
			}
		}
		perMu.Unlock()
		if busy {
			log.Printf("refused %s: too many connections at once", from)
			c.Close()
			continue
		}
		go func() {
			defer func() {
				perMu.Lock()
				if per[from]--; per[from] <= 0 {
					delete(per, from)
				}
				perMu.Unlock()
				<-sem
			}()
			a.serve(c, key)
		}()
	}
}

// agentDir is where the key and the script list live, made if missing.
func agentDir(dir string) (string, error) {
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		dir = filepath.Join(base, "TECHO5 Deck")
	}
	return dir, os.MkdirAll(dir, 0o700)
}

// loadKey reads the pairing key, making one the first time (or when asked for a new one).
func loadKey(path string, fresh bool) (string, error) {
	if !fresh {
		if b, err := os.ReadFile(path); err == nil {
			if k := strings.TrimSpace(string(b)); len(deckwire.NormalizeKey(k)) >= 16 {
				_ = os.Chmod(path, 0o600) // a key copied in from elsewhere may have come readable to all
				return k, nil
			}
		}
	}
	k, err := deckwire.NewKey()
	if err != nil {
		return "", err
	}
	return k, os.WriteFile(path, []byte(k+"\n"), 0o600)
}

// local is whether a connection comes from this computer or the local network: private, link-local
// and loopback addresses. The agent is not for anything farther away.
func local(a net.Addr) bool {
	tcp, ok := a.(*net.TCPAddr)
	if !ok {
		return false
	}
	ip, ok := netip.AddrFromSlice(tcp.IP)
	if !ok {
		return false
	}
	ip = ip.Unmap()
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()
}

type agent struct {
	name        string
	scriptsPath string

	mu        sync.Mutex
	scripts   []script
	scriptsAt time.Time         // the list's modified time when it was read
	apps      map[string]string // name -> what opens it
	appList   []string

	// keys is held (a token in it) while keys are pressed or text typed, so two presses don't
	// interleave (shift held for a capital turning a ctrl+c into ctrl+shift+c); queued counts the
	// typing waiting for it, refreshing whether the app list is being read.
	keys       chan struct{}
	queued     atomic.Int32
	refreshing atomic.Bool
}

// takeKeys waits up to wait for the keys (forever when wait is 0), and says whether it got them.
func (a *agent) takeKeys(wait time.Duration) bool {
	if wait == 0 {
		a.keys <- struct{}{}
		return true
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case a.keys <- struct{}{}:
		return true
	case <-t.C:
		return false
	}
}

func (a *agent) giveKeys() { <-a.keys }

// loadScripts reads the script list again when it changed since it was last read, so a line added
// and saved is there for the next press without restarting the agent.
func (a *agent) loadScripts(force bool) {
	st, err := os.Stat(a.scriptsPath)
	if err != nil {
		// Deleted: nothing runs until it's back.
		a.mu.Lock()
		a.scripts, a.scriptsAt = nil, time.Time{}
		a.mu.Unlock()
		return
	}
	a.mu.Lock()
	same := !force && st.ModTime().Equal(a.scriptsAt)
	a.mu.Unlock()
	if same {
		return
	}
	err = checkPrivate(st, "chmod 600 the file")
	dir := filepath.Dir(a.scriptsPath)
	if dst, derr := os.Stat(dir); err == nil && derr == nil {
		// The folder too: whoever can write to it can put another list in its place.
		if err = checkPrivate(dst, "chmod go-w the folder"); err != nil {
			err = fmt.Errorf("its folder %s: %w", dir, err)
		}
	}
	if err != nil {
		log.Printf("not using %s: %v", a.scriptsPath, err)
		a.mu.Lock()
		a.scripts, a.scriptsAt = nil, st.ModTime()
		a.mu.Unlock()
		return
	}
	scripts, err := readScripts(a.scriptsPath)
	if err != nil {
		log.Printf("reading %s: %v", a.scriptsPath, err)
	}
	a.mu.Lock()
	a.scripts, a.scriptsAt = scripts, st.ModTime()
	a.mu.Unlock()
}

func (a *agent) refresh() {
	a.loadScripts(true)
	apps := listApps()
	names := make([]string, 0, len(apps))
	for n := range apps {
		names = append(names, n)
	}
	sortFold(names)
	if len(names) > deckwire.MaxNames {
		names = names[:deckwire.MaxNames]
	}
	a.mu.Lock()
	a.apps, a.appList = apps, names
	a.mu.Unlock()
}

func (a *agent) hello() deckwire.Hello {
	a.mu.Lock()
	defer a.mu.Unlock()
	h := deckwire.Hello{Name: a.name, OS: runtime.GOOS, Version: version, Keys: canPressKeys(), Apps: a.appList}
	for _, s := range a.scripts {
		h.Scripts = append(h.Scripts, s.name)
	}
	return h
}

// serve answers one Show's requests until it hangs up. Requests are few and far between (a finger on
// a button), so a Show sending them faster than a few a second is held back.
func (a *agent) serve(c net.Conn, key string) {
	defer c.Close()
	conn, err := deckwire.Accept(c, key)
	if err != nil {
		log.Printf("%s: %v", c.RemoteAddr(), err)
		return
	}
	from := hostOf(c.RemoteAddr())
	last := time.Time{}
	for {
		var req deckwire.Request
		if err := conn.Receive(&req, 2*time.Minute); err != nil {
			return
		}
		if wait := 100*time.Millisecond - time.Since(last); wait > 0 {
			time.Sleep(wait)
		}
		last = time.Now()
		reply := a.do(req)
		if req.Op != deckwire.OpHello {
			// Typed text can be anything, a password included: the log says only how much.
			what := req.Arg
			if req.Op == deckwire.OpType {
				what = fmt.Sprintf("(%d characters)", len([]rune(req.Arg)))
			}
			if reply.OK {
				log.Printf("%s: %s %s", from, req.Op, what)
			} else {
				log.Printf("%s: %s %s: %s", from, req.Op, what, reply.Error)
			}
		}
		if conn.Send(reply) != nil {
			return
		}
	}
}

func hostOf(a net.Addr) string {
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return h
}

func (a *agent) do(req deckwire.Request) deckwire.Reply {
	if len(req.Arg) > deckwire.MaxArg {
		return deckwire.Reply{Error: "too long"}
	}
	fail := func(err error) deckwire.Reply {
		if err != nil {
			return deckwire.Reply{Error: err.Error()}
		}
		return deckwire.Reply{OK: true}
	}
	switch req.Op {
	case deckwire.OpHello:
		a.loadScripts(false)
		h := a.hello()
		return deckwire.Reply{OK: true, Hello: &h}
	case deckwire.OpRefresh:
		// The script list now; the app list after answering, since asking Windows for it can take
		// longer than the Show waits.
		a.loadScripts(true)
		if a.refreshing.CompareAndSwap(false, true) {
			go func() {
				defer a.refreshing.Store(false)
				a.refresh()
			}()
		}
		h := a.hello()
		return deckwire.Reply{OK: true, Hello: &h}
	case deckwire.OpKeys:
		combo, err := parseCombo(req.Arg)
		if err != nil {
			return fail(err)
		}
		// A press waits a moment for typing to finish, not minutes: by then the Show has given up,
		// and the keys would land in whatever window is in front.
		if !a.takeKeys(3 * time.Second) {
			return fail(errors.New("still typing: try again in a moment"))
		}
		defer a.giveKeys()
		return fail(pressCombo(combo))
	case deckwire.OpType:
		// Checked now and typed after answering: a long text takes longer to type than the Show waits,
		// and a press that seems to fail gets pressed again.
		if req.Arg == "" {
			return fail(errors.New("nothing to type"))
		}
		if err := checkType(req.Arg); err != nil {
			return fail(err)
		}
		if a.queued.Add(1) > 4 {
			a.queued.Add(-1)
			return fail(errors.New("still typing the last few"))
		}
		go func() {
			defer a.queued.Add(-1)
			a.takeKeys(0)
			defer a.giveKeys()
			if err := typeText(req.Arg); err != nil {
				log.Printf("typing failed: %v", err)
			}
		}()
		return deckwire.Reply{OK: true}
	case deckwire.OpOpen:
		if isWebAddress(req.Arg) {
			return fail(openURL(req.Arg))
		}
		a.mu.Lock()
		target, ok := a.apps[req.Arg]
		a.mu.Unlock()
		if !ok {
			return fail(fmt.Errorf("no app called %q here", req.Arg))
		}
		return fail(openApp(target))
	case deckwire.OpRun:
		a.loadScripts(false)
		a.mu.Lock()
		var cmd string
		for _, s := range a.scripts {
			if s.name == req.Arg {
				cmd = s.command
			}
		}
		a.mu.Unlock()
		if cmd == "" {
			return fail(fmt.Errorf("no script called %q in %s", req.Arg, filepath.Base(a.scriptsPath)))
		}
		return fail(runScript(cmd))
	}
	return deckwire.Reply{Error: "not something this agent does"}
}

// isWebAddress is whether s is an http or https address with a host, the only kind the agent opens as
// one: no control characters, no spaces, nothing invisible.
func isWebAddress(s string) bool {
	for _, r := range s {
		if r <= ' ' || r == 0x7f || r == '"' || !unicode.IsPrint(r) {
			return false
		}
	}
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

func keysNote() string {
	if canPressKeys() {
		return "can press keys and type"
	}
	switch runtime.GOOS {
	case "darwin":
		return "not allowed yet: System Settings > Privacy & Security > Accessibility, turn on this agent (or the Terminal it runs in), then start it again"
	case "linux":
		return "not allowed to make a keyboard yet: see the Linux part of the Deck guide (the uinput group)"
	}
	return "can't press keys on this system"
}
