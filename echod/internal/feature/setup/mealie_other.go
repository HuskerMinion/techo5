//go:build dot || spot

package setup

import "net/http"

// The recipe page only works on the Show. For Spot and the Dot the section writes nothing, and a form
// posted for it is refused.

func mealieSection(http.ResponseWriter, string) {}

func saveMealie(*http.Request) string { return "this device has no recipe page" }
