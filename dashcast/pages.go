package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"sync"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// Web pages that are not Home Assistant's: a status page, a photo site, a page of your own. Whoever
// runs the server lists them, by name, in DASHCAST_PAGES, and those are the only ones shown: a device
// asks for one by its name and never by an address, so a device's key opens nothing the server's
// owner did not choose.
//
// They open in a browser context of their own, apart from the dashboards' tabs: no Home Assistant
// token, no cookies or storage shared with them. A page is kept on its own site: a top-level
// navigation to another is refused before it happens (keepTopLevel), and one that gets there anyway is
// brought back (keepOnPage); a window it opens is closed as it appears (closePopups). Its scripts
// still run here, on the server, and can reach this machine's network, so list only pages you trust.

// webPage is one page a device may be shown.
type webPage struct {
	name    string
	addr    string        // http or https
	refresh time.Duration // reloaded this often; zero for never
}

// refreshMin is the shortest refresh a page may ask for: a page reloaded every few seconds is a page
// that never finishes loading on a Pi.
const refreshMin = 30 * time.Second

// pageNameMax is the longest name a page may have: it is a choice in a list on the device.
const pageNameMax = 40

// parsePages reads DASHCAST_PAGES: entries separated by commas or new lines, each name=address, with
// |refresh=10m after the address for a page to be reloaded that often.
func parsePages(s string) ([]webPage, error) {
	var out []webPage
	seen := map[string]bool{}
	// An error names the entry by its place in the list or its name, never its address: an address
	// can carry a key.
	n := 0
	for _, entry := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		n++
		name, rest, ok := strings.Cut(entry, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" {
			return nil, fmt.Errorf("entry %d: write a page as name=https://address", n)
		}
		if !pageName(name) {
			return nil, fmt.Errorf("entry %d: a page's name is letters, digits, spaces, '-' and '_', at most %d", n, pageNameMax)
		}
		if seen[strings.ToLower(name)] {
			return nil, fmt.Errorf("%q is listed twice", name)
		}
		seen[strings.ToLower(name)] = true
		addr, opts, _ := strings.Cut(rest, "|")
		addr = strings.TrimSpace(addr)
		u, err := url.Parse(addr)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return nil, fmt.Errorf("%q: its address has to be http:// or https://, with no user in it", name)
		}
		// A browser writes a host's origin in its ASCII form (xn--...), and the page is kept on that.
		for _, c := range u.Hostname() {
			if c > 0x7f {
				return nil, fmt.Errorf("%q: write its host in its ASCII (xn--) form", name)
			}
		}
		p := webPage{name: name, addr: u.String()}
		for _, o := range strings.Split(opts, "|") {
			o = strings.TrimSpace(o)
			if o == "" {
				continue
			}
			v, ok := strings.CutPrefix(o, "refresh=")
			if !ok {
				return nil, fmt.Errorf("%q: %q is not an option a page takes (refresh=10m)", name, o)
			}
			d, err := time.ParseDuration(v)
			if err != nil || d < refreshMin {
				return nil, fmt.Errorf("%q: refresh is a time of at least %s, like 10m", name, refreshMin)
			}
			p.refresh = d
		}
		out = append(out, p)
	}
	return out, nil
}

// pageName is a name a list can show as it is: letters, digits, spaces, '-' and '_'.
func pageName(s string) bool {
	if s == "" || len(s) > pageNameMax {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ' ' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// findPage is the page called name, however it is cased.
func findPage(pages []webPage, name string) (webPage, bool) {
	for _, p := range pages {
		if strings.EqualFold(p.name, strings.TrimSpace(name)) {
			return p, true
		}
	}
	return webPage{}, false
}

// pageNames is every page's name, in the order they were listed.
func pageNames(pages []webPage) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = p.name
	}
	return out
}

// logged is a page's address as the log says it: its site alone. A key to a page is kept in its
// query, and in a shared link's path as often.
func logged(addr string) string {
	u, err := url.Parse(addr)
	if err != nil || u.Host == "" {
		return "?"
	}
	return haOrigin(u.Scheme + "://" + u.Host)
}

// pageOrigin is a page's own site, as a page sees location.origin: the one it is kept on.
func pageOrigin(addr string) string { return haOrigin(addr) }

// onSite is whether a page at addr, as the browser has it, is still on the site home it was opened
// for: an http or https address there, or about:blank or nothing while it loads. Anything else -
// another site, an error page, a data: or blob: document - is taken back.
func onSite(addr, home string) bool {
	if addr == "" || addr == "about:blank" {
		return true
	}
	u, err := url.Parse(addr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return haOrigin(u.Scheme+"://"+u.Host) == home
}

// pageScript is what runs in a web page before its own code: no popups, and no link that opens
// another window.
const pageScript = `(() => {
  window.open = () => null;
  const here = (e) => {
    const el = e.target && e.target.closest ? e.target.closest("a[target], area[target], form[target]") : null;
    if (el) el.removeAttribute("target");
  };
  addEventListener("click", here, true);
  addEventListener("submit", here, true);
})();`

// openPage is a new tab showing p at w by h, in a browser context of its own: nothing of the
// dashboards' sign-in or storage is in it, and it is thrown away with the tab.
func (b *browser) openPage(ctx context.Context, p webPage, w, h int) (context.Context, func(), error) {
	// The context and its tab made here rather than by chromedp's WithNewBrowserContext: headless
	// Chrome opens a tab in a new context only as a window of its own ("no browser is open").
	var bctx cdp.BrowserContextID
	var tid target.ID
	dispose := func() {
		if bctx == "" {
			return
		}
		b.pages.Delete(bctx)
		_ = chromedp.Run(b.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			return target.DisposeBrowserContext(bctx).Do(asBrowser(ctx))
		}))
	}
	if err := chromedp.Run(b.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		ctx = asBrowser(ctx)
		var err error
		if bctx, err = target.CreateBrowserContext().WithDisposeOnDetach(true).Do(ctx); err != nil {
			return err
		}
		tid, err = target.CreateTarget("about:blank").WithBrowserContextID(bctx).WithNewWindow(true).Do(ctx)
		return err
	})); err != nil {
		dispose() // a context made, and no tab in it
		return nil, nil, err
	}
	// Any other window in this context is the page's popup, and is closed (closePopups).
	b.pages.Store(bctx, tid)
	tab, cancelTab := chromedp.NewContext(b.ctx, chromedp.WithTargetID(tid))
	// Closing the tab throws its context away with it: its cookies and storage go too.
	cancel := func() {
		cancelTab()
		dispose()
	}
	stop := context.AfterFunc(ctx, cancel)
	refused := keepTopLevel(tab, tid, pageOrigin(p.addr), p.name)
	err := chromedp.Run(tab,
		chromedp.ActionFunc(func(ctx context.Context) error {
			if _, err := page.AddScriptToEvaluateOnNewDocument(pageScript).Do(ctx); err != nil {
				return err
			}
			if err := watchFields(ctx); err != nil { // as a dashboard's: see field.go
				return err
			}
			// Every document the page loads is looked at before it is fetched (keepTopLevel).
			pattern := []*fetch.RequestPattern{{URLPattern: "*", ResourceType: network.ResourceTypeDocument}}
			if err := fetch.Enable().WithPatterns(pattern).Do(ctx); err != nil {
				return err
			}
			// Nothing a page offers to download is saved on this machine. A browser's own command, for
			// this page's context alone.
			return cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorBehaviorDeny).
				WithBrowserContextID(bctx).Do(asBrowser(ctx))
		}),
		emulation.SetDeviceMetricsOverride(int64(w), int64(h), 1, false),
		emulation.SetTouchEmulationEnabled(true).WithMaxTouchPoints(1),
		emulation.SetFocusEmulationEnabled(true), // as a dashboard's: see open
		emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: "dark"}}),
		chromedp.Navigate(p.addr),
	)
	if err != nil {
		stop()
		cancel()
		if to := refused(); to != "" {
			return nil, nil, fmt.Errorf("its address goes on to %s, another site: list that address in DASHCAST_PAGES", to)
		}
		return nil, nil, err
	}
	return tab, func() { stop(); cancel() }, nil
}

// asBrowser has a command go to the browser itself rather than to a tab: making and throwing away
// contexts, and the download rule, are the browser's.
func asBrowser(ctx context.Context) context.Context {
	return cdp.WithExecutor(ctx, chromedp.FromContext(ctx).Browser)
}

// keepTopLevel refuses, before it is fetched, a document for the page's own window from anywhere but
// its site, a redirect included; a frame inside the page may load what it likes, as frames do. Refused
// as aborted, so the page stays where it was rather than showing an error. The first one refused is
// said in the log.
//
// It returns where the last document refused was going: an address that lands on another site, a
// redirect from http to https or from a bare domain to its www, is refused on its first load, and the
// page fails to open with that said.
func keepTopLevel(tab context.Context, tid target.ID, home, page string) (refused func() string) {
	var mu sync.Mutex
	said := false
	last := ""
	chromedp.ListenTarget(tab, func(ev any) {
		e, ok := ev.(*fetch.EventRequestPaused)
		if !ok {
			return
		}
		go func() {
			ctx, cancel := context.WithTimeout(tab, tabAnswer)
			defer cancel()
			top := string(e.FrameID) == string(tid)
			if !top || e.Request == nil || onSite(e.Request.URL, home) {
				_ = chromedp.Run(ctx, fetch.ContinueRequest(e.RequestID))
				return
			}
			mu.Lock()
			first := !said
			said = true
			last = logged(e.Request.URL)
			mu.Unlock()
			if first {
				slog.Warn("a web page tried to leave its site; refused", "page", page, "to", logged(e.Request.URL))
			}
			_ = chromedp.Run(ctx, fetch.FailRequest(e.RequestID, network.ErrorReasonAborted))
		}()
	})
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

// Taking a page back: looked at every second, and after takeBacks in a row - an address that itself
// lands somewhere else, an http one sent on to https or a bare domain to its www - only every
// awayEvery, said once in the log.
const (
	takeBacks = 3
	awayEvery = 5 * time.Minute
	// pageAnswer is how long the browser has to say where the page is, or to start taking it back.
	pageAnswer = 3 * time.Second
)

// keepOnPage looks every second at where the page is, as the browser has it rather than by asking the
// page, which one busy forever would never answer; takes it back to its address if it has left its
// site; and reloads it every refresh, when it has one.
func keepOnPage(ctx context.Context, tab context.Context, p webPage, name string) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	home := pageOrigin(p.addr)
	tid := chromedp.FromContext(tab).Target.TargetID
	var reload <-chan time.Time
	if p.refresh > 0 {
		r := time.NewTicker(p.refresh)
		defer r.Stop()
		reload = r.C
	}
	back := func() {
		actx, cancel := context.WithTimeout(tab, pageAnswer)
		defer cancel()
		_ = chromedp.Run(actx, chromedp.ActionFunc(func(ctx context.Context) error {
			_, _, _, _, err := page.Navigate(p.addr).Do(ctx)
			return err
		}))
	}
	misses, said := 0, false
	var waitUntil time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-reload:
			slog.Info("reloading a web page", "name", name, "page", p.name)
			back()
			continue
		case <-t.C:
		}
		var info *target.Info
		actx, cancel := context.WithTimeout(tab, pageAnswer)
		_ = chromedp.Run(actx, chromedp.ActionFunc(func(ctx context.Context) error {
			var err error
			info, err = target.GetTargetInfo().WithTargetID(tid).Do(asBrowser(ctx))
			return err
		}))
		cancel()
		if info == nil {
			continue
		}
		if onSite(info.URL, home) {
			misses = 0
			continue
		}
		misses++
		if misses > takeBacks {
			if !said {
				slog.Warn("a web page's address lands on another site: list the address it lands on instead",
					"page", p.name, "lands", logged(info.URL))
				said = true
			}
			if time.Now().Before(waitUntil) {
				continue
			}
			waitUntil = time.Now().Add(awayEvery)
		} else {
			slog.Warn("a web page left its site; taking it back", "name", name, "page", p.name)
		}
		back()
	}
}
