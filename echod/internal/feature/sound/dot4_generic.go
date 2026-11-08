//go:build !arm || noasm

package sound

// dot4I8 accumulates four int8 dot products of length n (n%8 == 0, n >= 8) into out[0..3].
func dot4I8(x, w0, w1, w2, w3 []int8, out *[4]int32) {
	n := len(x)
	if n == 0 {
		return
	}
	_ = w0[n-1]
	_ = w1[n-1]
	_ = w2[n-1]
	_ = w3[n-1]
	a0, a1, a2, a3 := out[0], out[1], out[2], out[3]
	for i := 0; i < n; i++ {
		xv := int32(x[i])
		a0 += xv * int32(w0[i])
		a1 += xv * int32(w1[i])
		a2 += xv * int32(w2[i])
		a3 += xv * int32(w3[i])
	}
	out[0], out[1], out[2], out[3] = a0, a1, a2, a3
}
