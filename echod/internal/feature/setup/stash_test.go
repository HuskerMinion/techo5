package setup

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// refillOf is the refused form a page carries for its script, or nil.
func refillOf(t *testing.T, page string) map[string]any {
	t.Helper()
	m := regexp.MustCompile(`<script type="application/json" id="refill">([^<]*)</script>`).FindStringSubmatch(page)
	if m == nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(m[1]), &out); err != nil {
		t.Fatalf("the refill is not JSON: %v", err)
	}
	return out
}

// A refused form comes back with what was typed into it, once, to the browser that typed it, and
// escaped so that nothing typed can end the script it is in.
func TestARefusedFormComesBackAsTyped(t *testing.T) {
	f, c := in(t)
	typed := "a word </script><b>bold</b>\nand a second line"
	timer := url.Values{"what": {"alarm_add"}, "kind": {"timer"}, "in": {"banana"}, "label": {typed}}
	to := post(t, f, c, timer)
	if to.Query().Get("problem") == "" {
		t.Fatal("a timer of no length was taken")
	}
	if strings.Contains(to.String(), "bold") {
		t.Error("what was typed is in the address")
	}
	page := get(f, to.RequestURI(), c).Body.String()
	got := refillOf(t, page)
	if got == nil || got["what"] != "alarm_add" {
		t.Fatalf("the page does not carry the refused form: %v", got)
	}
	if words, _ := got["fields"].(map[string]any)["label"].([]any); len(words) != 1 || words[0] != typed {
		t.Errorf("the label came back as %v", words)
	}
	if strings.Contains(page, "</script><b>") {
		t.Error("what was typed ended the script it is in")
	}
	if _, ok := got["fields"].(map[string]any)["token"]; ok {
		t.Error("the page's token was kept with the form")
	}
	// Once: a reload shows what is saved.
	if refillOf(t, get(f, to.RequestURI(), c).Body.String()) != nil {
		t.Error("the refused form came back twice")
	}
	// Not to another browser, and not on another tab.
	post(t, f, c, timer)
	if _, ok := f.rejected.take("somebody-else", formTab("alarm_add")); ok {
		t.Error("another session was handed the form")
	}
	if refillOf(t, get(f, "/setup?tab=sound&problem=x", c).Body.String()) != nil {
		t.Error("the form came back on a tab it is not on")
	}
	// A page without a problem to say does not refill, and closing the page forgets it.
	if refillOf(t, get(f, "/setup?tab=alarms", c).Body.String()) != nil {
		t.Error("a page with no refusal was refilled")
	}
	f.Close()
	if _, ok := f.rejected.take(c.Value, formTab("alarm_add")); ok {
		t.Error("the form outlived the page")
	}
}

// The house word is the key the house's devices sign with: a refused house form does not keep it.
func TestARefusedHouseFormKeepsNoWord(t *testing.T) {
	f, c := in(t)
	to := post(t, f, c, url.Values{"what": {"house"}, "word": {"two\nlines"}})
	if to.Query().Get("problem") == "" {
		t.Fatal("a house word of two lines was taken")
	}
	got := refillOf(t, get(f, to.RequestURI(), c).Body.String())
	if got != nil {
		if _, ok := got["fields"].(map[string]any)["word"]; ok {
			t.Error("the house word was kept with the refused form")
		}
	}
}

// What is never kept: a secret, a private address, an address with a login in it, the box that says a
// danger is understood.
func TestARefusedFormKeepsNoSecret(t *testing.T) {
	f, c := in(t)
	for _, v := range []url.Values{
		{"what": {"music"}, "url": {"ftp://192.168.1.20"}, "matoken": {"walnut-stash-ma"}},
		{"what": {"calendar"}, "name": {"Chores"}, "url": {"calendar.example.org/walnut-stash-cal"}},
		{"what": {"go2rtc"}, "address": {"http://viewer:walnut-stash-rtc@192.168.1.20:1984\nx"}},
		{"what": {"reolink"}, "host": {"192.168.1.30\n"}, "user": {"admin"}, "pass": {"walnut-stash-rl"}},
		{"what": {"lock"}, "do": {"set"}, "pin": {"12"}},
		{"what": {"giftreset"}, "understood": {"no"}},
	} {
		to := post(t, f, c, v)
		if to.Query().Get("problem") == "" {
			continue // not a form this build refuses that way
		}
		page := get(f, to.RequestURI(), c).Body.String()
		got := refillOf(t, page)
		if got == nil {
			continue
		}
		fields, _ := got["fields"].(map[string]any)
		for _, name := range []string{"matoken", "pass", "pin", "understood", "token"} {
			if _, ok := fields[name]; ok {
				t.Errorf("%s: %s was kept", v.Get("what"), name)
			}
		}
		if v.Get("what") == "calendar" {
			if _, ok := fields["url"]; ok {
				t.Error("a calendar's private address was kept")
			}
		}
		if v.Get("what") == "go2rtc" {
			if _, ok := fields["address"]; ok {
				t.Error("an address with a login in it was kept")
			}
		}
		if strings.Contains(page, "walnut-stash") {
			t.Errorf("%s: a secret is in the page", v.Get("what"))
		}
	}
	// A form that is not one of the page's is not kept at all.
	f.rejected.keep(c.Value, "nothing-here", url.Values{"a": {"b"}})
	if _, ok := f.rejected.take(c.Value, defaultTab); ok {
		t.Error("a form the page does not have was kept")
	}
}

// Every password field the page draws, on every tab, is one the stash leaves out.
func TestEveryPasswordFieldIsLeftOutOfTheStash(t *testing.T) {
	saveFakeSecrets(t)
	f := build()
	f.Open()
	c := ask(t, f)
	f.press()
	name := regexp.MustCompile(`name="([^"]*)"`)
	seen := 0
	for _, tb := range shownTabs() {
		for _, field := range passwordFields.FindAllString(get(f, "/setup?tab="+tb.id, c).Body.String(), -1) {
			m := name.FindStringSubmatch(field)
			if m == nil {
				continue
			}
			seen++
			if !stashSecret[m[1]] {
				t.Errorf("tab %s: the password field %q would be kept with a refused form", tb.id, m[1])
			}
		}
	}
	if seen < 4 {
		t.Errorf("only %d password fields were found", seen)
	}
}
