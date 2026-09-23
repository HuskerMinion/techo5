package config

import "testing"

// A reminder's list of devices is a slice inside each alarm, so copying the alarm list is not enough:
// both a caller's snapshot and what was handed to Put must stay apart from the store's own.
func TestAReminderKeepsItsOwnListOfDevices(t *testing.T) {
	st := load(t)

	given := []string{"Kitchen", "Office"}
	if err := st.Set().Alarms().Put(Alarm{ID: "r1", Hour: 14, Minute: 30, Label: "Pills", On: true, Remind: true, RingOn: given}); err != nil {
		t.Fatal(err)
	}
	given[0] = "scribbled by the caller"

	held := st.Get().Alarms.List[0]
	if !held.Remind || held.RingOn[0] != "Kitchen" {
		t.Fatalf("read back %+v", held)
	}
	held.RingOn[1] = "scribbled on the snapshot"

	if got := st.Get().Alarms.List[0].RingOn; got[0] != "Kitchen" || got[1] != "Office" {
		t.Errorf("the store's list now reads %v", got)
	}
}
