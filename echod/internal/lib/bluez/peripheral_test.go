//go:build linux

package bluez

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/godbus/dbus/v5/prop"
)

// privateBus starts a dbus-daemon of the test's own, or skips where there is none.
func privateBus(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("dbus-daemon")
	if err != nil {
		t.Skip("no dbus-daemon")
	}
	cmd := exec.Command(path, "--session", "--nofork", "--print-address")
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Skip("dbus-daemon will not start:", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	addr, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(addr)
}

func connect(t *testing.T, addr string) *dbus.Conn {
	t.Helper()
	c, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// What bluetoothd does with a registered application, done by hand: read the objects through the
// ObjectManager, write and read a characteristic, and hear a value change as PropertiesChanged.
func TestPeripheralAsBluetoothdSeesIt(t *testing.T) {
	addr := privateBus(t)
	server := connect(t, addr)
	client := connect(t, addr)

	var written [][]byte
	got := make(chan []byte, 1)
	root := dbus.ObjectPath("/org/techo5/test")
	p := &Peripheral{
		conn:    server,
		root:    root,
		advert:  root + "/advert",
		objects: map[dbus.ObjectPath]map[string]map[string]*prop.Prop{},
		chars:   map[string]*charObj{},
		props:   map[dbus.ObjectPath]*prop.Properties{},
	}
	err := p.export("0000aaaa-0000-1000-8000-00805f9b34fb", []CharSpec{
		{UUID: "0000aaa1-0000-1000-8000-00805f9b34fb", Flags: []string{"read", "notify"}, Value: []byte("hello")},
		{UUID: "0000aaa2-0000-1000-8000-00805f9b34fb", Flags: []string{"write"}, Write: func(b []byte) {
			written = append(written, b)
			got <- b
		}},
	}, AdvertSpec{
		ServiceUUIDs: []string{"0000aaaa-0000-1000-8000-00805f9b34fb"},
		ServiceData:  map[string][]byte{"00004677-0000-1000-8000-00805f9b34fb": {2, 7, 0, 0, 0, 0}},
		LocalName:    "Kitchen",
	})
	if err != nil {
		t.Fatal(err)
	}
	name := server.Names()[0]

	var objs map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := client.Object(name, root).Call(objMgrIfc+".GetManagedObjects", 0).Store(&objs); err != nil {
		t.Fatal(err)
	}
	svc := objs[root+"/service0"][gattServiceIfc]
	if svc["UUID"].Value() != "0000aaaa-0000-1000-8000-00805f9b34fb" || svc["Primary"].Value() != true {
		t.Fatalf("service %v", svc)
	}
	c0 := objs[root+"/service0/char0"][gattCharIfc]
	if c0["Service"].Value() != root+"/service0" || !bytes.Equal(c0["Value"].Value().([]byte), []byte("hello")) {
		t.Fatalf("char0 %v", c0)
	}

	// Read, whole and from an offset.
	var b []byte
	if err := client.Object(name, root+"/service0/char0").Call(gattCharIfc+".ReadValue", 0, map[string]dbus.Variant{}).Store(&b); err != nil || string(b) != "hello" {
		t.Fatalf("read %q %v", b, err)
	}
	if err := client.Object(name, root+"/service0/char0").Call(gattCharIfc+".ReadValue", 0,
		map[string]dbus.Variant{"offset": dbus.MakeVariant(uint16(2))}).Store(&b); err != nil || string(b) != "llo" {
		t.Fatalf("read at offset %q %v", b, err)
	}

	// Write reaches the callback; a characteristic without one refuses.
	if err := client.Object(name, root+"/service0/char1").Call(gattCharIfc+".WriteValue", 0, []byte{1, 2, 3}, map[string]dbus.Variant{}).Err; err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-got:
		if !bytes.Equal(w, []byte{1, 2, 3}) {
			t.Fatalf("written % x", w)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("write never arrived")
	}
	if err := client.Object(name, root+"/service0/char0").Call(gattCharIfc+".WriteValue", 0, []byte{9}, map[string]dbus.Variant{}).Err; err == nil {
		t.Fatal("a read-only characteristic took a write")
	}

	// Set is the notification: PropertiesChanged on the characteristic, and the new value read back.
	if err := client.AddMatchSignal(dbus.WithMatchInterface(propsIfc), dbus.WithMatchMember("PropertiesChanged")); err != nil {
		t.Fatal(err)
	}
	sigs := make(chan *dbus.Signal, 4)
	client.Signal(sigs)
	p.Set("0000AAA1-0000-1000-8000-00805f9b34fb", []byte("bye"))
	select {
	case s := <-sigs:
		changed := s.Body[1].(map[string]dbus.Variant)
		if s.Path != root+"/service0/char0" || string(changed["Value"].Value().([]byte)) != "bye" {
			t.Fatalf("signal %v %v", s.Path, changed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no PropertiesChanged")
	}
	if err := client.Object(name, root+"/service0/char0").Call(gattCharIfc+".ReadValue", 0, map[string]dbus.Variant{}).Store(&b); err != nil || string(b) != "bye" {
		t.Fatalf("read after set %q %v", b, err)
	}

	// The advertisement, as LEAdvertisingManager1 reads it.
	var all map[string]dbus.Variant
	if err := client.Object(name, p.advert).Call(propsIfc+".GetAll", 0, advertIfc).Store(&all); err != nil {
		t.Fatal(err)
	}
	if all["Type"].Value() != "peripheral" || all["LocalName"].Value() != "Kitchen" {
		t.Fatalf("advert %v", all)
	}
	sd := all["ServiceData"].Value().(map[string]dbus.Variant)
	if !bytes.Equal(sd["00004677-0000-1000-8000-00805f9b34fb"].Value().([]byte), []byte{2, 7, 0, 0, 0, 0}) {
		t.Fatalf("service data %v", sd)
	}
	if len(written) != 1 {
		t.Fatalf("%d writes", len(written))
	}
}
