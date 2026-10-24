package wakeword

import "time"

// Scores is what the words the device listens for have been scoring lately, for the setup page to show
// while somebody picks a threshold by watching instead of by reading the device's log. It is numbers
// only: no audio is kept for it. Detection fills it in (ReadScores).
type Scores struct {
	// Bucket is the newest stretch of BucketMs the peaks reach, counted from the Unix epoch, and From the
	// first one each row's Best starts at.
	Bucket   int64 `json:"bucket"`
	From     int64 `json:"from"`
	BucketMs int   `json:"bucket_ms"`
	// Muted is the microphones being cut: nothing is scored then.
	Muted bool `json:"muted"`
	// Lost is a read from further back than is still kept, so what came in between is missing.
	Lost   bool         `json:"lost"`
	Rows   []ScoreRow   `json:"rows"`
	Events []ScoreEvent `json:"events"`
}

// ScoreRow is one word being listened for.
type ScoreRow struct {
	// Slot is the slot as the device's log numbers it (slot=104 for the volume up word), so a line in
	// the log and a row here can be told to be the same word.
	Slot   int    `json:"slot"`
	Role   string `json:"role"`
	ID     string `json:"id"`
	Phrase string `json:"phrase"`
	// Ready is the model having scored once: until then the word cannot be heard at all.
	Ready bool `json:"ready"`
	// Threshold is the word's own, and Cutoff what a score is judged against now. Slack is the two
	// differing: the allowance a word gets while the speaker plays and the echo canceller runs.
	Threshold float64 `json:"threshold"`
	Cutoff    float64 `json:"cutoff"`
	Slack     bool    `json:"slack"`
	// Best is the highest score heard in each bucket from From to Bucket, and Settling whether any of
	// it was heard while the word's detections were held back: just after a detection, or while the
	// device's own sound plays after an unmute.
	Best     []float32 `json:"best"`
	Settling []bool    `json:"settling"`
}

// ScoreEvent is one of the lines the device's log has about a word, with the same numbers: detected,
// near miss, unsure (a score between the confirm threshold and the word's own, the log's "wake
// candidate"), ready or dropped.
type ScoreEvent struct {
	Seq       uint64    `json:"seq"`
	At        time.Time `json:"at"`
	Slot      int       `json:"slot"`
	What      string    `json:"what"`
	ID        string    `json:"id"`
	Peak      float64   `json:"peak,omitempty"`
	Crossing  float64   `json:"crossing,omitempty"`
	Cutoff    float64   `json:"cutoff,omitempty"`
	AskedFrom float64   `json:"asked_from,omitempty"`
}

// ReadScores is the scores now: the events after the one numbered after (zero for every one still
// kept), and each word's peaks from the bucket from on (zero for as far back as they go). watch is a
// page drawing them as they come, which keeps the peaks being kept for a little longer; the plain page
// reads without it. Detection sets it.
var ReadScores func(after uint64, from int64, watch bool) Scores
