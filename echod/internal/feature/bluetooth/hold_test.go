package bluetooth

import (
	"errors"
	"path/filepath"
	"testing"

	esphome "github.com/ygelfand/go-esphome-device"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/hardware/ble"
)

// holdRadio is a scanner that remembers how it was started.
type holdRadio struct {
	running, scanning bool
	advertisement     []byte
	starts            int
}

func (r *holdRadio) Start(scan, _ bool, advertisement []byte, _ func(ble.Advertisement)) error {
	r.running, r.scanning, r.advertisement = true, scan, advertisement
	r.starts++
	return nil
}
func (r *holdRadio) Stop()           { r.running, r.scanning, r.advertisement = false, false, nil }
func (r *holdRadio) Running() bool   { return r.running }
func (r *holdRadio) Scanning() bool  { return r.scanning }
func (r *holdRadio) Reports() uint64 { return 0 }

// A hold takes the radio off, scanning and beacon both; holds nest, a release given twice counts once,
// and the last release brings the proxy back as it was.
func TestAHoldTakesTheRadioOffUntilReleased(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	if err := config.Set().Bluetooth().Proxy(true); err != nil {
		t.Fatal(err)
	}
	was := holdWait
	holdWait = 0
	t.Cleanup(func() { holdWait = was })
	radio := &holdRadio{}
	beacon := beaconState{advertisement: []byte{0x02, 0x01, 0x06}, minor: 7}
	b := &Proxy{proxy: &esphome.BluetoothProxy{}, radio: radio, wanted: make(chan struct{}, 1)}

	b.tune(beacon)
	if !radio.running || !radio.scanning || radio.advertisement == nil {
		t.Fatalf("not started as wanted: %+v", radio)
	}

	first := b.Hold() // the radio stops only when tune runs: there is no settle goroutine here
	b.tune(beacon)
	if radio.running {
		t.Fatalf("still running while held: %+v", radio)
	}
	second := b.Hold()
	first()
	first()
	b.tune(beacon)
	if radio.running {
		t.Fatal("running again with a hold still out")
	}
	second()
	b.tune(beacon)
	if !radio.running || !radio.scanning || radio.advertisement == nil {
		t.Fatalf("not back as it was: %+v", radio)
	}
	if radio.starts != 2 {
		t.Fatalf("started %d times", radio.starts)
	}
}

// offerRadio is a holdRadio that can also be lent out to advertise a service.
type offerRadio struct {
	holdRadio
	offered []byte
}

func (r *offerRadio) Offer(advertisement, _ []byte) error {
	r.running, r.offered = true, advertisement
	return nil
}

// The held radio, lent out, is left advertising by tune until it is given back; then the release
// brings the proxy back as before.
func TestAHeldRadioIsLentUntilGivenBack(t *testing.T) {
	config.Use(filepath.Join(t.TempDir(), "config.json"))
	if err := config.Set().Bluetooth().Proxy(true); err != nil {
		t.Fatal(err)
	}
	was := holdWait
	holdWait = 0
	t.Cleanup(func() { holdWait = was })
	radio := &offerRadio{}
	beacon := beaconState{advertisement: []byte{0x02, 0x01, 0x06}, minor: 7}
	b := &Proxy{proxy: &esphome.BluetoothProxy{}, radio: radio, wanted: make(chan struct{}, 1)}
	b.tune(beacon)

	if _, err := b.Offer([]byte{1}, nil); err == nil {
		t.Fatal("lent without a hold")
	}
	release := b.Hold()
	b.tune(beacon)
	done, err := b.Offer([]byte{1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Offer([]byte{2}, nil); err == nil {
		t.Fatal("lent twice")
	}
	b.tune(beacon)
	if !radio.running || radio.offered == nil || radio.scanning {
		t.Fatalf("not advertising the service while lent: %+v", radio)
	}

	done()
	done()
	if radio.running {
		t.Fatal("still running once given back")
	}
	release()
	b.tune(beacon)
	if !radio.running || !radio.scanning || radio.advertisement == nil {
		t.Fatalf("not back as it was: %+v", radio)
	}
}

// A radio with no Offer, such as the Shows' BlueZ scanner, says so.
func TestARadioWithoutOfferSaysSo(t *testing.T) {
	b := &Proxy{proxy: &esphome.BluetoothProxy{}, radio: &holdRadio{}, wanted: make(chan struct{}, 1)}
	b.held = 1
	if _, err := b.Offer([]byte{1}, nil); !errors.Is(err, ErrCannotOffer) {
		t.Fatalf("got %v", err)
	}
}
