// Package all is what this device is made of.
//
// Components register themselves from init, so a component nobody imports is a component that
// silently does not exist — no entity, no lifecycle, no error. This is the one list that pulls them
// in, and importing it is what makes the registry complete.
//
// Order is not decided here. Each component declares its phase and its place within it, so this list
// can stay alphabetical and mean nothing but membership.
package all

import (
	_ "github.com/HuskerMinion/techo5/echod/internal/android/setup"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/activity"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/alarm"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/api"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/bluetooth"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/btaudio"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/buttons"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/camera"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/detect"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/diag"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/display"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/feedback"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/firmware"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/hastate"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/home"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/light"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/maintenance"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/media"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/microphone"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/mute"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/phone"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/recording"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/room"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/security"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/sendspin"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/setup"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/timer"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/timezone"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/voice"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/wakeword"
	_ "github.com/HuskerMinion/techo5/echod/internal/feature/web"
)
