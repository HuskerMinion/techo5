//go:build dot

package setup

import (
	"errors"
	"io/fs"
	"os"

	"github.com/HuskerMinion/techo5/echod/internal/android/firewall"
	"github.com/HuskerMinion/techo5/echod/internal/android/prop"
)

// pinControl dumps and drives every pin on the SoC. It is how the vendor audio HAL reaches the
// microphone mute line, which is the one thing here that must be nobody else's.
const pinControl = "/sys/devices/soc/1000b000.pinctrl/mt_gpio"

// deviceActions is what the Echo Dot on Fire OS needs on every boot.
var deviceActions = []Action{
	stop("meshmgrservice", "the other half of the BLE mesh stack"),
	stop("whad_cc", "whole-home audio's control channel, left with nothing once whad is hidden"),
	stop("vitals_service", "collects the device's vitals for Amazon"),
	stop("perfmonitord", "Amazon's performance monitoring"),
	stop("perfrecoveryd", "Amazon's performance monitoring"),
	stop("avahi-daemon", "Amazon's mDNS, for Spotify Connect; echod advertises itself"),
	stop("drm", "drmserver, for protected media playback"),
	{
		Name: "take the pin controller back from mediaserver",
		Reason: "the vendor audio HAL clears the microphone mute line while it sets up an input path, " +
			"which unmutes a device the user left muted",
		// Amazon's csm_audio_init.sh grants it: "mediaserver service needs to access mt_gpio file in
		// order to access mute LED on MUTE button ... chown root.media". Dropping the group write takes
		// it back. echod drives the line through /sys/class/gpio, which is root's, so this costs us
		// nothing and leaves mediaserver running — stopping it is not an option, because AudioService
		// inside system_server then retries forever and says so every time.
		//
		// Every boot: sysfs modes are the kernel's defaults again after a restart, and the vendor
		// script re-grants it. A kernel without the file (the Echo Dot 3's) has nothing to take back.
		Do: func() error {
			if err := os.Chmod(pinControl, 0o644); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			return nil
		},
	},
	{
		Name:   "silence AmazonUsageStatsService",
		Reason: "logs the network state on a timer from inside system_server",
		// The persist form of this property is 39 bytes, past what Android 5.1 accepts, so it
		// cannot be set once at install and has to be set again on every boot.
		Do: func() error { return prop.Set("log.tag.AmazonUsageStatsService", "S") },
	},
}

var deviceLate = []Action{
	stop("shblemeshd", "BLE mesh daemon left with nothing to talk to once its service package is hidden"),
	{
		Name: "put the firewall jumps back",
		Reason: "the boot completing is what starts firewall.sh, which flushes INPUT: our chains keep " +
			"their rules but nothing reaches them again until INPUT jumps to them",
		Do: firewall.Ensure,
	},
}
