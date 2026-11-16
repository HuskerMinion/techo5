//go:build !dot

package setup

import (
	"strings"
	"testing"
)

func TestJoiningReloadSkipsDirtyPage(t *testing.T) {
	for _, want := range []string{"'input'", "'change'", "if(!nbDirty)location.reload()"} {
		if !strings.Contains(joiningReload, want) {
			t.Errorf("joiningReload lacks %q", want)
		}
	}
}
