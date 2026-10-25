package component

import (
	"sync"

	esphome "github.com/ygelfand/go-esphome-device"
)

// Sub-devices Home Assistant shows beneath this one, which entities join by Base.DeviceID. Zero is the
// device itself and is what most things want.
//
// The point is a shorter entity list per page, not grouping for its own sake: Home Assistant puts every
// entity of a device on one page, and there are enough of them here to be unreadable. Each of these is a
// real entry in the device registry, linked back to the device, so one is worth having only where it holds
// enough entities to be worth clicking into.
//
// Names are composed with the device's own at runtime. Home Assistant uses what it is given verbatim, so a
// bare "Ring" would be a device called "Ring" in a house with several satellites.
//
// The numbers are identity: Home Assistant keys a sub-device on the address and this, so changing one
// orphans the entry it used to name. Add above DeviceAssistant, which has to stay last because the slots
// count upwards from it.
const (
	DeviceRing uint32 = iota + 1

	// DeviceMicrophone is the array: how it is combined, how hard it is driven, whether it is cut.
	DeviceMicrophone

	// DevicePlayback is how sound comes out, which is not the media player itself — that is the thing
	// people reach for, and it stays where it is found.
	DevicePlayback

	// DeviceAssistant is the first of one per wake word slot. Home Assistant keeps its own Assistant and
	// Wake word selects on the device itself, so choosing a wake word and tuning it are on different pages.
	DeviceAssistant
)

// AssistantDevice is the sub-device holding one slot's settings.
func AssistantDevice(slot int) uint32 { return DeviceAssistant + uint32(slot) }

// DeviceTessera is the screen the Tessera add-on sees (feature/tessera), there only while that is turned
// on. It is numbered apart from the list above, well past the assistant slots, so adding it moved none of
// theirs, and a slot added later does not move it.
const DeviceTessera uint32 = 64

// Offered is a sub-device a component asks for, there only while Wanted says so. Suffix goes after the
// device's own name, as the others' do. Wanted is asked when the server starts, which is the only time
// Home Assistant reads the list.
type Offered struct {
	ID     uint32
	Suffix string
	Wanted func() bool
}

var (
	offeredMu sync.Mutex
	offered   []Offered
)

// Offer adds a sub-device that comes and goes with a setting. From a component's constructor.
func Offer(d Offered) {
	offeredMu.Lock()
	defer offeredMu.Unlock()
	offered = append(offered, d)
}

// OfferedDevices is the offered sub-devices wanted now, named for a device called name.
func OfferedDevices(name string) []esphome.Device {
	offeredMu.Lock()
	defer offeredMu.Unlock()
	var out []esphome.Device
	for _, d := range offered {
		if d.Wanted == nil || d.Wanted() {
			out = append(out, esphome.Device{ID: d.ID, Name: name + d.Suffix})
		}
	}
	return out
}
