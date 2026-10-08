package config

import "testing"

func TestDeckGridDefaultsAndRange(t *testing.T) {
	if c, r := (Deck{}).Grid(); c != DeckColsDefault || r != DeckRowsDefault {
		t.Errorf("default grid %dx%d", c, r)
	}
	if c, r := (Deck{Cols: 99, Rows: -3}).Grid(); c != DeckColsMax || r != DeckRowsMin {
		t.Errorf("clamped grid %dx%d", c, r)
	}
}

func TestDeckSetOnlyWithAnActionInTheGrid(t *testing.T) {
	d := Deck{Cols: 2, Rows: 1, Pages: []DeckPage{{Buttons: []DeckButton{{Label: "no action"}, {}, {Action: DeckOBSStream}}}}}
	if d.Set() {
		t.Error("a button past the grid counted")
	}
	d.Pages[0].Buttons[1].Action = DeckOBSRecord
	if !d.Set() {
		t.Error("a button with an action did not count")
	}
}

func TestDeckButtonsAndPages(t *testing.T) {
	st := testStore(t)
	w := st.Set().Deck()
	if err := w.Button(1, 3, DeckButton{Label: "BRB", Action: DeckOBSScene, Scene: "BRB", Color: "pink"}); err != nil {
		t.Fatal(err)
	}
	d := st.Get().Deck
	if len(d.Pages) != 2 || len(d.Pages[1].Buttons) != 4 {
		t.Fatalf("pages %+v", d.Pages)
	}
	if b := d.Button(1, 3); b.Scene != "BRB" || b.Color != "" {
		t.Errorf("button %+v (an unknown color should be dropped)", b)
	}
	if err := w.Button(0, 0, DeckButton{Action: "rm -rf"}); err != nil {
		t.Fatal(err)
	}
	if b := st.Get().Deck.Button(0, 0); b.Action != DeckNone {
		t.Errorf("an unknown action was kept: %q", b.Action)
	}

	// A snapshot is a copy: changing it leaves the store alone.
	snap := st.Get().Deck
	snap.Pages[1].Buttons[3].Label = "changed"
	if st.Get().Deck.Button(1, 3).Label != "BRB" {
		t.Error("a snapshot shared the store's buttons")
	}

	if err := w.RemovePage(0); err != nil {
		t.Fatal(err)
	}
	if b := st.Get().Deck.Button(0, 3); b.Label != "BRB" {
		t.Errorf("after removing page 0, page 1 did not move up: %+v", b)
	}
}

func TestDeckOBSPasswordKeptWhenLeftEmpty(t *testing.T) {
	st := testStore(t)
	w := st.Set().Deck()
	_ = w.OBS("192.168.1.20", "secret")
	_ = w.OBS("192.168.1.21", "")
	if o := st.Get().Deck.OBS; o.Addr != "192.168.1.21" || o.Password != "secret" {
		t.Errorf("obs %+v", o)
	}
	_ = w.ForgetOBSPassword()
	if st.Get().Deck.OBS.Password != "" {
		t.Error("the password was not forgotten")
	}
}

func testStore(t *testing.T) *Store {
	t.Helper()
	st, err := Load(t.TempDir() + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestDeckComputers(t *testing.T) {
	st := testStore(t)
	w := st.Set().Deck()
	_ = w.PairComputer(DeckComputer{Name: "Office PC", Addr: "192.168.1.30", Key: "K1"})
	_ = w.PairComputer(DeckComputer{Name: "Office PC", Addr: "192.168.1.31", Key: "K2"})
	_ = w.PairComputer(DeckComputer{Name: "Laptop", Addr: "192.168.1.40", Key: "K3"})
	d := st.Get().Deck
	if c, ok := d.Computer("Office PC"); !ok || c.Addr != "192.168.1.31" || len(d.Computers) != 2 {
		t.Errorf("computers %+v", d.Computers)
	}
	_ = w.ForgetComputer("Office PC")
	if _, ok := st.Get().Deck.Computer("Office PC"); ok {
		t.Error("forgotten computer still paired")
	}
	if !DeckPCRun.OnComputer() || DeckOBSScene.OnComputer() {
		t.Error("OnComputer")
	}
}
