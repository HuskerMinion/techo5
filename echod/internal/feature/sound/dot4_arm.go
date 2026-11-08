//go:build arm && !noasm

package sound

//go:noescape
func dot4I8ASM(x, w0, w1, w2, w3 *int8, n int, out *[4]int32)

func dot4I8(x, w0, w1, w2, w3 []int8, out *[4]int32) {
	n := len(x)
	if n < 8 {
		for i := 0; i < n; i++ {
			xv := int32(x[i])
			out[0] += xv * int32(w0[i])
			out[1] += xv * int32(w1[i])
			out[2] += xv * int32(w2[i])
			out[3] += xv * int32(w3[i])
		}
		return
	}
	n8 := n &^ 7
	dot4I8ASM(&x[0], &w0[0], &w1[0], &w2[0], &w3[0], n8, out)
	for i := n8; i < n; i++ {
		xv := int32(x[i])
		out[0] += xv * int32(w0[i])
		out[1] += xv * int32(w1[i])
		out[2] += xv * int32(w2[i])
		out[3] += xv * int32(w3[i])
	}
}
