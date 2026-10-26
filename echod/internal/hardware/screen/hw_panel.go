//go:build panel

package screen

// A TECHO5 Panel draws on the app's surface, with none of the Show's display hardware: no GPU helper,
// no color matrix and no backlight to trade against the picture. These are the Show's calls, doing
// nothing here.

// Tint is the gain on red, green and blue, each 0 to 1 of the value drawn; {1, 1, 1} is none.
type Tint [3]float64

// None is no tint.
var None = Tint{1, 1, 1}

// SetTint does nothing on a panel: the app's surface has no color matrix of ours.
// CanTint: a tablet's screen is Android's, and SetTint does nothing.
const CanTint = false

func (d *Device) SetTint(Tint) {}

// SetLift does nothing on a panel: the app's surface has no backlight of ours to lower.
func (d *Device) SetLift(bool) {}

// DrawnByGPU is false on a panel: there is no GPU helper.
func DrawnByGPU() bool { return false }
