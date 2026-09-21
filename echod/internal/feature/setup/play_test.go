package setup

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// form is a posted stations form, as the page's own buttons send one.
func form(values url.Values) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "/setup/save", strings.NewReader(values.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if err := r.ParseForm(); err != nil {
		panic(err)
	}
	return r
}

// Play carries what is typed rather than what was saved, which is the whole use of it: an address is
// tried before it is kept. What it must not do is accept a row that says nothing, or a number that
// names no row at all.
func TestPlayingARowThatIsNotOne(t *testing.T) {
	for _, c := range []struct {
		what   string
		values url.Values
		says   string
	}{
		{
			what:   "a row number the form does not have",
			values: url.Values{"play": {"7"}, "name": {"KXYZ"}, "url": {"https://s/1"}},
			says:   "not one of these",
		},
		{
			what:   "a row number that is not a number",
			values: url.Values{"play": {"first"}, "name": {"KXYZ"}, "url": {"https://s/1"}},
			says:   "not one of these",
		},
		{
			what:   "the spare row at the bottom, with nothing in it",
			values: url.Values{"play": {"0"}, "name": {""}, "url": {""}},
			says:   "fill the row in first",
		},
		{
			what:   "a name with no address",
			values: url.Values{"play": {"0"}, "name": {"KXYZ"}, "url": {""}},
			says:   "no stream address",
		},
		{
			what:   "an address that is not a stream address",
			values: url.Values{"play": {"0"}, "name": {"KXYZ"}, "url": {"file:///etc/passwd"}},
			says:   "http://",
		},
	} {
		got := playRow(form(c.values))
		if got == "" {
			t.Errorf("%s: played it anyway", c.what)
			continue
		}
		if !strings.Contains(got, c.says) {
			t.Errorf("%s: refused with %q, want it to mention %q", c.what, got, c.says)
		}
	}
}
