//go:build dot || spot

package setup

import "net/http"

// No dashboard page on these yet, so nothing to set for one.
func dashboardSection(http.ResponseWriter, string) {}

func saveDashboard(*http.Request) string { return "this device has no dashboard page" }
