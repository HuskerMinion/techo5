package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"strings"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// browser is the one Chrome every device's tab runs in.
type browser struct {
	cfg    config
	ctx    context.Context // the browser's own; a tab is a child of it
	cancel func()
}

func newBrowser(parent context.Context, cfg config) (*browser, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.DisableGPU,
		// Inside a container Chrome runs as root, which its sandbox refuses; the container is the
		// sandbox there.
		chromedp.NoSandbox,
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
	)
	if path := chromePath(cfg.chrome); path != "" {
		opts = append(opts, chromedp.ExecPath(path))
	}
	actx, acancel := chromedp.NewExecAllocator(parent, opts...)
	bctx, bcancel := chromedp.NewContext(actx, chromedp.WithErrorf(quiet))
	// Starting it now rather than with the first device, so a browser that cannot start says so at
	// once rather than when somebody first opens the page.
	if err := chromedp.Run(bctx); err != nil {
		bcancel()
		acancel()
		return nil, err
	}
	return &browser{cfg: cfg, ctx: bctx, cancel: func() { bcancel(); acancel() }}, nil
}

func (b *browser) close() { b.cancel() }

// chromePath is the browser to run: the one named, or the headless-shell image's, or whatever
// chromedp finds for itself.
func chromePath(named string) string {
	if named != "" {
		return named
	}
	for _, p := range []string{"/headless-shell/headless-shell"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "headless-shell"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// open is a new tab showing path at w by h, signed in to Home Assistant, in the dark theme a screen
// in a room wants. The tab closes with ctx.
func (b *browser) open(ctx context.Context, path string, w, h int, allowed map[string]bool) (context.Context, func(), error) {
	tab, cancel := chromedp.NewContext(b.ctx, chromedp.WithErrorf(quiet))
	stop := context.AfterFunc(ctx, cancel)

	// The frontend keeps its sign-in in local storage; putting a long-lived token there before any
	// of its code runs signs it in without the login page. The sidebar is kept closed for the same
	// reason: a screen this size has no room for it.
	tokens, _ := json.Marshal(map[string]any{
		"access_token": b.cfg.token, "token_type": "Bearer", "expires_in": 1800,
		"hassUrl": b.cfg.ha, "clientId": b.cfg.ha + "/", "expires": 9999999999999, "refresh_token": "",
	})
	quoted, _ := json.Marshal(string(tokens))
	// And the frontend is kept on dashboards: it moves between its pages with the history API, and a
	// move to anywhere not allowed is refused before it happens.
	firsts := make([]string, 0, len(allowed))
	for p := range allowed {
		firsts = append(firsts, p)
	}
	list, _ := json.Marshal(firsts)
	// Only in Home Assistant's own top-level page: a card can frame another page, and one on the same
	// machine would otherwise be handed the token too.
	origin, _ := json.Marshal(haOrigin(b.cfg.ha))
	script := fmt.Sprintf(`(() => {
  if (window.top !== window || location.origin !== %s) return;
  localStorage.setItem("hassTokens", %s); localStorage.setItem("dockedSidebar", '"always_hidden"');
  const allowed = new Set(%s);
  const ok = (u) => {
    try {
      const p = new URL(u, location.href);
      if (p.origin !== location.origin) return false;
      return allowed.has(p.pathname.replace(/^\/+/, "").split("/")[0]);
    } catch (e) { return false; }
  };
  for (const name of ["pushState", "replaceState"]) {
    const real = history[name].bind(history);
    history[name] = (state, title, url) => { if (url === undefined || url === null || ok(url)) return real(state, title, url); };
  }
})();`, origin, quoted, list)

	err := chromedp.Run(tab,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
			return err
		}),
		emulation.SetDeviceMetricsOverride(int64(w), int64(h), 1, false),
		emulation.SetTouchEmulationEnabled(true).WithMaxTouchPoints(1),
		emulation.SetEmulatedMedia().WithFeatures([]*emulation.MediaFeature{{Name: "prefers-color-scheme", Value: "dark"}}),
		chromedp.Navigate(b.cfg.ha+path),
	)
	if err != nil {
		stop()
		cancel()
		return nil, nil, err
	}
	return tab, func() { stop(); cancel() }, nil
}

// haOrigin is Home Assistant's address as a page sees its own origin: the scheme and host in lower
// case, and the port only when it is not the scheme's own - which is how a browser writes
// location.origin, so the two can be compared as strings.
func haOrigin(ha string) string {
	u, err := url.Parse(ha)
	if err != nil {
		return ha
	}
	scheme, host, port := strings.ToLower(u.Scheme), strings.ToLower(u.Hostname()), u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]" // IPv6
	}
	if port != "" {
		return scheme + "://" + host + ":" + port
	}
	return scheme + "://" + host
}

// quiet is where chromedp's own complaints go: events from a newer Chrome than it knows the names of
// ("unhandled node event"), which say nothing about dashcast. Anything else it has to say goes to
// the log at debug.
func quiet(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if strings.Contains(msg, "unhandled") {
		return
	}
	slog.Debug("chromedp", "said", msg)
}
