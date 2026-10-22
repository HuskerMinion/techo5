package diag

import (
	"slices"
	"strings"
	"testing"

	"github.com/HuskerMinion/techo5/echod/internal/config"
)

// The newer switches are in a bundle, as on or off, and their secrets are not.
func TestTheBundleSaysWhichSwitchesAreOn(t *testing.T) {
	var c config.Config
	c.Remote.On, c.Remote.Secret = true, "remote-secret-value"
	c.CamStream.On, c.CamStream.Pass = true, "camera-password-value"
	c.Microphone.Claps = true
	got := settingsSummary(c)
	want := "switches: remote_api=true camera_streaming=true camera_discovery=false camera_login_from_ha=false nearest=true noise_sensor=false claps=true clock_hold=false"
	if !strings.Contains(got, want) {
		t.Errorf("summary %q, want %q", got, want)
	}
	for _, secret := range []string{c.Remote.Secret, c.CamStream.Pass} {
		if strings.Contains(got, secret) {
			t.Errorf("the summary carries %q", secret)
		}
	}
}

// The secrets a line could carry are named for the redactor, the go2rtc login with them.
func TestTheSecretsAreTakenOut(t *testing.T) {
	var c config.Config
	c.CamStream.Pass, c.Remote.Secret, c.Home.HouseWord = "camera-password-value", "remote-secret-value", "our long house word"
	c.Home.Immich.Key = "immich-key-value"
	c.Home.Go2RTC.Base = "http://admin:go2rtc-password@go2rtc.example:1984"
	got := secrets(c)
	for _, want := range []string{"camera-password-value", "remote-secret-value", "our long house word", "immich-key-value", "go2rtc-password"} {
		if !slices.Contains(got, want) {
			t.Errorf("%q is not among the secrets", want)
		}
	}
	// A short one would take a common word out all over the bundle.
	if slices.Contains(got, "admin") {
		t.Error("a short user name is among the secrets")
	}
}
