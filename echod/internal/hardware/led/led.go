// Package led drives the Echo Dot's ring through the is31fl3236 driver's sysfs attributes, the Echo Dot
// 5th gen's through the is31fl3246's twelve RGB LED class devices, and the Echo Input's single LED
// through the LP5562's LED class devices.
//
// This deliberately bypasses Amazon's LedController service. A direct frame write works even
// when that service has brightness set to 0, and keeps working once the Amazon stack is
// disabled — which the binder path does not.
package led

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// DefaultPath is the is31fl3236 i2c device directory.
const DefaultPath = "/sys/bus/i2c/devices/0-003f"

// SinglePath is the Echo Input's LED: one RGB LED on a TI LP5562 at 0-0030, whose three channels are LED
// class devices of their own (R, G and B, each with brightness and max_brightness), as Amazon's
// libled_hal.so drives it (echo-dot-assist, docs/re-cupcake.md).
const SinglePath = "/sys/bus/i2c/devices/0-0030/leds"

// ClassRingPrefix is the Echo Dot 5th gen's ring: an ISSI IS31FL3246 at 0-0031 whose twelve RGB modules
// are LED class devices of their own, is31fl3246:ledmod_0 to _11, each taking its color as "R G B" in
// its color attribute (Amazon's led-class-rgb, in the 6.5.5.5 GPL tree). The device tree maps the
// labels onto the chip's modules (ledmod_0 is module 3, ..., ledmod_9 is module 0) and the R/G/B order
// (rgb-map), so a label is a place on the ring and segment n is ledmod_n. Walked on one unit
// (2026-11-21): ledmod_0 is at the back, over the power socket, and the numbers run clockwise seen from
// above, as segments do on every ring here.
const ClassRingPrefix = "/sys/class/leds/is31fl3246:ledmod_"

// singleChannels are the Echo Input LED's class devices under SinglePath, in R, G, B order.
var singleChannels = [3]string{"R", "G", "B"}

// Channels is the number of PWM outputs: 12 ring segments x RGB.
const Channels = 36

// Segments is the number of addressable RGB positions on the ring.
const Segments = 12

// Layout, established by walking channels and then segments:
//
//   - The 36 channels are consecutive RGB triplets in R, G, B order, so segment n occupies
//     channels 3n, 3n+1, 3n+2.
//   - Segment 0 is at the bottom-right of the top face, between the microphone and
//     volume-down buttons.
//   - Increasing segment index runs clockwise, 30 degrees per segment.
//   - Each channel is a linear 0-255 brightness, 0 being dark.

// Color is one segment's color.
type Color struct{ R, G, B byte }

// SetSegments writes all 12 segments at once.
func (r *Ring) SetSegments(c []Color) error {
	if len(c) != Segments {
		return fmt.Errorf("led: need %d segments, got %d", Segments, len(c))
	}
	buf := make([]byte, Channels)
	for i, col := range c {
		buf[i*3], buf[i*3+1], buf[i*3+2] = col.R, col.G, col.B
	}
	return r.SetFrame(buf)
}

// SetSegment lights one segment and blanks the rest.
func (r *Ring) SetSegment(n int, c Color) error {
	if n < 0 || n >= Segments {
		return fmt.Errorf("led: segment %d out of range 0-%d", n, Segments-1)
	}
	all := make([]Color, Segments)
	all[n] = c
	return r.SetSegments(all)
}

// SetAll paints every segment the same color.
func (r *Ring) SetAll(c Color) error {
	all := make([]Color, Segments)
	for i := range all {
		all[i] = c
	}
	return r.SetSegments(all)
}

// Ring writes whole frames to the LED driver.
type Ring struct {
	Path string

	// last is what was written, so a frame identical to it can be skipped. An animation is a ticker
	// that redraws whether or not anything moved, and the ring holds what it was given with nothing
	// driving it — the same property that lets a frame outlive the process.
	//
	// Nil until the first write. Start takes the ring off the kernel driver mid boot animation, so
	// what is on it before that is not ours to assume.
	mu   sync.Mutex
	last []byte

	// absent is a device with no ring at all, such as the Echo Show 5. Frames are remembered and
	// nothing is written, so everything that draws keeps working and the hardware is simply not there.
	absent bool

	// single is a device with one RGB LED in place of the ring, the Echo Input: everything still draws
	// twelve segments, and the LED shows them mixed into one color (mixSegments). Path is then the LED
	// class directory (SinglePath), and singleMax each channel's max_brightness.
	single    bool
	singleMax [3]int

	// class is a ring of twelve RGB LED class devices, the Echo Dot 5th gen's: classDirs are their
	// directories in segment order, and classLit says each one's brightness (the chip's per-module
	// dimming, which the kernel zeroes whenever its trigger is taken off) has been put at full.
	classDirs []string
	classLit  bool
}

func New() *Ring {
	r := &Ring{Path: DefaultPath}
	if _, err := os.Stat(r.path()); err == nil {
		return r
	}
	if c, ok := newClassRing(ClassRingPrefix); ok {
		return c
	}
	if s, ok := newSingle(SinglePath); ok {
		return s
	}
	r.absent = true
	return r
}

// newClassRing is the Echo Dot 5th gen's ring, when all twelve of its modules are there.
func newClassRing(prefix string) (*Ring, bool) {
	r := &Ring{Path: filepath.Dir(prefix)}
	for n := range Segments {
		dir := prefix + strconv.Itoa(n)
		if _, err := os.Stat(dir + "/color"); err != nil {
			return nil, false
		}
		r.classDirs = append(r.classDirs, dir)
	}
	return r, true
}

// lightClass puts every module's brightness at its max_brightness, once: the colors are scaled by it in
// the chip, and with the kernel's boot animation taken off it is zero.
func (r *Ring) lightClass() error {
	if r.classLit {
		return nil
	}
	for _, dir := range r.classDirs {
		v := "255"
		if b, err := os.ReadFile(dir + "/max_brightness"); err == nil {
			v = strings.TrimSpace(string(b))
		}
		if err := os.WriteFile(dir+"/brightness", []byte(v), 0o644); err != nil {
			return err
		}
	}
	r.classLit = true
	return nil
}

// setClass writes the segments that differ from what the ring shows: each module is an I2C write and
// an update of its own, so an unchanged one is not sent again.
func (r *Ring) setClass(vals []byte) error {
	if err := r.lightClass(); err != nil {
		return err
	}
	for n, dir := range r.classDirs {
		at := 3 * n
		if r.last != nil && bytes.Equal(vals[at:at+3], r.last[at:at+3]) {
			continue
		}
		c := fmt.Sprintf("%d %d %d", vals[at], vals[at+1], vals[at+2])
		if err := os.WriteFile(dir+"/color", []byte(c), 0o644); err != nil {
			// What was written of this frame is not what last says: let the next frame write it all.
			r.last = nil
			return err
		}
	}
	return nil
}

// Class reports whether the ring is the Echo Dot 5th gen's twelve LED class devices.
func (r *Ring) Class() bool { return r.classDirs != nil }

// newSingle is the Echo Input's one LED at dir, when its three channels are there.
func newSingle(dir string) (*Ring, bool) {
	r := &Ring{Path: dir, single: true}
	for i, ch := range singleChannels {
		if _, err := os.Stat(dir + "/" + ch + "/brightness"); err != nil {
			return nil, false
		}
		r.singleMax[i] = 255
		if b, err := os.ReadFile(dir + "/" + ch + "/max_brightness"); err == nil {
			if v, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && v > 0 {
				r.singleMax[i] = v
			}
		}
	}
	return r, true
}

// Single reports whether the ring is one LED (the Echo Input's) rather than twelve.
func (r *Ring) Single() bool { return r.single }

// mixSegments is the one color a single LED shows for a frame of twelve segments: each channel at the
// brightest any segment has it. An average would leave a spinner or a chaser, which light one or two
// segments of twelve, at a twelfth of their brightness; the brightest keeps every state's color as
// bright as it is on a ring, and what moves around a ring stands still on one LED.
func mixSegments(vals []byte) [3]byte {
	var c [3]byte
	for i, v := range vals {
		if v > c[i%3] {
			c[i%3] = v
		}
	}
	return c
}

// Present reports whether there is a ring to drive.
func (r *Ring) Present() bool { return !r.absent }

func (r *Ring) path() string {
	if r.Path == "" {
		return DefaultPath
	}
	return r.Path
}

func (r *Ring) attr(name string) string { return r.path() + "/" + name }

// SetFrame writes all 36 channel brightnesses: twelve segments, three channels each. A frame the
// ring is already showing is not written again — see last.
func (r *Ring) SetFrame(vals []byte) error {
	if len(vals) != Channels {
		return fmt.Errorf("led: need %d channels, got %d", Channels, len(vals))
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if bytes.Equal(vals, r.last) {
		return nil
	}
	if r.absent {
		r.last = append(r.last[:0], vals...)
		return nil
	}
	if r.classDirs != nil {
		if err := r.setClass(vals); err != nil {
			return err
		}
		r.last = append(r.last[:0], vals...)
		return nil
	}
	if r.single {
		mixed := mixSegments(vals)
		for i, ch := range singleChannels {
			v := int(mixed[i]) * r.singleMax[i] / 255
			if err := os.WriteFile(r.attr(ch+"/brightness"), []byte(strconv.Itoa(v)), 0o644); err != nil {
				return err
			}
		}
		r.last = append(r.last[:0], vals...)
		return nil
	}
	if err := os.WriteFile(r.attr("frame"), []byte(hex.EncodeToString(vals)+"\n"), 0o644); err != nil {
		return err
	}

	r.last = append(r.last[:0], vals...)
	return nil
}

// Forget drops what SetFrame believes is showing, so the next write happens whatever it is. Taking
// the ring back off the kernel driver goes through here: it was animating until a moment ago and may
// have left anything in the registers.
func (r *Ring) Forget() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.last = nil
}

// Frame reads back the current frame.
func (r *Ring) Frame() ([]byte, error) {
	if r.absent || r.single || r.classDirs != nil {
		r.mu.Lock()
		defer r.mu.Unlock()
		return append(make([]byte, 0, Channels), r.last...), nil
	}
	b, err := os.ReadFile(r.attr("frame"))
	if err != nil {
		return nil, err
	}
	return hex.DecodeString(strings.TrimSpace(string(b)))
}

// Fill sets every channel to the same value.
func (r *Ring) Fill(v byte) error {
	buf := make([]byte, Channels)
	for i := range buf {
		buf[i] = v
	}
	return r.SetFrame(buf)
}

// Off blanks the ring.
func (r *Ring) Off() error { return r.Fill(0) }

// Current reads the global drive-current setting.
func (r *Ring) Current() (int, error) {
	if r.absent || !r.hasCurrent() {
		return 0, nil
	}
	b, err := os.ReadFile(r.attr("led_current"))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(b)))
}

// SetCurrent sets the global drive current.
func (r *Ring) SetCurrent(v int) error {
	if r.absent || !r.hasCurrent() {
		return nil
	}
	return os.WriteFile(r.attr("led_current"), []byte(strconv.Itoa(v)), 0o644)
}

// hasCurrent is whether the driver has the one current setting for all the ring: Amazon's 3.18 driver
// does; the is31fl32xx driver of the Echo 2nd gen's Linux 6.1 kernel writes frame at full current
// already, so there is nothing to set.
func (r *Ring) hasCurrent() bool {
	if r.single || r.classDirs != nil {
		return false
	}
	_, err := os.Stat(r.attr("led_current"))
	return err == nil
}

// SetBootAnimation toggles the driver's built-in boot animation. On the Echo Input it is the LP5562's
// bootanimation, on the chip beside its LED class devices: the kernel's own thread, which repaints the
// LED blue every 88 ms until it is told to stop, so left on it pulses over everything drawn (#641). A
// kernel without it has nothing to stop.
func (r *Ring) SetBootAnimation(on bool) error {
	if r.absent {
		return nil
	}
	v := "0"
	if on {
		v = "1"
	}
	if r.classDirs != nil {
		return r.classBootAnimation(on)
	}
	if r.single {
		attr := filepath.Join(filepath.Dir(r.path()), "bootanimation")
		if _, err := os.Stat(attr); err != nil {
			return nil
		}
		return os.WriteFile(attr, []byte(v), 0o644)
	}
	return os.WriteFile(r.attr("boot_animation"), []byte(v), 0o644)
}

// classBootAnimation is SetBootAnimation on the Echo Dot 5th gen: each module's trigger, the kernel's
// "boot-anim" (as the device tree starts them) or none. Taking a trigger off zeroes the module's
// brightness, so it is put back at full (lightClass) and the frame is written again whole. The lock is
// held across the triggers too, so no frame is drawn between a trigger zeroing a module and its relight.
func (r *Ring) classBootAnimation(on bool) error {
	trigger := "none"
	if on {
		trigger = "boot-anim"
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, dir := range r.classDirs {
		if _, err := os.Stat(dir + "/trigger"); err != nil {
			continue
		}
		if err := os.WriteFile(dir+"/trigger", []byte(trigger), 0o644); err != nil {
			return err
		}
	}
	r.classLit = false
	r.last = nil
	if on {
		return nil
	}
	return r.lightClass()
}
