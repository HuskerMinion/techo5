//go:build linux && !dot && !spot

package update

import (
	"context"
	"strings"
	"testing"
)

// An athena handed a release that carries only the other Shows' rootfs refuses it before anything
// is fetched or written: the key it asks for is arm-athena.
func TestAnAthenaRefusesTheOtherShowsRootfs(t *testing.T) {
	restoreArch, restoreBoard := arch, onAthena
	t.Cleanup(func() { arch, onAthena = restoreArch, restoreBoard })
	arch, onAthena = "arm", func() bool { return true }

	m := Manifest{Version: "1.3.0", Rootfs: map[string]Binary{
		"arm": {URL: "https://example/techo5-rootfs-1.3.0.tar.gz", SHA256: strings.Repeat("d", 64), Size: 60 << 20},
	}}
	err := installRootfs(context.Background(), m, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no rootfs for arm-athena") {
		t.Fatalf("installRootfs on an athena with only the arm rootfs: %v", err)
	}
}
