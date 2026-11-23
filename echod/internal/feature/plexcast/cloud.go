//go:build !dot

package plexcast

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/metrics"
)

// Where the device tells an account where its player is. This is what a phone's cast list is built
// from: Plexamp on iOS, for one, does no searching of the home network at all and lists only the
// players the account knows. Plex Web, Plex Desktop and Plex for Android TV find the device by GDM
// (gdm.go) and need none of this, which is why a device that answers searches can still be invisible
// in an app.
//
// Two steps, in this order, because it is the only order plex.tv acts on:
//
//  1. the account's device list (devicesXML) is read for the number plex.tv gave this device when the
//     account was linked. A device is published to by that number; the identifier this device makes for
//     itself names it in the list but is not its address in it.
//  2. the connections are published against that number (devicesURL/{number}). A PUT against the client
//     identifier is answered 200 and changes nothing, which from the device's side is indistinguishable
//     from a registration that worked -- and the player stays out of every app.
const (
	devicesXML = "https://plex.tv/devices.xml"
	devicesURL = "https://plex.tv/devices"
)

// register says where this player is: the account's device list is read for this device's number, and
// the addresses the device has are published against it. Called after a link, on every start with a
// token, and again when the device's address changes.
func (f *Feature) register(ctx context.Context) {
	token := config.Get().Plex.Token
	if token == "" {
		return
	}
	uris := connectionURIs()
	if len(uris) == 0 {
		return
	}
	id, err := deviceID(ctx, token)
	if err != nil {
		slog.Warn("plex: the account's device list could not be read", "err", err)
		return
	}
	if id == "" {
		// The record is made with the link itself, so this is a link that has not settled yet: the next
		// start or network change asks again.
		slog.Info("plex: the account has no device for this player yet", "identifier", clientIdentifier())
		return
	}
	// One request carries every address: publishing replaces the connections the account had, so a
	// second request would leave only the last one in place.
	q := url.Values{}
	for _, u := range uris {
		q.Add("Connection[][uri]", u)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		devicesURL+"/"+url.PathEscape(id)+"?"+q.Encode(), nil)
	if err != nil {
		return
	}
	for k, v := range identityHeaders() {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-Plex-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("plex: the account was not told where the player is", "err", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		said, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		slog.Warn("plex: the account refused the player's address",
			"status", resp.Status, "said", strings.TrimSpace(string(said)))
		return
	}
	slog.Info("plex: the account knows where the player is", "device", id, "addresses", len(uris))
}

// connectionURIs are the addresses a controller could reach this player at, as full http addresses:
// the account's list takes them as written, and a bare host:port is not something a controller can
// connect to. metrics.Addresses leaves out the VPNs and the USB cable on purpose, so what is left is
// the home network's.
func connectionURIs() []string {
	var uris []string
	for _, ip := range metrics.Addresses() {
		if ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		uris = append(uris, "http://"+net.JoinHostPort(ip.String(), strconv.Itoa(Port)))
	}
	return uris
}

// deviceID is this device's number in the account's device list, or "" when the account has no record
// of it yet. The list is XML -- plex.tv answers a device list as XML whatever is asked for -- and it
// carries the identifier beside the number, which is how the two are tied together.
func deviceID(ctx context.Context, token string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, devicesXML, nil)
	if err != nil {
		return "", err
	}
	for k, v := range identityHeaders() {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/xml")
	req.Header.Set("X-Plex-Token", token)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return "", fmt.Errorf("plex.tv answered %s", resp.Status)
	}
	var list struct {
		Devices []struct {
			ID               string `xml:"id,attr"`
			ClientIdentifier string `xml:"clientIdentifier,attr"`
		} `xml:"Device"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, mostBody)).Decode(&list); err != nil {
		return "", fmt.Errorf("the device list was not readable: %w", err)
	}
	for _, d := range list.Devices {
		if d.ClientIdentifier == clientIdentifier() {
			return d.ID, nil
		}
	}
	return "", nil
}

// unregister takes the device out of the account's list, so a phone stops offering a player that is no
// longer linked. Best effort, as register is: an account that will not let go of it is harmless, and
// the device advertises nothing locally any more either way.
func (f *Feature) unregister(ctx context.Context, token string) {
	if token == "" {
		return
	}
	id, err := deviceID(ctx, token)
	if err != nil || id == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		devicesURL+"/"+url.PathEscape(id)+".xml", nil)
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
