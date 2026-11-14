//go:build dot || spot

package setup

import (
	"context"
	"net/http"
)

// The recipe page is the Show's alone: on the Spot and the Dot there is no section, and a form posted
// for one is refused.

func mealieSection(context.Context, http.ResponseWriter, string) {}

func saveMealie(*http.Request) string { return "this device has no recipe page" }
