//go:build dot

package setup

import "net/http"

// The Dot has no screen, so nothing to set for a dashboard.
func dashboardSection(http.ResponseWriter, string) {}

func saveDashboard(*http.Request) string { return "this device has no dashboard page" }
