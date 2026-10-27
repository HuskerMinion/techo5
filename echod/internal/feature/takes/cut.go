package takes

import (
	"math"
	"slices"
	"time"
)

// Cutting a recording into takes.
//
// A take is a stretch that stands out from the room around it. Everything is judged against the
// recording itself rather than against a fixed level: what the device records is leveled
// (hardware/mic level.go), so a quiet room and a loud one come out at about the same level, and the
// leveler's gain between words sits wherever the last word left it. So the room's floor is taken near
// each frame, a few seconds either side, and how far over it counts as a word from how far the loudest
// of the recording stood out over the room as a whole.
//
// The numbers below are a starting point; the comments say what each is for.

const (
	// shortest is a take that is a click or a cough rather than a word. "Stop" said quickly is about
	// a third of a second.
	shortest = 250 * time.Millisecond

	// longestTake is the most of a word, so that a take with its padding fits in the 3.2 seconds that
	// step 5 of training by hand (training-wake-word.md) cuts its clips to. Longer is somebody talking,
	// not a take.
	longestTake = 2600 * time.Millisecond

	// bridge is the longest dip inside one take: the gap between the words of "hey jarvis" or "turn it
	// up" is shorter than this, and the pause between takes much longer.
	bridge = 250 * time.Millisecond

	// pad is the silence kept before and after each take, never past halfway to the next.
	pad = 300 * time.Millisecond

	// widen is how far either side of a take the speaker is looked for: whether the device was making
	// a sound is read when the frame is taken, which can be up to the listener's eight frames after it
	// was heard.
	widen = 160 * time.Millisecond

	// soundTail is how long after the frames marked as heard over the speaker the device's sound may
	// still be in what was recorded, unmarked: widen, and the playback buffer sounding on after the
	// queue has run out (speaker.HardwareTail, 150 ms).
	soundTail = widen + 150*time.Millisecond

	// block and reach are the room's floor near a frame: the quiet of each second, and the quietest
	// second within reach of it either side. A take is never long enough to fill five seconds.
	block = time.Second
	reach = 2

	// floorPart is the part of a second's frames that are its quiet: a second with a word in it is
	// still mostly room.
	floorPart = 0.2

	// standOut is how far over the room's floor, in dB, the loudest of the recording has to be for
	// anything to count. Under it nothing was said near enough to the device.
	standOut = 12.0

	// mostTakes is the most a recording is cut into: two minutes of a word said every two seconds.
	mostTakes = 60
)

// span is a stretch of a recording, in frames: from up to and not including to.
type span struct{ from, to int }

// leftOut is a stretch heard and not kept as a take, and why.
type leftOut struct {
	span
	why string
}

// Why a stretch is left out.
const (
	whyShort   = "too short for a word"
	whyLong    = "too long for a word"
	whySpeaker = "said while the device was making a sound"
	whyCutOff  = "cut off by the end of the recording"
	whyCutIn   = "cut off by the start of the recording"
	whyMore    = "more than 60 takes"
)

// cut finds the takes in a recording's frame levels, each padded with the room either side, and what
// it heard and left out. over marks the frames heard while the device was making a sound.
func cut(level []float32, over []bool) (takes []span, left []leftOut) {
	n := len(level)
	if n == 0 {
		return nil, nil
	}
	smooth := make([]float64, n)
	for i := range level {
		lo, hi := max(i-1, 0), min(i+2, n)
		var sum float64
		for _, v := range level[lo:hi] {
			sum += float64(v)
		}
		smooth[i] = sum / float64(hi-lo)
	}

	floors := blockFloors(level, over)
	all := slices.DeleteFunc(slices.Clone(floors), func(v float64) bool { return math.IsInf(v, 1) })
	if len(all) == 0 {
		return nil, nil // all of it heard over the speaker
	}
	slices.Sort(all)
	room := max(all[len(all)/2], 1)
	// The loudest of what was said, not of what the device played: a reply over the speaker in the
	// middle would raise the bar for every take, and the far ones would fall under it. Nor the frames
	// just after it, which still carry its end (soundTail), or just before, which the smoothing carries
	// it into.
	loud := 0.0
	tailFrames := int(soundTail / frameTime)
	for i, v := range smooth {
		if !slices.Contains(over[max(i-tailFrames, 0):min(i+2, n)], true) {
			loud = max(loud, v)
		}
	}
	spread := 20 * math.Log10(loud/room)
	if spread < standOut {
		return nil, nil
	}
	on, off := thresholds(spread)

	perBlock := int(block / frameTime)
	floorAt := func(i int) float64 {
		b := i / perBlock
		near := math.Inf(1)
		for j := max(b-reach, 0); j <= min(b+reach, len(floors)-1); j++ {
			near = min(near, floors[j])
		}
		if math.IsInf(near, 1) {
			near = room
		}
		return max(near, 1)
	}

	padFrames, widenFrames := int(pad/frameTime), int(widen/frameTime)
	minFrames, maxFrames := int(math.Ceil(float64(shortest)/float64(frameTime))), int(longestTake/frameTime)

	found, cutOff := scan(smooth, 0, n, floorAt, on, off)
	// A stretch too long for a word may be the room itself stepping up: after a take said far away the
	// leveler holds the gain it raised, and the room comes up with it for longer than a word lasts but not
	// for long enough to move the floor a few seconds around it. Cut again inside it, against its own.
	var heard []span
	for k, h := range found {
		if h.to-h.from <= maxFrames || (cutOff && k == len(found)-1) {
			heard = append(heard, h)
			continue
		}
		if inner := recut(level, smooth, h); len(inner) > 0 {
			heard = append(heard, inner...)
		} else {
			heard = append(heard, h)
		}
	}

	for k, h := range heard {
		lo, hi := max(h.from-padFrames, 0), min(h.to+padFrames, n)
		if k > 0 {
			lo = max(lo, (heard[k-1].to+h.from+1)/2)
		}
		if k < len(heard)-1 {
			hi = min(hi, (h.to+heard[k+1].from)/2)
		}
		why := ""
		switch length := h.to - h.from; {
		case cutOff && k == len(heard)-1 && h.to == n:
			why = whyCutOff
		// Begun within a dip's length of the first frame kept: the word may well have begun in the lead-in
		// that was dropped (record.go), and a take missing its first sound would teach the model the
		// wrong word. One take of twenty or thirty is the cheaper thing to lose.
		case h.from <= int(bridge/frameTime):
			why = whyCutIn
		case length < minFrames:
			why = whyShort
		case length > maxFrames:
			why = whyLong
		case slices.Contains(over[max(lo-widenFrames, 0):min(hi+widenFrames, n)], true):
			why = whySpeaker
		case len(takes) == mostTakes:
			why = whyMore
		}
		if why != "" {
			left = append(left, leftOut{h, why})
			continue
		}
		takes = append(takes, span{lo, hi})
	}
	return takes, left
}

// thresholds is how far over the floor a word has to rise to start, and how far back it falls to end, for a
// recording whose loudest stood spread dB over its room: a word rises well over the room near it, and
// ends where it falls most of the way back.
func thresholds(spread float64) (on, off float64) {
	onDB := min(max(0.4*spread, 8), spread-3)
	offDB := min(max(0.25*spread, 5), onDB-2)
	return math.Pow(10, onDB/20), math.Pow(10, offDB/20)
}

// scan finds the stretches of smooth from up to to that stand out from the floor at each frame, with
// dips shorter than bridge inside them, and whether the last was still going at to.
func scan(smooth []float64, from, to int, floorAt func(int) float64, on, off float64) (found []span, going bool) {
	gapFrames := int(bridge / frameTime)
	in, start, lastLoud := false, from, from
	for i := from; i < to; i++ {
		fl := floorAt(i)
		if !in {
			if smooth[i] >= fl*on {
				in, start, lastLoud = true, i, i
				// Back to where it first rose out of the room, not past the stretch before.
				prev := from
				if len(found) > 0 {
					prev = found[len(found)-1].to
				}
				for start > prev && smooth[start-1] >= floorAt(start-1)*off {
					start--
				}
			}
			continue
		}
		if smooth[i] >= fl*off {
			lastLoud = i
		} else if i-lastLoud > gapFrames {
			found = append(found, span{start, lastLoud + 1})
			in = false
		}
	}
	if in {
		found = append(found, span{start, to})
	}
	return found, in
}

// innerStandOut is how far the loudest of a stretch has to stand over the stretch's own floor to be cut
// again (cut): less than over a whole recording, since a word said far away is quieter against the room.
const innerStandOut = 10.0

// recut is the takes inside h, a stretch too long for a word, when h is the room stepped up with a word
// or two in it; nothing when it is somebody talking on. Talking has dips between its syllables that a
// stretch's own floor would cut at, so what is not the room has to come to no more than a word's
// length, and what stands out of the room has to be apart as takes are, by more than the pause inside
// a word.
func recut(level []float32, smooth []float64, h span) []span {
	quiet := make([]float64, 0, h.to-h.from)
	for _, v := range level[h.from:h.to] {
		quiet = append(quiet, float64(v))
	}
	slices.Sort(quiet)
	fl := max(quiet[int(floorPart*float64(len(quiet)-1))], 1)
	spread := 20 * math.Log10(slices.Max(smooth[h.from:h.to])/fl)
	if spread < innerStandOut {
		return nil
	}
	on, off := thresholds(spread)
	roomish := 0
	for _, v := range smooth[h.from:h.to] {
		if v < fl*off {
			roomish++
		}
	}
	if h.to-h.from-roomish > int(longestTake/frameTime) {
		return nil
	}
	inner, _ := scan(smooth, h.from, h.to, func(int) float64 { return fl }, on, off)
	apart := 2 * int(bridge/frameTime)
	for k := 1; k < len(inner); k++ {
		if inner[k].from-inner[k-1].to < apart {
			return nil
		}
	}
	return inner
}

// blockFloors is the quiet of each second of a recording: the level floorPart of the way up its
// frames, leaving out those heard over the speaker. A second heard all over the speaker has none.
func blockFloors(level []float32, over []bool) []float64 {
	perBlock := int(block / frameTime)
	var out []float64
	for from := 0; from < len(level); from += perBlock {
		to := min(from+perBlock, len(level))
		var quiet []float64
		for i := from; i < to; i++ {
			if !over[i] {
				quiet = append(quiet, float64(level[i]))
			}
		}
		if len(quiet) == 0 {
			out = append(out, math.Inf(1))
			continue
		}
		slices.Sort(quiet)
		out = append(out, quiet[int(floorPart*float64(len(quiet)-1))])
	}
	return out
}
