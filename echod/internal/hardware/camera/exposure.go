//go:build !dot && !spot

package camera

import "math"

// Metering. A plain mean of the frame lets a window behind somebody set the exposure: the window is
// most of the light, so the loop darkens until the window looks right and the face is a silhouette.
// The Show sits on a desk facing whoever uses it, so the frame is metered in zones, the middle
// counting for more than the edges, and zones near clipping (a window, a lamp) counting for little.
const (
	zoneCols, zoneRows = 8, 6
	meterRowStep       = 16  // sensor rows between samples (even rows: R G R G …)
	meterGroupStep     = 4   // five-byte groups between samples, 16 pixels
	clipLevel          = 870 // a zone this bright is a light source rather than the scene
	clipWeight         = 0.15
	centerBoost        = 3.0  // how much more the middle counts than the edges
	centerSpread       = 0.35 // of the frame's half-size, squared: how wide "the middle" is
)

// meter is the frame's brightness on the 10-bit scale as the exposure loop should see it. A frame that is
// white everywhere still meters high, since a clipped zone counts for less but not for nothing.
func meter(bayer []byte) float64 {
	var sum [zoneRows][zoneCols]float64
	var count [zoneRows][zoneCols]float64
	groups := sensorW / 4
	for y := 0; y+1 < sensorH; y += meterRowStep {
		line := bayer[y*bytesPerLine:]
		zy := y * zoneRows / sensorH
		for g := 0; g < groups; g += meterGroupStep {
			i := g * 5
			if i+2 >= len(line) {
				break
			}
			// The group's second pixel: green on an even row.
			v := float64(uint16(line[i+1])>>2 | (uint16(line[i+2])&0xF)<<6)
			zx := g * zoneCols / groups
			sum[zy][zx] += v
			count[zy][zx]++
		}
	}
	var weighted, weights float64
	for zy := 0; zy < zoneRows; zy++ {
		for zx := 0; zx < zoneCols; zx++ {
			if count[zy][zx] == 0 {
				continue
			}
			mean := sum[zy][zx] / count[zy][zx]
			cx := (float64(zx)+0.5)/zoneCols*2 - 1
			cy := (float64(zy)+0.5)/zoneRows*2 - 1
			w := 1 + centerBoost*math.Exp(-(cx*cx+cy*cy)/centerSpread)
			if mean >= clipLevel {
				w *= clipWeight
			}
			weighted += w * mean
			weights += w
		}
	}
	if weights == 0 {
		return 0
	}
	return weighted / weights
}

// Tone. With the white point at the frame's top percentile, a backlit frame spends most of the output
// range on the window. When the top of the frame is far above the middle of the picture — the median of
// its central quarter, where a face is — the gamma steepens so that middle comes up and the window is
// left to roll off.
const (
	gammaNormal = 1 / 1.8
	hdrRatio    = 3.0  // top percentile over median before the curve changes: a lit room sits at 2-3
	hdrStrength = 0.45 // how fast the curve steepens past it, per doubling of the ratio
	gammaMaxAdd = 0.9  // at most 1/2.7
)

// gammaFor is the output gamma for a frame whose central median and top percentile (summed green) are
// given.
func gammaFor(median, top int) float64 {
	r := float64(top) / math.Max(float64(median), 1)
	if r <= hdrRatio {
		return gammaNormal
	}
	add := math.Min(hdrStrength*math.Log2(r/hdrRatio), gammaMaxAdd)
	return 1 / (1.8 + add)
}

// Black. The sensor's floor sits above zero — the darkest pixels of a real room read about 43 on the
// summed-green scale (2026-09-16) — and with nothing mapped to black the picture looks milky, more so
// under a steeper gamma. The frame's darkest 0.1% is taken as black, but never more than blackCap: a
// frame with nothing dark in it must not have its shadows cut.
const blackCap = 64

func blackFor(low, white int) int {
	return max(0, min(low, blackCap, white/4))
}
