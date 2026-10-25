//go:build panel

package timezone

// Android keeps a tablet's clock: there is no time server to set.
const ServerSettable = false

const ServerWhen = ""

var restartNTP = func() {}
