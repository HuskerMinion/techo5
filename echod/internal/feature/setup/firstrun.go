package setup

import "github.com/HuskerMinion/techo5/echod/internal/feature/firstrun"

func init() { firstrun.SetRenamer(Rename) }

// Rename is renaming from first run (feature/firstrun), on a device's own screen or this page's first
// run. Somebody is at the device, as the page's press makes sure of, and the box the General tab asks
// to be ticked is about a Home Assistant that has entities named after the old name: what first run's
// own line says instead. The daemon restarts to announce it, and first run comes back on the same step.
func Rename(to string) string { return rename(to, true) }

// firstRunTakesPress is whether a press of the action button is first run's: still to do, with nothing
// connected, the ring breathing orange (led.Splash). It opens this page, and nothing else answers it.
func firstRunTakesPress() bool { return firstrun.Pending() && !firstrun.HomeConnected() }
