//go:build !panel

package diag

import "context"

// networkSection is the bundle's network section: the Wi-Fi as the device runs it.
func networkSection(ctx context.Context) string { return networkSummary(ctx) }
