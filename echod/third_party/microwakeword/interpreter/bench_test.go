package interpreter

import (
	"math/rand"
	"os"
	"testing"

	"github.com/zserge/microwakeword/model"
)

// BenchmarkInvoke runs the alexa model on random features: what one wake-word step costs.
func BenchmarkInvoke(b *testing.B) {
	data, err := os.ReadFile("../testdata/alexa.tflite")
	if err != nil {
		b.Skip(err)
	}
	m, err := model.Parse(data)
	if err != nil {
		b.Fatal(err)
	}
	in, err := New(m)
	if err != nil {
		b.Fatal(err)
	}
	r := rand.New(rand.NewSource(1))
	feat := make([]int8, 40*3)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range feat {
			feat[j] = int8(r.Intn(256) - 128)
		}
		in.SetInput(feat)
		if err := in.Invoke(); err != nil {
			b.Fatal(err)
		}
	}
}
