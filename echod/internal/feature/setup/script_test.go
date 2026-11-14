package setup

import (
	"context"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// formTags are the opening tags of the forms in a page.
var formTags = regexp.MustCompile(`(?s)<form\b[^>]*>`)

// busyForms are the words of the forms in a page that are marked as taking a while, by where they post.
func busyForms(t *testing.T, body string) map[string]string {
	t.Helper()
	said := regexp.MustCompile(`data-busy="([^"]*)"`)
	action := regexp.MustCompile(`action="([^"]*)"`)
	out := map[string]string{}
	for _, tag := range formTags.FindAllString(body, -1) {
		if m := said.FindStringSubmatch(tag); m != nil {
			out[action.FindStringSubmatch(tag)[1]] = m[1]
		}
	}
	// The mark is the form's and nothing else's: every one of them is inside a form's own tag.
	if all, in := strings.Count(body, "data-busy="), len(said.FindAllString(strings.Join(formTags.FindAllString(body, -1), ""), -1)); all != in {
		t.Errorf("%d marks, %d of them on a form's tag", all, in)
	}
	return out
}

// The forms that hold their request for a while say so: a mark on the form's tag, which the page's
// script reads. Without the script the form is the form it was: the same fields, the same button, the
// same place to post.
func TestFormsThatTakeAWhileAreMarked(t *testing.T) {
	f, c := in(t)
	ownSoundsHere(t)

	w := httptest.NewRecorder()
	wifiJoinForm(context.Background(), w, c.Value, false)
	join := w.Body.String()
	if got := busyForms(t, join)["/setup/save"]; got != busyWifi || !strings.Contains(got, "up to a minute") {
		t.Errorf("joining a network says %q", got)
	}
	for _, want := range []string{`<form method="post" action="/setup/save" data-busy=`, `name="what" value="wifi"`,
		`name="passphrase"`, `<button type="submit">Add network</button>`} {
		if !strings.Contains(join, want) {
			t.Errorf("the form that joins a network lacks %q", want)
		}
	}

	sound := get(f, "/setup?tab=sound", c).Body.String()
	marks := busyForms(t, sound)
	if marks["/setup/sound"] != busySound || marks["/setup/soundpack"] != busyPack {
		t.Errorf("the sound uploads say %q", marks)
	}
	if _, saves := marks["/setup/save"]; saves {
		t.Error("an ordinary save on Sound is marked as taking a while")
	}

	general := get(f, "/setup?tab=general", c).Body.String()
	if got := busyForms(t, general)["/setup/save"]; got != busyCheck {
		t.Errorf("looking for an update says %q", got)
	}
}

// The tailnets' joins are marked too, on the builds that have them.
func TestJoiningATailnetIsMarked(t *testing.T) {
	for what, form := range tailnetForms() {
		w := httptest.NewRecorder()
		form.draw(w, "walnut-token", "Not on it.")
		body := w.Body.String()
		if got := busyForms(t, body)["/setup/save"]; got != form.says {
			t.Errorf("joining %s says %q", what, got)
		}
		if !strings.Contains(body, `<button type="submit">Join</button>`) || !strings.Contains(body, `name="key"`) {
			t.Errorf("the form that joins %s lost its key or its button", what)
		}
	}
}

// Every tab carries the script once, at its foot, and the pages a browser that is not in gets carry
// none of it.
func TestThePagesScriptIsOnEveryTabOnce(t *testing.T) {
	f, c := in(t)
	for _, tb := range shownTabs() {
		body := get(f, "/setup?tab="+tb.id, c).Body.String()
		if n := strings.Count(body, "getAttribute('data-busy')"); n != 1 {
			t.Errorf("tab %s carries the script %d times", tb.id, n)
		}
	}
	if strings.Contains(get(f, "/setup", nil).Body.String(), "data-busy") {
		t.Error("the locked page carries the settings' script")
	}
}

// What a form says is the page's own words, escaped into the attribute.
func TestBusyEscapesItsWords(t *testing.T) {
	if got := busy(`a "b" <c>`); got != ` data-busy="a &#34;b&#34; &lt;c&gt;"` {
		t.Errorf("busy wrote %s", got)
	}
}
