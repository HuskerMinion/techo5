package config

import "testing"

// A device that could not tune wrote the settled false back to itself, so every unit that ever ran
// such a build carries "asp": false whether or not anybody chose it. Once the tuning works, those
// units have to come up tuned rather than staying off for ever.
func TestTheTuningComesBackOnByItselfUnlessSomebodyTurnedItOff(t *testing.T) {
	// What every existing unit looks like: false was written by the settling, never chosen.
	settled := Speaker{ASP: false}
	if !settled.ASPWanted() {
		t.Error("a device that was never able to tune stays untuned after the fix")
	}

	// Somebody who turned it off keeps it off.
	off := Speaker{ASP: false, ASPChosen: true}
	if off.ASPWanted() {
		t.Error("a setting somebody turned off came back on")
	}

	on := Speaker{ASP: true, ASPChosen: true}
	if !on.ASPWanted() {
		t.Error("a setting somebody turned on did not stay on")
	}
}
