//go:build dot

package setup

import "net/http"

// tailnetForm is a form that joins a tailnet, and what it says while it does.
type tailnetForm struct {
	draw func(w http.ResponseWriter, token, where string)
	says string
}

// tailnetForms are the forms that join a tailnet on this build: the Dot has none.
func tailnetForms() map[string]tailnetForm { return nil }
