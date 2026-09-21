package announce

import (
	"net"
	"testing"

	"github.com/libp2p/zeroconf/v2"
)

// An entry names one device and gives one address to send to. The name comes from the record this
// daemon wrote rather than the instance name, which the responder is free to decorate when two
// devices on a network want the same one.
func TestReadingAnEntry(t *testing.T) {
	e := &zeroconf.ServiceEntry{
		ServiceRecord: zeroconf.ServiceRecord{Instance: "laundry-room (2)"},
		Text:          []string{"name=Laundry Room"},
		AddrIPv4:      []net.IP{net.ParseIP("192.168.1.40")},
	}
	if got := nameOf(e); got != "Laundry Room" {
		t.Errorf("name %q, want the one in the record", got)
	}
	if got := addressOf(e); got != "192.168.1.40" {
		t.Errorf("address %q, want the v4 one", got)
	}
}

// Without a record of ours it is still a device, under whatever the responder called it.
func TestAnEntryWithoutOurRecord(t *testing.T) {
	e := &zeroconf.ServiceEntry{ServiceRecord: zeroconf.ServiceRecord{Instance: "kitchen"}}
	if got := nameOf(e); got != "kitchen" {
		t.Errorf("name %q, want the instance", got)
	}
}

// A device with only a link-local address cannot be sent to, and saying so beats sending an
// announcement into the dark.
func TestAnAddressNobodyCanReach(t *testing.T) {
	e := &zeroconf.ServiceEntry{
		AddrIPv4: []net.IP{net.ParseIP("169.254.3.4")},
		AddrIPv6: []net.IP{net.ParseIP("fe80::1")},
	}
	if got := addressOf(e); got != "" {
		t.Errorf("address %q, want none of them", got)
	}
}

// IPv6 is used when that is all there is.
func TestIPv6WhenThereIsNothingElse(t *testing.T) {
	e := &zeroconf.ServiceEntry{AddrIPv6: []net.IP{net.ParseIP("2606:8e80::1")}}
	if got := addressOf(e); got != "2606:8e80::1" {
		t.Errorf("address %q, want the v6 one", got)
	}
}
