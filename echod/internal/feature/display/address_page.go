//go:build !dot && !spot

package display

import (
	"context"
	"image"
	"image/draw"
	"log/slog"
	"strings"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
	"github.com/HuskerMinion/techo5/echod/internal/lib/wifi"
)

// The network address page: the setup page's Network address setting (lib/wifi/address.go) on the
// screen, for a device set up by hand. Automatic or fixed, three fields and a keypad of what an IPv4
// address is made of. It is one of the Wi-Fi pages, so it takes every tap and keeps the network keeper
// from counting a page left open as an outage, and opens from the Connections card.

// Fields of the page, in the order they are drawn.
const (
	addrFieldAddress = iota
	addrFieldGateway
	addrFieldDNS
	addrFields
)

var addrLabels = [addrFields]string{"Address", "Gateway", "DNS"}

// addrForm is the page's state.
type addrForm struct {
	fixed bool
	field int // the field the keypad types into
	vals  [addrFields]string
	busy  string // what is happening while a change is tried
	note  string // how the last change went
	err   string // why the last Save was refused
}

// The keypad: two rows of characters and one of commands, under the fields.
var addrKeys = [3][]string{
	{"1", "2", "3", "4", "5", "6", "7"},
	{"8", "9", "0", ".", "/", ",", "Del"},
	{"Clear", "Back", "Save"},
}

const (
	addrModeTopBase   = 66
	addrModeHBase     = 46
	addrFieldTopBase  = 148
	addrFieldHBase    = 54
	addrStatusTopBase = 228
	addrKeyTopBase    = 242
	addrKeyHBase      = 72
	addrKeyGapBase    = 6
)

// newAddrForm is the page as the device stands: its fixed setting, or what DHCP gave it as a start for
// one.
func newAddrForm(current string) *addrForm {
	a := wifi.LoadAddress()
	f := &addrForm{fixed: a.Fixed}
	if a.Fixed {
		f.vals[addrFieldAddress] = a.CIDR()
		f.vals[addrFieldGateway] = a.Gateway.String()
		if !(len(a.DNS) == 1 && a.DNS[0].Equal(a.Gateway)) {
			f.vals[addrFieldDNS] = strings.ReplaceAll(a.DNSList(), " ", "")
		}
	} else {
		f.vals[addrFieldAddress] = current
		if gw := wifi.Gateway(); gw != nil {
			f.vals[addrFieldGateway] = gw.String()
		}
	}
	if why := wifi.AddressFallback(); why != "" {
		f.note = "Not kept: " + why
	}
	return f
}

// addrModeRects are the two mode buttons.
func (r *renderer) addrModeRects() (auto, fixed image.Rectangle) {
	top, h := r.s(addrModeTopBase), r.s(addrModeHBase)
	mid := r.w / 2
	gap := r.s(addrKeyGapBase)
	return image.Rect(r.margin, top, mid-gap/2, top+h), image.Rect(mid+gap/2, top, r.w-r.margin, top+h)
}

// addrFieldRect is one of the three fields, side by side.
func (r *renderer) addrFieldRect(i int) image.Rectangle {
	gap := r.s(addrKeyGapBase)
	w := (r.w - 2*r.margin - 2*gap) / addrFields
	x := r.margin + i*(w+gap)
	top := r.s(addrFieldTopBase)
	return image.Rect(x, top, x+w, top+r.s(addrFieldHBase))
}

// addrKeyRect is a key; the command row's keys are wider, filling the row as the others do.
func (r *renderer) addrKeyRect(row, i int) image.Rectangle {
	gap := r.s(addrKeyGapBase)
	n := len(addrKeys[row])
	w := (r.w - 2*r.margin - (n-1)*gap) / n
	top := r.s(addrKeyTopBase) + row*(r.s(addrKeyHBase)+gap)
	x := r.margin + i*(w+gap)
	return image.Rect(x, top, x+w, top+r.s(addrKeyHBase))
}

// addrHit is what a tap on the page landed on: "auto", "fixed", "field:<n>", a key, or nothing.
func (r *renderer) addrHit(x, y int) string {
	pt := image.Pt(x, y)
	auto, fixed := r.addrModeRects()
	switch {
	case pt.In(auto):
		return "auto"
	case pt.In(fixed):
		return "fixed"
	}
	for i := 0; i < addrFields; i++ {
		if pt.In(r.addrFieldRect(i)) {
			return "field:" + string(rune('0'+i))
		}
	}
	for row := range addrKeys {
		for i, k := range addrKeys[row] {
			if pt.In(r.addrKeyRect(row, i)) {
				return k
			}
		}
	}
	return ""
}

func (r *renderer) addressPage(s scene) {
	f := s.wifi.addr
	r.text(r.body, "Network address", r.margin, r.s(52), amber)
	t := clockHM(s.now)
	r.text(r.small, t, r.w-r.margin-r.width(r.small, t), r.s(52), dim)

	auto, fixed := r.addrModeRects()
	r.key(auto, "Automatic (DHCP)", !f.fixed)
	r.key(fixed, "Fixed", f.fixed)

	for i := 0; i < addrFields; i++ {
		rect := r.addrFieldRect(i)
		r.text(r.tiny, addrLabels[i], rect.Min.X, rect.Min.Y-r.s(8), dim)
		fill := shift(walnut, 8)
		if f.fixed && i == f.field {
			fill = shift(ember, 24)
		}
		r.bevel(rect, fill, false)
		shown, c := f.vals[i], cream
		switch {
		case !f.fixed:
			c = dim
		case i == f.field:
			shown += "|"
		}
		if shown == "" && i == addrFieldDNS {
			shown, c = "the gateway", dim
		}
		for r.width(r.small, shown) > rect.Dx()-r.s(20) && len(shown) > 1 {
			shown = shown[1:]
		}
		r.text(r.small, shown, rect.Min.X+r.s(10), rect.Min.Y+rect.Dy()/2+r.s(11), c)
	}

	line, c := "No prefix means /24. Up to three DNS servers, with commas.", dim
	switch {
	case f.busy != "":
		line, c = f.busy, cream
	case f.err != "":
		line, c = f.err, amber
	case f.note != "":
		line, c = f.note, cream
	case !f.fixed:
		line = "The router hands out the address."
	}
	for r.width(r.tiny, line) > r.w-2*r.margin && len(line) > 1 {
		line = line[:len(line)-1]
	}
	r.text(r.tiny, line, r.margin, r.s(addrStatusTopBase), c)

	for row := range addrKeys {
		for i, k := range addrKeys[row] {
			rect := r.addrKeyRect(row, i)
			if row < 2 && !f.fixed {
				draw.Draw(r.dst, rect, image.NewUniform(shift(walnut, 6)), image.Point{}, draw.Src)
				continue
			}
			r.key(rect, k, k == "Save")
		}
	}
}

// openAddress shows the network address page.
func (d *Display) openAddress() {
	d.openWifi()
	d.mu.Lock()
	d.wifi.addr = newAddrForm(address())
	d.mu.Unlock()
	d.wake()
}

// addressBack leaves the page for the Connections card it was opened from.
// The sheet comes back before the page goes, so the settings lock never sees neither open.
func (d *Display) addressBack() {
	d.mu.Lock()
	d.sheet, d.cat, d.picker, d.restartArm = true, catConnections, "", time.Time{}
	d.draft, d.cardScroll, d.pickScroll = nil, 0, 0
	d.mu.Unlock()
	d.closeWifi()
}

// addressTap is a finger on the network address page.
func (d *Display) addressTap(x, y int) {
	hit := d.r.addrHit(x, y)
	d.mu.Lock()
	f := d.wifi.addr
	if f == nil || f.busy != "" {
		d.mu.Unlock()
		return
	}
	f.err = ""
	switch {
	case hit == "":
	case hit == "auto":
		f.fixed = false
	case hit == "fixed":
		f.fixed = true
	case strings.HasPrefix(hit, "field:"):
		f.fixed, f.field = true, int(hit[len("field:")]-'0')
	case hit == "Back":
		d.mu.Unlock()
		d.addressBack()
		return
	case hit == "Save":
		d.mu.Unlock()
		d.saveAddress()
		return
	case !f.fixed:
	case hit == "Del":
		if v := f.vals[f.field]; v != "" {
			f.vals[f.field] = v[:len(v)-1]
		}
	case hit == "Clear":
		f.vals[f.field] = ""
	case len(f.vals[f.field]) < 47:
		f.vals[f.field] += hit
	}
	d.mu.Unlock()
}

// saveAddress keeps the page's setting and tries it, as the setup page does, and says how it went.
func (d *Display) saveAddress() {
	d.mu.Lock()
	f := d.wifi.addr
	in := ""
	if f.fixed {
		in = f.vals[addrFieldAddress]
		if strings.TrimSpace(in) == "" {
			f.err = "Type the address, or choose Automatic."
			d.mu.Unlock()
			return
		}
	}
	a, err := wifi.ParseAddress(in, f.vals[addrFieldGateway], f.vals[addrFieldDNS])
	if err != nil {
		f.err = err.Error()
		d.mu.Unlock()
		return
	}
	if err := wifi.SaveAddress(a); err != nil {
		f.err = "Could not be saved: " + err.Error()
		d.mu.Unlock()
		return
	}
	f.busy, f.note = "Trying "+a.String()+"…", ""
	d.mu.Unlock()
	d.wake()
	slog.Info("network address set on the screen", "address", a.String())
	safe.Go("network address", func() {
		moved, err := wifi.ApplyAddress(context.Background())
		note := ""
		switch why := wifi.AddressFallback(); {
		case err != nil:
			note = "Could not be applied: " + err.Error()
			slog.Warn("the network address could not be applied", "err", err)
		case why != "":
			note = "Not kept: " + why
		case moved:
			note = "Now at " + address() + ". Home Assistant follows within about two minutes."
		default:
			note = "Saved. The address is " + address() + "."
		}
		d.mu.Lock()
		if d.wifi.addr == f {
			f.busy, f.note = "", note
		}
		d.mu.Unlock()
		d.refreshWifi()
	})
}
