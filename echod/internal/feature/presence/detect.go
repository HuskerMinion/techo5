//go:build !dot

package presence

import "slices"

// detector tells somebody moving from a still room in a stream of brightness grids, and from what the
// light does to the camera. Measured on a Show 8 (2026-10-04, a recording of the light switched on and
// off four times and somebody walking up): an LED bulb flickers in bands across the picture, so a
// still, lit room shows 3 to 5% of its cells changing frame to frame, all in thin bands; a light
// switched on or off swings the whole picture for three to five frames while the exposure settles;
// somebody walking up changes a patch of the picture, 50 to 300 cells joined together, for seconds.
//
// So each frame is compared with the last in proportion (the exposure scales the whole picture), each
// row's and then each column's own change is taken out (the flicker's bands), and what is left is a
// cell that moved, beyond what that cell usually does: each cell learns its own noise from frames with
// nothing moving, so a TV, a fan or a flickering lamp in one part of the room raises only its own
// cells' bar. A frame has movement when the largest patch of joined cells that moved is big enough,
// and only when the picture as a whole held steady (a frame of the exposure swinging is not counted,
// either way). Somebody is near when two of the last three counted frames had movement.
type detector struct {
	prev   []uint8
	recent [3]bool
	n      int
	noise  []float64 // each cell's usual change on a still frame (exponential average)

	// patchMin is how many joined cells make movement: the sensitivity (see patchFor).
	patchMin int
}

// patchFor is the patch size for a sensitivity of 1 to 100: 50 is defaultPatch cells, 100 is 6, and 1
// is 60.
func patchFor(sensitivity int) int {
	s := min(max(sensitivity, 1), 100)
	if s >= 50 {
		return defaultPatch - (defaultPatch-6)*(s-50)/50
	}
	return 60 - (60-defaultPatch)*(s-1)/49
}

const (
	cellStep     = 25   // a cell's change, on 0-255, left after the picture's, the row's and the column's
	saturated    = 235  // a cell this bright has no change left to show
	defaultPatch = 20   // joined cells that moved, for a frame to have movement, at sensitivity 50 (2.6%)
	noiseTimes   = 3    // a cell has moved when its change is this many times its usual noise, at least
	steadyLo     = 0.80 // the picture's own change, in proportion, within which a frame is counted
	steadyHi     = 1.25
	brightMax    = 200 // a picture this bright on average is the exposure not yet caught up
)

// step takes the next grid and reports whether somebody is moving in front of the camera.
func (d *detector) step(cur []uint8) bool {
	if len(cur) != gridW*gridH {
		return false
	}
	prev := d.prev
	d.prev = append(d.prev[:0:0], cur...)
	if len(prev) != len(cur) {
		return false
	}
	var sumPrev, sumCur int
	for i := range cur {
		sumPrev += int(prev[i])
		sumCur += int(cur[i])
	}
	ratio := 1.0
	if sumPrev > 4*len(cur) {
		ratio = float64(sumCur) / float64(sumPrev)
	}
	steady := ratio >= steadyLo && ratio <= steadyHi &&
		sumCur < brightMax*len(cur) && sumPrev < brightMax*len(cur)
	if !steady {
		return d.hits() >= 2 // not counted: the last counted frames stand
	}

	res := make([]float64, len(cur))
	for i := range cur {
		res[i] = float64(cur[i]) - float64(prev[i])*ratio
	}
	buf := make([]float64, 0, max(gridW, gridH))
	for y := range gridH {
		row := res[y*gridW : (y+1)*gridW]
		m := median(buf, row)
		for x := range row {
			row[x] -= m
		}
	}
	col := make([]float64, gridH)
	for x := range gridW {
		for y := range gridH {
			col[y] = res[y*gridW+x]
		}
		m := median(buf, col)
		for y := range gridH {
			res[y*gridW+x] -= m
		}
	}
	if d.noise == nil {
		d.noise = make([]float64, len(cur))
		for i := range d.noise {
			d.noise[i] = cellStep / noiseTimes
		}
	}
	moved := make([]bool, len(cur))
	for i, v := range res {
		bright := min(float64(cur[i]), float64(prev[i])*ratio)
		step := max(cellStep, noiseTimes*d.noise[i])
		moved[i] = (v > step || v < -step) && bright < saturated
	}
	need := d.patchMin
	if need == 0 {
		need = defaultPatch
	}
	movement := largestPatch(moved) >= need
	if !movement {
		// A still frame teaches each cell what it does on its own.
		for i, v := range res {
			if v < 0 {
				v = -v
			}
			d.noise[i] += (min(v, 60) - d.noise[i]) * 0.05
		}
	}
	d.recent[d.n%3] = movement
	d.n++
	return d.hits() >= 2
}

func (d *detector) hits() int {
	n := 0
	for _, r := range d.recent {
		if r {
			n++
		}
	}
	return n
}

// median of v, sorted in buf.
func median(buf, v []float64) float64 {
	buf = append(buf[:0], v...)
	slices.Sort(buf)
	return buf[len(buf)/2]
}

// largestPatch is the size of the largest set of moved cells joined side by side.
func largestPatch(moved []bool) int {
	seen := make([]bool, len(moved))
	stack := make([]int, 0, len(moved))
	best := 0
	for start, m := range moved {
		if !m || seen[start] {
			continue
		}
		seen[start] = true
		stack = append(stack[:0], start)
		size := 0
		for len(stack) > 0 {
			i := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			size++
			x := i % gridW
			next := [4]int{i - gridW, i + gridW, -1, -1}
			if x > 0 {
				next[2] = i - 1
			}
			if x < gridW-1 {
				next[3] = i + 1
			}
			for _, j := range next {
				if j >= 0 && j < len(moved) && moved[j] && !seen[j] {
					seen[j] = true
					stack = append(stack, j)
				}
			}
		}
		best = max(best, size)
	}
	return best
}
