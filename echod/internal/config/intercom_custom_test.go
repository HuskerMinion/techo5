package config

import "testing"

func TestIntercomAutoAnswerDelayAcceptedValues(t *testing.T) {
	st := load(t)
	for _, seconds := range []int{0, 2, 3, 5, 10, 15} {
		if err := st.Set().Home().IntercomAutoAnswerDelay(seconds); err != nil {
			t.Fatalf("delay %d should be accepted: %v", seconds, err)
		}
		if got := st.Get().Home.IntercomAutoAnswerDelay; got != seconds {
			t.Fatalf("got delay %d, want %d", got, seconds)
		}
	}
}

func TestIntercomAutoAnswerDelayRejectsInvalidValues(t *testing.T) {
	st := load(t)
	if err := st.Set().Home().IntercomAutoAnswerDelay(5); err != nil {
		t.Fatal(err)
	}
	for _, seconds := range []int{-1, 1, 4, 7, 20} {
		if err := st.Set().Home().IntercomAutoAnswerDelay(seconds); err == nil {
			t.Errorf("delay %d should be rejected", seconds)
		}
		if got := st.Get().Home.IntercomAutoAnswerDelay; got != 5 {
			t.Fatalf("invalid delay %d changed setting to %d", seconds, got)
		}
	}
}
