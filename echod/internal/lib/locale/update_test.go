package locale

import (
	"fmt"
	"strings"
	"testing"
)

func TestUpdateTextAvailableForScreenLanguages(t *testing.T) {
	languages := []string{"en", "de", "es", "fr", "it", "nl", "pt", "ru"}
	keys := []string{
		UpdateSystemTitle,
		UpdateFailedTitle,
		UpdateDownloading,
		UpdateInstalling,
		UpdateRestartingIn,
		UpdateRestarting,
		UpdateRebootingIn,
		UpdateRebooting,
		UpdatePowerHint,
		UpdateRetryHint,
	}

	for _, lang := range languages {
		for _, key := range keys {
			if got := UpdateText(lang, key); got == "" || got == key {
				t.Errorf("UpdateText(%q, %q) = %q; want a translated label", lang, key, got)
			}
		}
	}
}

func TestUpdateTextFallsBackToEnglish(t *testing.T) {
	for _, lang := range []string{"", "en", "unsupported"} {
		for key, want := range updateEnglish {
			if got := UpdateText(lang, key); got != want {
				t.Errorf("UpdateText(%q, %q) = %q; want %q", lang, key, got, want)
			}
		}
	}
}

func TestUpdateDownloadingFormatsPercent(t *testing.T) {
	for _, lang := range []string{"en", "de", "es", "fr", "it", "nl", "pt", "ru"} {
		got := fmt.Sprintf(UpdateText(lang, UpdateDownloading), 73)
		if got == "" || !strings.Contains(got, "73%") {
			t.Errorf("download text for %q = %q; want the percentage", lang, got)
		}
	}
}

// The countdown before a restart and the one before a reboot are different things to the person
// watching, so no language may word them the same.
func TestRestartingAndRebootingReadApart(t *testing.T) {
	for _, lang := range []string{"en", "de", "es", "fr", "it", "nl", "pt", "ru"} {
		for _, pair := range [][2]string{
			{UpdateRestartingIn, UpdateRebootingIn},
			{UpdateRestarting, UpdateRebooting},
		} {
			restart, reboot := UpdateText(lang, pair[0]), UpdateText(lang, pair[1])
			if restart == reboot {
				t.Errorf("%q words a restart and a reboot the same: %q", lang, restart)
			}
		}
	}
}
