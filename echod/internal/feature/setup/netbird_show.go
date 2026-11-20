//go:build !dot

package setup

import (
	"fmt"
	"net/http"
)

// netbirdSection says where NetBird went: the built-in client is gone (#580), and NetBird's own client
// runs from userdata instead, as its author's add-on. Shown where the built-in could run, which is
// wherever Tailscale can.
func netbirdSection(w http.ResponseWriter, _ string) {
	if !hasTailnet() {
		return
	}
	sectionOpen(w, "netbird", "NetBird", "An add-on")
	fmt.Fprint(w, `<p class="note" style="margin:0">NetBird is no longer built in. It runs as an add-on,
	 <a href="https://github.com/tioan/techo5-netbird">techo5-netbird</a>, made and kept by its author:
	 NetBird's own client, started from userdata at every boot, set up over SSH. It and others are in
	 <a href="https://github.com/HuskerMinion/techo5/blob/main/docs/external-asp.md#add-ons-others-have-made">Add-ons
	 others have made</a>.</p>`)
	sectionClose(w)
}
