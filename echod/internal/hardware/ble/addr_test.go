package ble

import "testing"

// A report carries the address least significant byte first. Home Assistant has to get it as it is
// printed, or every device a proxy hears lands under an address nothing else has ever seen.
func TestReportAddressReachesHomeAssistantAsPrinted(t *testing.T) {
	p := []byte{
		leAdvertisingReport, 1, // one report
		0x00, 0x01, // event type, random address
		0xBC, 0x9A, 0x78, 0x56, 0x34, 0x12, // 12:34:56:78:9A:BC, least significant byte first
		3, 0x02, 0x01, 0x06, // flags
		0xA4, // RSSI -92
	}
	var got []Advertisement
	reports(p, func(a Advertisement) { got = append(got, a) })
	if len(got) != 1 {
		t.Fatalf("got %d reports, want 1", len(got))
	}
	if a := got[0].Addr(); a != 0x123456789ABC {
		t.Errorf("Addr() = %012X, want 123456789ABC", a)
	}
	if got[0].RSSI != -92 {
		t.Errorf("RSSI = %d, want -92", got[0].RSSI)
	}
}
