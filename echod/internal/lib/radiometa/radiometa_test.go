package radiometa

import "testing"

func TestBBCNetworkFor(t *testing.T) {
	tests := []struct {
		name    string
		service string
		ok      bool
	}{
		{"BBC Radio 1", "bbc_radio_one", true},
		{"BBC Radio One", "bbc_radio_one", true},
		{"BBC Radio 1Xtra", "bbc_1xtra", true},
		{"BBC Radio 2", "bbc_radio_two", true},
		{"BBC Radio 3", "bbc_radio_three", true},
		{"BBC Radio 4", "bbc_radio_fourfm", true},
		{"BBC Radio 4 Extra", "bbc_radio_four_extra", true},
		{"BBC Radio 5 Live", "bbc_radio_five_live", true},
		{"BBC Radio Five Live", "bbc_radio_five_live", true},
		{"BBC Radio 6 Music", "bbc_6music", true},
		{"BBC 6 Music", "bbc_6music", true},
		{"BBC Radio 6Music", "bbc_6music", true},
		{"BBC Asian Network", "bbc_asian_network", true},

		// What Radio Browser lists add around a network's name.
		{"BBC Radio 4 (128k)", "bbc_radio_fourfm", true},
		{"BBC Radio 2 (128k)", "bbc_radio_two", true},
		{" BBC RADIO 1 - The biggest new pop and all-day vibes", "bbc_radio_one", true},
		{"BBC Radio 1XTRA: Amplifying black music & culture", "bbc_1xtra", true},

		// No BBC in the name: it could be a station anywhere. World Service is not in the table
		// yet; it falls back to iHeartRadio and TuneIn, where the match is guarded.
		{"Radio 4", "", false},
		{"BBC World Service", "", false},
		{"BBC Radio Humberside", "", false},
		{"BBC 6 Indie Forever", "", false},
		{"BBC News HD (720P)", "", false},
		{"BBC Live News (128k)", "", false},
	}
	for _, c := range tests {
		n, ok := bbcNetworkFor(c.name)
		if ok != c.ok || n.service != c.service {
			t.Errorf("bbcNetworkFor(%q) = %q, %v; want %q, %v", c.name, n.service, ok, c.service, c.ok)
		}
	}
}

func TestMatchEnough(t *testing.T) {
	tests := []struct {
		query, candidate string
		want             bool
	}{
		{"BBC Radio 6 Music", "BBC Radio 6 Music", true},
		{"BBC Radio 6 Music", "BBC KUT 90.5 HD2 KUT-HD2", false},
		{"BBC Radio 4", "BBC KUT 90.5 HD2 KUT-HD2", false},
		{"98.7 WZZZ", "98.7 WZZZ WZZZ-FM", true},
		{"WZZZ", "98.7 WZZZ WZZZ-FM", true},
		{"KEXP", "KEXP 90.3 FM", true},
		{"KEXP", "KUT 90.5", false},
		{"BBC Radio 1", "BBC Radio 1Xtra", true}, // two words is the rule's floor; the BBC table settles its own names
	}
	for _, c := range tests {
		if got := matchEnough(c.query, c.candidate); got != c.want {
			t.Errorf("matchEnough(%q, %q) = %v; want %v", c.query, c.candidate, got, c.want)
		}
	}
}
