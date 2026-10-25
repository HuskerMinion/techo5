//go:build dot

package timezone

const ServerSettable = true

const ServerWhen = "It is asked from the next restart on."

// restartNTP leaves ntpd running: on the Dot nothing would start it again, so the new server is asked
// from the next start on.
var restartNTP = func() {}
