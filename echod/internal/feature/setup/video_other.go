//go:build dot

package setup

import "net/http"

// Videos are the Show's and the Spot's: on the Dot there is no section, and a form posted for one
// is refused.

func videoSection(http.ResponseWriter, string) {}

func saveVideo(*http.Request) string { return "this device does not play videos" }
