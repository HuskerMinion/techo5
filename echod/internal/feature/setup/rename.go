package setup

import (
	"log/slog"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/layout"
	"github.com/HuskerMinion/techo5/echod/internal/lib/safe"
)

// Renaming a device.
//
// Home Assistant keys its entities on the device's MAC address, so a renamed device is the same
// device to it: the entity ids it was given stay, and automations naming them keep working. What
// changes is what Home Assistant shows, and that the entity ids stop looking like the name — enough
// to be told about, which is what the box on the page is for.
//
// On a device that has never met a Home Assistant there is nothing to think about at all, and naming
// one before it is handed to somebody is exactly what this is for.

// nameLimit is what the installer allows too: one line, short enough for the screen.
const nameLimit = 31

// rename writes the new name and restarts, since the name is announced when the daemon starts.
// It reports what was wrong, or empty when the device is on its way back up.
func rename(to string, acknowledged bool) string {
	to = strings.TrimSpace(to)
	switch {
	case to == "":
		return "the device needs a name"
	case len(to) > nameLimit:
		return "a name is at most 31 characters"
	case strings.ContainsAny(to, "\n\r\t"):
		return "a name is one line"
	case to == config.Get().Device.Name:
		return "that is already its name"
	case !acknowledged:
		return "tick the box to say you know the entity ids in Home Assistant do not change with it"
	}
	if err := os.WriteFile(layout.NamePath, []byte(to+"\n"), 0o644); err != nil {
		return "could not write the name: " + err.Error()
	}
	slog.Warn("device renamed from the setup page; restarting to announce it",
		"from", config.Get().Device.Name, "to", to)

	// Long enough for the page to answer, so whoever asked sees that it worked rather than a
	// connection that died mid-request.
	safe.Go("restart after rename", func() {
		time.Sleep(3 * time.Second)
		syscall.Sync()
		if err := syscall.Reboot(syscall.LINUX_REBOOT_CMD_RESTART); err != nil {
			slog.Error("restart after rename failed", "err", err)
		}
	})
	return ""
}
