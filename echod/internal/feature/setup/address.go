package setup

import (
	"context"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// addressSection is how the device gets its address on the network: from the router's DHCP, as every
// device starts, or fixed. A new fixed address that does not work, taken by another device or with a
// gateway that does not answer from it, is dropped and the setting in use kept, and this says why.
// Not offered on an image without techo5-net, which could save a setting but never try it.
func addressSection(w http.ResponseWriter, token string) {
	if !wifi.Available() || !wifi.AddressSupported() {
		return
	}
	a := wifi.LoadAddress()
	fmt.Fprint(w, `<fieldset><legend>Network address</legend><form method="post" action="/setup/save">`)
	hidden(w, token, "address", "connections")
	if reason := wifi.AddressFallback(); reason != "" {
		fmt.Fprintf(w, `<p class="bad">The last change was not kept: %s. The device kept the setting
		 below.</p>`, html.EscapeString(reason))
	}
	checked := func(on bool) string {
		if on {
			return " checked"
		}
		return ""
	}
	dns := a.DNSList()
	if a.Fixed && len(a.DNS) == 1 && a.DNS[0].Equal(a.Gateway) {
		dns = "" // the default, shown as the placeholder
	}
	fmt.Fprintf(w, `<label><input type="radio" name="mode" value="dhcp"%s> Automatic, from the router (DHCP)</label>
	 <label><input type="radio" name="mode" value="fixed"%s> Fixed</label>
	 <label for="address">Address</label>
	 <input id="address" name="address" value="%s" autocomplete="off" placeholder="192.168.1.50/24">
	 <label for="gateway">Gateway</label>
	 <input id="gateway" name="gateway" value="%s" autocomplete="off" placeholder="192.168.1.1">
	 <label for="dns">DNS</label>
	 <input id="dns" name="dns" value="%s" autocomplete="off" placeholder="the gateway">
	 <p class="note">A fixed address can be one the router's DHCP does not hand out. Without a prefix it is
	  /24. Up to three DNS servers, with commas between them; left empty, it asks the gateway.</p>
	 <p class="note">Changing it moves this page to the new address, where the browser has to be let in
	  again. A new fixed address is tried first: if another device has it, or the gateway does not answer
	  from it, the device keeps the setting it had and says why here. Once kept, it is used as it is after
	  every restart, on the Wi-Fi it was set on; on another network, or when its gateway stays silent for five
	  minutes, the device takes an address from the router until it restarts. Home Assistant follows the
	  device to a new address by itself, about two minutes after the change.</p>
	 <p><button type="submit">Save</button></p></form></fieldset>`,
		checked(!a.Fixed), checked(a.Fixed), html.EscapeString(a.CIDR()), html.EscapeString(ipString(a)), html.EscapeString(dns))
}

func ipString(a wifi.Address) string {
	if !a.Fixed {
		return ""
	}
	return a.Gateway.String()
}

// applyAfter is how long after the answer the new address is put on, so the browser has the page that
// says where the device went before the connection it came on goes away.
var applyAfter = 2 * time.Second

// saveAddress keeps the address setting and puts it on the interface. It answers the browser itself
// when the device is moving, since the redirect the other settings use would go to the old address;
// handled is false when the usual redirect is still right, with problem saying what was refused.
func saveAddress(w http.ResponseWriter, r *http.Request) (problem string, handled bool) {
	address := r.PostFormValue("address")
	if r.PostFormValue("mode") != "fixed" {
		address = ""
	}
	a, err := wifi.ParseAddress(address, r.PostFormValue("gateway"), r.PostFormValue("dns"))
	if err != nil {
		return err.Error(), false
	}
	if r.PostFormValue("mode") == "fixed" && !a.Fixed {
		return "a fixed address needs the address itself, like 192.168.1.50/24", false
	}
	if wifi.AddressUnchanged(a) {
		return "", false // the setting in use: nothing to try, nothing moves
	}
	before := wifi.LoadAddress()
	slog.Info("setup page: network address set", "address", a.String())

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	here := wifi.Current(ctx).Address
	cancel()
	safe.Go("network address", func() {
		time.Sleep(applyAfter)
		if _, err := wifi.ChangeAddress(context.Background(), a); err != nil {
			slog.Warn("setup page: the network address could not be applied", "err", err)
		}
	})
	switch {
	case a.Fixed && a.IP.String() == here:
		return "", false // the address it already has: nothing moves
	case !a.Fixed && !before.Fixed:
		return "", false
	}
	head(w)
	fmt.Fprintf(w, `<div class="wrap"><h1>%s</h1><p class="sub">Setup</p>`, html.EscapeString(deviceName()))
	if a.Fixed {
		next := "http://" + a.IP.String() + "/setup"
		fmt.Fprintf(w, `<p><strong>Saved.</strong> In a moment the device moves to %s; Home Assistant
		 finds it there by itself, within about two minutes.</p>
		 <p><a href="%s">Open the setup page there</a> (let the browser in again on the device). If it is not
		  found within a minute, the address did not work and the device kept the one it had: open the page
		  there to see why.</p></div>`, html.EscapeString(a.IP.String()), html.EscapeString(next))
	} else {
		fmt.Fprint(w, `<p><strong>Saved.</strong> In a moment the device asks the router for an address.</p>
		 <p>The setup page will be at that new address: the router's list of devices shows it, and so does the
		  device's IP address sensor in Home Assistant.</p></div>`)
	}
	return "", true
}
