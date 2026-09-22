//go:build !dot

package camera

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/feature/web"
)

// The switch in front of /screen.png lets the network read the screen. Working the screen is a
// different permission, so the parameters that do it are told apart from a plain screenshot here and
// go on to need the setup page's press.
func TestDrivingIsTheParametersThatChangeTheDevice(t *testing.T) {
	for _, url := range []string{
		"/screen.png",
		"/screen.png?",
		"/screen.png?sheet=",
	} {
		if driving(httptest.NewRequest(http.MethodGet, url, nil)) {
			t.Errorf("%s was taken for more than a screenshot", url)
		}
	}

	for _, url := range []string{
		"/screen.png?theme=dusk",
		"/screen.png?wifi=keyboard",
		"/screen.png?radio=stop",
		"/screen.png?sheet=alarms",
		"/screen.png?demo=1",
		"/screen.png?list=Theme&sheet=device",
	} {
		if !driving(httptest.NewRequest(http.MethodGet, url, nil)) {
			t.Errorf("%s was taken for a plain screenshot", url)
		}
	}
}

// Nothing has handed a session check in here, so nobody is let in: a build with no setup page
// refuses the options rather than allowing them.
func TestWithNoSetupPageNobodyIsLetIn(t *testing.T) {
	if web.LetIn(httptest.NewRequest(http.MethodGet, "/screen.png?theme=dusk", nil)) {
		t.Error("a request was let in with no setup page to let it in")
	}
}
