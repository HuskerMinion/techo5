package interpreter

import "math"

// QuantMultiplier holds a decomposed scale factor as (multiplier, shift)
// for efficient fixed-point arithmetic.
type QuantMultiplier struct {
	Multiplier int32
	Shift      int
}

// QuantizeMultiplier decomposes a float64 scale into a fixed-point
// multiplier (Q0.31 format) and an exponent shift.
func QuantizeMultiplier(realMultiplier float64) QuantMultiplier {
	if realMultiplier == 0 {
		return QuantMultiplier{0, 0}
	}
	sign := 1
	if realMultiplier < 0 {
		sign = -1
		realMultiplier = -realMultiplier
	}
	shift := 0
	for realMultiplier < 0.5 {
		realMultiplier *= 2.0
		shift--
	}
	for realMultiplier >= 1.0 {
		realMultiplier /= 2.0
		shift++
	}
	qMul := int64(math.Round(realMultiplier * float64(int64(1)<<31)))
	if qMul == int64(1)<<31 {
		qMul /= 2
		shift++
	}
	return QuantMultiplier{int32(qMul) * int32(sign), shift}
}

// MultiplyByQuantizedMultiplier performs the fixed-point multiply with rounding.
func MultiplyByQuantizedMultiplier(x int32, qm QuantMultiplier) int32 {
	product := int64(x) * int64(qm.Multiplier)
	totalShift := 31 - qm.Shift
	if totalShift > 0 {
		round := int64(1) << uint(totalShift-1)
		product += round
		product >>= uint(totalShift)
	} else if totalShift < 0 {
		product <<= uint(-totalShift)
	}
	return saturateInt32(product)
}

func saturateInt32(v int64) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return int32(v)
}

// ClampInt8 clamps a value to int8 range.
func ClampInt8(v int32) int8 {
	if v < -128 {
		return -128
	}
	if v > 127 {
		return 127
	}
	return int8(v)
}

// ClampUint8 clamps a value to uint8 range.
func ClampUint8(v int32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// Logistic computes 1/(1+exp(-x)).
func Logistic(x float32) float32 {
	return float32(1.0 / (1.0 + math.Exp(float64(-x))))
}
