package sound

import "strings"

// TopClass is one scored AudioSet class from yamnet-tflite.
type TopClass struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Score float64 `json:"score"`
}

// EngineReply is one JSON line written by yamnet-tflite --stream.
type EngineReply struct {
	MS      float64    `json:"ms"`
	RMSDBFS float64    `json:"rms_dbfs"`
	Top     []TopClass `json:"top"`
}

// Detection is the room sound chosen for Home Assistant from one YAMNet window.
type Detection struct {
	Category   string
	Label      string
	RawClass   string
	Confidence float64
	Actionable bool
}

type rule struct {
	category   string
	label      string
	minScore   float64
	actionable bool
	ids        map[int]bool
}

func idSet(ids ...int) map[int]bool {
	m := make(map[int]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}

// Ordered from highest-priority specific/alert room events to general ambient states.
var rules = []rule{
	{
		category: "smoke_alarm", label: "Smoke / fire alarm", minScore: 0.20, actionable: true,
		ids: idSet(393, 394),
	},
	{
		category: "siren", label: "Siren", minScore: 0.22, actionable: true,
		ids: idSet(317, 318, 319, 390, 391),
	},
	{
		category: "glass", label: "Glass breaking", minScore: 0.22, actionable: true,
		ids: idSet(435, 436, 437),
	},
	{
		category: "baby_cry", label: "Baby cry", minScore: 0.22, actionable: true,
		ids: idSet(19, 20),
	},
	{
		category: "doorbell", label: "Doorbell", minScore: 0.22, actionable: true,
		ids: idSet(349, 195, 197),
	},
	{
		category: "alarm", label: "Alarm / ring", minScore: 0.24, actionable: true,
		ids: idSet(304, 382, 383, 384, 385, 389, 392, 475),
	},
	{
		category: "dog", label: "Dog", minScore: 0.22, actionable: true,
		ids: idSet(69, 70, 71, 72, 75, 117),
	},
	{
		category: "cat", label: "Cat", minScore: 0.22, actionable: true,
		ids: idSet(76, 77, 78, 80),
	},
	{
		category: "finger_snap", label: "Finger snapping", minScore: 0.20, actionable: true,
		ids: idSet(57),
	},
	{
		category: "clapping", label: "Clapping", minScore: 0.22, actionable: true,
		ids: idSet(56, 58, 62),
	},
	{
		category: "knock", label: "Knock", minScore: 0.22, actionable: true,
		ids: idSet(353, 354),
	},
	{
		category: "whistle", label: "Whistling", minScore: 0.22, actionable: true,
		ids: idSet(35, 396, 497),
	},
	{
		category: "cough", label: "Cough", minScore: 0.22, actionable: true,
		ids: idSet(42, 43),
	},
	{
		category: "sneeze", label: "Sneeze", minScore: 0.22, actionable: true,
		ids: idSet(44),
	},
	{
		category: "laughter", label: "Laughter", minScore: 0.24, actionable: true,
		ids: idSet(13, 14, 15, 17, 18),
	},
	{
		category: "door", label: "Door", minScore: 0.25, actionable: true,
		ids: idSet(348, 351, 356, 357),
	},
	{
		category: "water", label: "Running water", minScore: 0.28, actionable: false,
		ids: idSet(282, 364, 365, 368),
	},
	{
		category: "vacuum", label: "Vacuum cleaner", minScore: 0.28, actionable: false,
		ids: idSet(371),
	},
	{
		category: "typing", label: "Typing", minScore: 0.26, actionable: false,
		ids: idSet(378, 380),
	},
	{
		category: "snoring", label: "Snoring", minScore: 0.26, actionable: false,
		ids: idSet(38),
	},
	{
		category: "bird", label: "Bird", minScore: 0.28, actionable: false,
		ids: idSet(106, 107, 108),
	},
	{
		category: "speech", label: "Speech", minScore: 0.28, actionable: false,
		ids: idSet(0, 1, 2, 3, 4, 5, 65),
	},
	{
		category: "music", label: "Music", minScore: 0.30, actionable: false,
		ids: idSet(24, 29, 132, 133),
	},
}

func isMusicID(id int) bool {
	return (id >= 132 && id <= 276) || id == 24 || id == 29 || id == 30
}

func isNoiseFloorID(id int) bool {
	// Silence, Inside small/large room, Pink/White noise, Static, Hum
	switch id {
	case 494, 500, 501, 502, 506, 507, 513, 514, 515:
		return true
	}
	return false
}

// Classify maps YAMNet's top-k AudioSet scores to a clean room category and label using default thresholds.
func Classify(top []TopClass) Detection {
	return ClassifyWithMin(top, 0.25)
}

// ClassifyWithMin maps YAMNet's top-k AudioSet scores to a room category and label,
// scaling thresholds relative to minConf (where 0.25 is the calibrated default).
func ClassifyWithMin(top []TopClass, minConf float64) Detection {
	if len(top) == 0 {
		return Detection{Category: "quiet", Label: "Quiet"}
	}
	if minConf <= 0 {
		minConf = 0.25
	}
	scale := minConf / 0.25

	// Evaluate actionable and ambient category groups, combining hierarchical siblings.
	var best Detection
	bestIdx := len(rules)
	for i, r := range rules {
		var sum, topScore float64
		var raw string
		for _, tc := range top {
			match := r.ids[tc.ID] || (r.category == "music" && isMusicID(tc.ID))
			if !match {
				continue
			}
			sum += tc.Score
			if tc.Score > topScore {
				topScore = tc.Score
				raw = tc.Name
			}
		}
		if sum > 1.0 {
			sum = 1.0
		}
		req := r.minScore * scale
		if minConf > 0.25 && req < minConf {
			req = minConf
		}
		rawFloor := 0.14 * scale
		if rawFloor > req*0.8 {
			rawFloor = req * 0.8
		}
		// Require at least one individual class to be meaningful while rewarding sibling agreement.
		eff := topScore + 0.35*(sum-topScore)
		if eff < req || topScore < rawFloor {
			continue
		}
		// Actionable specific events beat ambient speech/music unless ambient is vastly stronger;
		// within the same tier, higher effective confidence wins.
		if best.Category == "" ||
			(r.actionable && !best.Actionable && eff >= req) ||
			(r.actionable == best.Actionable && eff > best.Confidence+0.05) ||
			(i < bestIdx && eff >= best.Confidence-0.05 && r.actionable == best.Actionable) {
			best = Detection{
				Category:   r.category,
				Label:      r.label,
				RawClass:   raw,
				Confidence: eff,
				Actionable: r.actionable,
			}
			bestIdx = i
		}
	}
	if best.Category != "" {
		return best
	}

	// Fallback: surface any other distinct AudioSet class (e.g., Microwave oven, Thunder, Guitar).
	fallbackMin := 0.35 * scale
	if minConf > 0.35 && fallbackMin < minConf {
		fallbackMin = minConf
	}
	t0 := top[0]
	if !isNoiseFloorID(t0.ID) && t0.Score >= fallbackMin {
		cat := strings.ToLower(t0.Name)
		cat = strings.NewReplacer(", ", "_", " ", "_", "(", "", ")", "", "/", "_").Replace(cat)
		return Detection{
			Category:   cat,
			Label:      t0.Name,
			RawClass:   t0.Name,
			Confidence: t0.Score,
			Actionable: false,
		}
	}

	return Detection{Category: "quiet", Label: "Quiet"}
}
