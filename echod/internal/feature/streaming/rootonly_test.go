package streaming

import (
	"os"

	"github.com/HuskerMinion/techo5/echod/internal/lib/rootonly"
)

// The tests run as somebody other than root: the markers they make pass as root's would.
func init() { rootonly.Owner = os.Getuid() }
