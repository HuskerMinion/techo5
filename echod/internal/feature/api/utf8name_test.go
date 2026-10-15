package api

import (
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/layout"
	esapi "github.com/ygelfand/go-esphome-device/api"
	"google.golang.org/protobuf/proto"
)

// A name written as Latin-1 (one 0xFC byte for the u-umlaut) is not UTF-8, and Go's protobuf will not
// marshal it into a string field, so the DeviceInfoResponse the device owes Home Assistant never leaves.
func TestNonUTF8NameBreaksDeviceInfo(t *testing.T) {
	latin1, utf8 := "K\xfcche Echo", "Küche Echo"
	if got := layout.Slug(latin1); got != "k-che-echo" {
		t.Fatalf("slug of the Latin-1 name = %q", got)
	}
	if got := layout.Slug(utf8); got != "k-che-echo" {
		t.Fatalf("slug of the UTF-8 name = %q", got)
	}
	if _, err := proto.Marshal(&esapi.DeviceInfoResponse{Name: "k-che-echo", FriendlyName: latin1}); err == nil {
		t.Fatal("a Latin-1 friendly name marshaled")
	} else {
		t.Logf("Latin-1 name: %v", err)
	}
	if _, err := proto.Marshal(&esapi.DeviceInfoResponse{Name: "k-che-echo", FriendlyName: utf8}); err != nil {
		t.Fatalf("a UTF-8 friendly name: %v", err)
	}
}
