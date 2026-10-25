//go:build !panel && !dot

package timezone

import "os/exec"

// ServerSettable is whether this device's own ntpd keeps its clock, so a time server can be set.
const ServerSettable = true

// ServerWhen says when a time server saved is first asked.
const ServerWhen = "It is asked within a minute of saving."

// restartNTP stops ntpd; boot's network keeper starts it again within a minute, asking the new server
// first.
var restartNTP = func() { _ = exec.Command("killall", "ntpd").Run() }
