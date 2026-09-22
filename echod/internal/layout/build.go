package layout

import "fmt"

// Set with -ldflags at build time.
//
// Version has to stay something Home Assistant can rank, stamped or not: it is what a device reports
// as running, and Home Assistant offers an update whenever that differs from what a release says it
// carries, without asking which is newer. A bare "dev" differs from every release forever, so an
// unstamped daemon would show an update card no install could clear. "v0.0.0-dev" ranks below every
// release instead, which is what an unstamped build is — see update.ValidVersion for the rule.
var (
	Version   = "v0.0.0-dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

// VersionString is what a --version flag prints.
func VersionString() string {
	return fmt.Sprintf("%s (%s, %s)", Version, GitCommit, BuildDate)
}
