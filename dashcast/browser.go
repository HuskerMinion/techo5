package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

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
	bctx, bcancel := chromedp.NewContext(actx)
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
func (b *browser) open(ctx context.Context, path string, w, h int) (context.Context, func(), error) {
	tab, cancel := chromedp.NewContext(b.ctx)
	stop := context.AfterFunc(ctx, cancel)

	// The frontend keeps its sign-in in local storage; putting a long-lived token there before any
	// of its code runs signs it in without the login page. The sidebar is kept closed for the same
	// reason: a screen this size has no room for it.
	tokens, _ := json.Marshal(map[string]any{
		"access_token": b.cfg.token, "token_type": "Bearer", "expires_in": 1800,
		"hassUrl": b.cfg.ha, "clientId": b.cfg.ha + "/", "expires": 9999999999999, "refresh_token": "",
	})
	quoted, _ := json.Marshal(string(tokens))
	script := fmt.Sprintf(`localStorage.setItem("hassTokens", %s); localStorage.setItem("dockedSidebar", '"always_hidden"');`, quoted)

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
