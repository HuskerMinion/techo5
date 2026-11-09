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

// The template searches every Music Assistant player, keeps own as the last resort rather than skipping
// it outright (#485: own can be Music Assistant's own player when Home Assistant gave it the clean entity
// id), and renders nothing found as 'none', which Home Assistant takes rather than rejecting the call.
func TestTheResumeTemplateKeepsOwnAsALastResort(t *testing.T) {
	got := maPlayerTemplate("media_player.show8", "Show8")
	if strings.Contains(got, "s.entity_id != own") {
		t.Errorf("own is still excluded outright from the search:\n%s", got)
	}
	if !strings.Contains(got, "ns.o") {
		t.Errorf("own is not kept as a last resort:\n%s", got)
	}
	if !strings.HasSuffix(got, "ns.q or ns.p or ns.n or ns.o or 'none' }}") {
		t.Errorf("an empty answer does not fall back to 'none':\n%s", got)
	}
}
