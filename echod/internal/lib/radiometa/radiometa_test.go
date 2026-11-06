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
		{"BBC 1 XTRA", "bbc_1xtra", true},
		{"BBC One Xtra", "bbc_1xtra", true},
		{"BBC Radio 1XTRA: Amplifying black music & culture", "bbc_1xtra", true},
		{"BBC Radio 2", "bbc_radio_two", true},
		{"BBC Radio 3", "bbc_radio_three", true},
		{"BBC Radio 4", "bbc_radio_fourfm", true},
		{"BBC Radio 4 Extra", "bbc_radio_four_extra", true},
		{"BBC Radio 5 Live", "bbc_radio_five_live", true},
		{"BBC Radio Five Live", "bbc_radio_five_live", true},
		{"BBC Radio 5 Sports Extra (UK only)", "bbc_radio_five_live_sports_extra", true},
		{"BBC Radio Five Live Sports Extra", "bbc_radio_five_live_sports_extra", true},
		{"BBC Radio 6 Music", "bbc_6music", true},
		{"BBC 6 Music", "bbc_6music", true},
		{"BBC Radio 6Music", "bbc_6music", true},
		{"BBC World Service", "bbc_world_service", true},
		{"BBC Asian Network", "bbc_asian_network", true},

		// What Radio Browser lists add around a network's name.
		{"BBC Radio 4 (128k)", "bbc_radio_fourfm", true},
		{"BBC Radio 2 (128k)", "bbc_radio_two", true},
		{" BBC RADIO 1 - The biggest new pop and all-day vibes", "bbc_radio_one", true},
		{"BBC Radio 6 Music – The best new music", "bbc_6music", true},
		{"BBC Radio 6 Music — The best new music", "bbc_6music", true},

		// No BBC in the name: it could be a station anywhere. A network the table does not know
		// falls back to iHeartRadio and TuneIn, where the match is guarded.
		{"Radio 4", "", false},
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

// TestBBCNetworksAgree catches an alias added with a copy-pasted service or logo, and a key that
// is not in the spelling bbcNetworkFor produces (so the table would never be reached).
func TestBBCNetworksAgree(t *testing.T) {
	byService := map[string]bbcNetwork{}
	for name, n := range bbcNetworks {
		if n.service == "" || n.logo == "" {
			t.Errorf("bbcNetworks[%q] is missing a service or logo", name)
		}
		if prev, ok := byService[n.service]; ok && prev != n {
			t.Errorf("bbcNetworks[%q] and another alias for %s disagree: %+v vs %+v", name, n.service, prev, n)
		}
		byService[n.service] = n
		if got := bbcNetworkName("BBC Radio " + name); got != name {
			t.Errorf("bbcNetworkName(%q) = %q; want the table key %q", "BBC Radio "+name, got, name)
		}
	}
}

func TestSplitName(t *testing.T) {
	tests := []struct {
		label, name, service string
	}{
		{"101.1 WXYZ on iHeartRadio", "101.1 WXYZ", "iHeartRadio"},
		{"101.1 WXYZ on iheartradio", "101.1 WXYZ", "iHeartRadio"},
		{"101.1 WXYZ on iHeart", "101.1 WXYZ", "iHeartRadio"},
		{"BBC Radio 6 Music on TuneIn", "BBC Radio 6 Music", "TuneIn"},
		{"BBC Radio 6 Music on Tune In", "BBC Radio 6 Music", "TuneIn"},
		{"Magic 105.4 on tunein", "Magic 105.4", "TuneIn"},
		{"  KEXP  ", "KEXP", ""},
		{"", "", ""},
	}
	for _, c := range tests {
		name, service := SplitName(c.label)
		if name != c.name || service != c.service {
			t.Errorf("SplitName(%q) = %q, %q; want %q, %q", c.label, name, service, c.name, c.service)
		}
	}
}

func TestMatchEnough(t *testing.T) {
	tests := []struct {
		query, candidate string
		want             bool
	}{
		// The list has more detail than the service's own name.
		{"Z100 New York", "Z100 WHTZ-FM", true},
		{"KCRW 89.9 FM", "89.9 KCRW KCRW-FM", true},
		{"Magic 105.4", "Magic Radio", true},
		{"KEXP: where the music matters", "KEXP 90.3 KEXP-FM", true},
		{"Heart London 106.2", "Heart London", true},
		{"98.7 WZZZ", "98.7 WZZZ WZZZ-FM", true},

		// A matching name on another dial is another station.
		{"Magic 105.4", "Magic 96.5 WMJJ-FM", false},
		{"Heart London 106.2", "Heart 106.2", false},

		// A name with words added is another station unless another word identifies it.
		{"Absolute Radio", "Absolute Classic Hits ACH-FL", false},
		{"Absolute Radio", "Absolute Radio 90s", false},
		{"Absolute Radio", "Absolute Radio", true},

		// A directory's name for the same station adds a call sign, a city or a slogan word.
		{"Hot 97", "HOT 97 WQHT", true},
		{"Power 106 FM", "Power 106 KPWR", true},
		{"Kiss FM", "KISS 100 London", true},
		{"Capital FM", "Capital London", true},
		{"Heart", "Heart London", true},
		{"The Beat 100.3", "100.3 The Beat WBEAT", true},
		{"Greatest Hits Radio", "The Bay's Greatest Hits", false},
		{"Heart", "Heart 80s", false},

		// Sharing the words every list has is not enough.
		{"BBC Radio 6 Music", "BBC KUT 90.5 HD2 KUT-HD2", false},
		{"BBC Radio 4", "BBC KUT 90.5 HD2 KUT-HD2", false},
		{"BBC Radio 1", "BBC Radio 1Xtra", false},
		{"The UK 1940s Radio Station", "FRED FILM RADIO", false},
		{"BBC Radio 5 Live", "BBC Radio 5 Live", true},
		{"BBC Radio 5 Live Sports Extra", "BBC Radio 5 Sports Extra", false},
	}
	for _, c := range tests {
		if got := matchEnough(c.query, c.candidate); got != c.want {
			t.Errorf("matchEnough(%q, %q) = %v; want %v", c.query, c.candidate, got, c.want)
		}
	}
}

func TestSameName(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"Magic 105.4", "Magic Radio", true},
		{"Magic 105.4", "Magic 96.5", false},
		{"KCRW 89.9 FM", "89.9 KCRW", true},
		{"98.7 WZZZ", "98.7 WZZZ WZZZ-FM", true},
		{"Absolute Radio", "Absolute Radio 90s", false},
		{"Z100 New York", "Z100", false},
	}
	for _, c := range tests {
		if got := sameName(c.a, c.b); got != c.want {
			t.Errorf("sameName(%q, %q) = %v; want %v", c.a, c.b, got, c.want)
		}
	}
}
