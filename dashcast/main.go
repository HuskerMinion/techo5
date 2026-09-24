// dashcast shows Home Assistant dashboards on TECHO5 screens that are too small to run a browser.
//
// It runs a headless Chrome next to Home Assistant, opens the dashboard a device asks for at that
// device's screen size, and streams what changes to the device as pictures; the device sends back
// where it was touched, and that is replayed on the page. The device only ever decodes a picture and
// draws it, which an Echo can do, where running Home Assistant's frontend itself is beyond it.
//
// Configuration is from the environment:
//
//	HA_URL        Home Assistant's address, as the browser should open it (http://homeassistant:8123)
//	HA_TOKEN      a long-lived access token the browser signs in with
//	DASHCAST_KEY  what a device has to present before it is shown anything
//	LISTEN        where devices connect (default :9555)
//	CHROME        the browser to run (default: found on the PATH, or the headless-shell image's)
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))

	cfg := config{
		ha:     strings.TrimRight(os.Getenv("HA_URL"), "/"),
		token:  strings.TrimSpace(os.Getenv("HA_TOKEN")),
		key:    strings.TrimSpace(os.Getenv("DASHCAST_KEY")),
		listen: os.Getenv("LISTEN"),
		chrome: os.Getenv("CHROME"),
	}
	if cfg.listen == "" {
		cfg.listen = ":9555"
	}
	if cfg.ha == "" || cfg.token == "" || cfg.key == "" {
		slog.Error("HA_URL, HA_TOKEN and DASHCAST_KEY all have to be set")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b, err := newBrowser(ctx, cfg)
	if err != nil {
		slog.Error("starting the browser failed", "err", err)
		os.Exit(1)
	}
	defer b.close()

	ln, err := net.Listen("tcp", cfg.listen)
	if err != nil {
		slog.Error("listening failed", "addr", cfg.listen, "err", err)
		os.Exit(1)
	}
	slog.Info("dashcast ready", "listen", cfg.listen, "home_assistant", cfg.ha)
	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("accept", "err", err)
			continue
		}
		go serve(ctx, b, cfg, c)
	}
}

type config struct {
	ha, token, key, listen, chrome string
}
