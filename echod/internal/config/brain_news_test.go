package config

import "testing"

func TestNewsSourcesAreANameThenAFeed(t *testing.T) {
	b := Brain{News: "City Radio https://example.com/news.xml\n\n  KXYZ  http://radio.example/feed  \r\n"}
	got, err := b.NewsSources()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != (NewsSource{"City Radio", "https://example.com/news.xml"}) ||
		got[1] != (NewsSource{"KXYZ", "http://radio.example/feed"}) {
		t.Fatalf("got %+v", got)
	}
	for _, bad := range []string{"https://example.com/news.xml", "City Radio", "City Radio ftp://example.com/x", "City Radio https://"} {
		if _, err := (Brain{News: bad}).NewsSources(); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}
