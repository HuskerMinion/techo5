package config

import "testing"

func TestIntercomAutoAnswerDelay(t *testing.T) {
	st := load(t)

	valeurs := []int{0, 2, 3, 5, 10, 15}

	for _, valeur := range valeurs {
		err := st.Set().Home().IntercomAutoAnswerDelay(valeur)
		if err != nil {
			t.Errorf("Le délai %d devrait être accepté : %v", valeur, err)
			continue
		}

		obtenu := st.Get().Home.IntercomAutoAnswerDelay
		if obtenu != valeur {
			t.Errorf("Délai obtenu : %d, attendu : %d", obtenu, valeur)
		}
	}
}

func TestDelaiInvalide(t *testing.T) {
	st := load(t)

	valeurs := []int{-1, 1, 4, 7, 20}

	for _, valeur := range valeurs {
		err := st.Set().Home().IntercomAutoAnswerDelay(valeur)

		if err == nil {
			t.Errorf("Le délai %d aurait dû être refusé", valeur)
		}
	}
}

func TestEnregistrementDelai(t *testing.T) {
	st := load(t)

	err := st.Set().Home().IntercomAutoAnswerDelay(5)

	if err != nil {
		t.Fatalf("Impossible d'enregistrer le délai : %v", err)
	}

	delai := st.Get().Home.IntercomAutoAnswerDelay

	if delai != 5 {
		t.Errorf("Délai obtenu : %d, attendu : 5", delai)
	}
}

func TestRefusDelaiInvalide(t *testing.T) {
	st := load(t)

	// Enregistrer d'abord un délai valide.
	err := st.Set().Home().IntercomAutoAnswerDelay(5)
	if err != nil {
		t.Fatal(err)
	}

	// Essayer ensuite d'enregistrer une valeur interdite.
	err = st.Set().Home().IntercomAutoAnswerDelay(7)

	if err == nil {
		t.Error("Le délai 7 aurait dû être refusé")
	}

	// Vérifier que le délai initial est conservé.
	delai := st.Get().Home.IntercomAutoAnswerDelay

	if delai != 5 {
		t.Errorf("Délai modifié : %d, attendu : 5", delai)
	}
}
