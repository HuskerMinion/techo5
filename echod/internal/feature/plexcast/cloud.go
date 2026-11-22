//go:build !dot

package plexcast

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// Where the device tells an account where its player is. Plex's own players do this with a PUT of the
// connections, and it is the only part of this feature that is not covered by a published API: it is
// done on a best-effort basis, and nothing about casting on the home network depends on it.
//
// This is what a phone that is not on this network needs, so Plexamp can list the device at all.
const devicesURL = "https://plex.tv/devices"

// register says where this player is, for every address the device has: the port a controller
// connects to, and the name it shows. A refusal is logged and lived with -- GDM (gdm.go) is what Plex
// Web and Desktop use, and it needs none of this.
func (f *Feature) register(ctx context.Context) {
	token := config.Get().Plex.Token
	if token == "" {
		return
	}
	uris := 0
	for _, ip := range metrics.Addresses() {
		if ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		q := url.Values{}
		q.Add("Connection[][uri]", net.JoinHostPort(ip.String(), strconv.Itoa(Port)))
		q.Set("X-Plex-Device-Name", friendlyName())
		req, err := http.NewRequestWithContext(ctx, http.MethodPut,
			devicesURL+"/"+url.PathEscape(clientIdentifier())+"?"+q.Encode(), nil)
		if err != nil {
			continue
		}
		for k, v := range identityHeaders() {
			req.Header.Set(k, v)
		}
		req.Header.Set("X-Plex-Token", token)
		resp, err := client.Do(req)
		if err != nil {
			slog.Debug("plex: registering the player failed", "err", err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			slog.Debug("plex: registering the player was refused", "status", resp.Status)
			continue
		}
		uris++
	}
	if uris == 0 {
		// Said once per start at debug: a house whose plex.tv account cannot take this still casts
		// from everything on the network.
		slog.Debug("plex: the account was not told where the player is")
		return
	}
	slog.Info("plex: the account was told where the player is", "addresses", uris)
}

// unregister takes the device out of the account's list, so a phone stops offering a player that is no
// longer linked. Best effort, as register is: an account that will not let go of it is harmless, and
// the device advertises nothing locally any more either way.
func (f *Feature) unregister(ctx context.Context, token string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		devicesURL+"/"+url.PathEscape(clientIdentifier()), nil)
	if err != nil {
		return
	}
	for k, v := range identityHeaders() {
		req.Header.Set(k, v)
	}
	req.Header.Set("X-Plex-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		slog.Debug("plex: unregistering the player failed", "err", err)
		return
	}
	resp.Body.Close()
}
