package home

import (
	"strings"
	"testing"
)

func TestTheResumeTemplateQuotesWhatItIsGiven(t *testing.T) {
	got := maPlayerTemplate("media_player.kids_room", ` Kid's \ Room `)
	if !strings.Contains(got, `set own = 'media_player.kids_room'`) {
		t.Errorf("own player not set:\n%s", got)
	}
	if !strings.Contains(got, `set name = 'kid\'s \\ room'`) {
		t.Errorf("the name is not quoted, lowered and trimmed:\n%s", got)
	}
	if strings.Count(got, "{%-") != strings.Count(got, "-%}") {
		t.Error("the template's tags do not pair up")
	}
}
