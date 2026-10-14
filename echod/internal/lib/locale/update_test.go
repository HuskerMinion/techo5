package locale

import (
	"fmt"
	"strings"
	"testing"
)

func TestUpdateTextAvailableForScreenLanguages(t *testing.T) {
	languages := []string{"en", "de", "es", "fr", "it", "nl", "ru"}
	keys := []string{
		UpdateSystemTitle,
		UpdateFailedTitle,
		UpdateDownloading,
		UpdateInstalling,
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
	for _, lang := range []string{"en", "de", "es", "fr", "it", "nl", "ru"} {
		got := fmt.Sprintf(UpdateText(lang, UpdateDownloading), 73)
		if got == "" || !strings.Contains(got, "73%") {
			t.Errorf("download text for %q = %q; want the percentage", lang, got)
		}
	}
}
