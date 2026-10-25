package phone

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseContacts(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []Contact
	}{
		{"", nil},
		{" \n , ;", nil},
		{"Alex = +1 (555) 123-4567, Kitchen=106\n", []Contact{{Name: "Alex", Number: "15551234567"}, {Name: "Kitchen", Number: "106"}}},
		{"Mom|Mother=15551234567, Alex=15557654321", []Contact{
			{Name: "Mom", Number: "15551234567", Also: []string{"Mother"}}, {Name: "Alex", Number: "15557654321"}}},
		{"Mom | Mother | Ma = 15551234567\nSam Smith|Sammy = 15557654321", []Contact{
			{Name: "Mom", Number: "15551234567", Also: []string{"Mother", "Ma"}},
			{Name: "Sam Smith", Number: "15557654321", Also: []string{"Sammy"}}}},
	} {
		got, err := parseContacts(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if len(got) != len(c.want) || len(got) > 0 && !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseContactsRefuses(t *testing.T) {
	for _, c := range []struct{ in, says string }{
		{"no number here", "not name=number"},
		{"Alex=", "not name=number"},
		{"Alex=--", "not name=number"},
		{"=15551234567", "empty name"},
		{"Mom||Mother=15551234567", "empty name"},
		{"Mom=15551234567, mom=15557654321", "twice"},
		{"Mom=15551234567, Mother|MOM.=15557654321", "twice"},
		{"Mom|Alex=15551234567, Alex=15557654321", "twice"},
		{"Mom|Mom=15551234567", "twice"},
		{strings.Repeat("A=1,", MaxContacts) + "Zed=2", "twice"},
	} {
		_, err := parseContacts(c.in)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%q: error %v, want one saying %q", c.in, err, c.says)
		}
	}
	// Thirteen different names are one too many.
	var many []string
	for i := range MaxContacts + 1 {
		many = append(many, "Name"+string(rune('a'+i))+"=1"+strings.Repeat("0", i))
	}
	if _, err := parseContacts(strings.Join(many, ",")); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("%d contacts: error %v", MaxContacts+1, err)
	}
}

// What goes wrong with an entry is said without what the entry says: it can be a number, and errors
// are logged.
func TestParseContactsKeepsNumbersOutOfErrors(t *testing.T) {
	for _, in := range []string{"15551234567", "Alex 15551234567", "|=15551234567"} {
		_, err := parseContacts(in)
		if err == nil || strings.Contains(err.Error(), "555") {
			t.Errorf("%q: error %v", in, err)
		}
	}
}

// What the setup page shows reads back as the same book.
func TestFormatContactsReadsBack(t *testing.T) {
	list := []Contact{{Name: "Mom", Number: "15551234567", Also: []string{"Mother"}}, {Name: "Alex", Number: "106"}}
	text := FormatContacts(list)
	if text != "Mom|Mother = 15551234567\nAlex = 106" {
		t.Errorf("formatted %q", text)
	}
	got, err := parseContacts(text)
	if err != nil || !reflect.DeepEqual(got, list) {
		t.Errorf("read back %+v, %v", got, err)
	}
}

// The file keeps the shape another reader of it expects, with "also" only where there are other names,
// and stays owner-only.
func TestSavedContactsFile(t *testing.T) {
	was := contactsPath
	contactsPath = filepath.Join(t.TempDir(), "phone-contacts.json")
	t.Cleanup(func() { contactsPath = was })

	n, err := Get().SetContacts("Mom|Mother=15551234567, Alex=106")
	if err != nil || n != 2 {
		t.Fatalf("set: %d, %v", n, err)
	}
	b, err := os.ReadFile(contactsPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"name":"Mom","number":"15551234567","also":["Mother"]},{"name":"Alex","number":"106"}]`; string(b) != want {
		t.Errorf("file %s, want %s", b, want)
	}
	if fi, err := os.Stat(contactsPath); err == nil && fi.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Errorf("file mode %v, want owner-only", fi.Mode().Perm())
	}
	if got := Get().Contacts(); len(got) != 2 || got[0].Also[0] != "Mother" {
		t.Errorf("read %+v", got)
	}
	if _, err := Get().SetContacts(""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(contactsPath); !os.IsNotExist(err) {
		t.Errorf("an empty book left the file: %v", err)
	}
}
