//go:build linux && !dot

package person

// The model on the VPU, where there is one (the 2nd gen Show 8's Vision P6, hardware/vpu;
// custom-kernel/athena/research/vpu-vision-p6.md §16-17): its 27 convolutions run there as one
// command, in about 2.5 ms and 0.25 ms of CPU time against 17 ms for the whole model on a core, and
// the little that follows them (the average pool, the last 1x1 to two classes, the softmax) runs
// here.
//
// The VPU's runtime (apunn) takes NNAPI 1.1's uint8, one scale a tensor. The activations convert
// exactly (uint8 = int8 + 128, at the same scale); the weights, quantized per channel in the model,
// are quantized again per tensor, the first layers equalized first (equalize). On 512 pictures of the
// unit's own camera and 26 of a portrait and a room, that moved a score by 0.04 on average and 0.14
// at worst, and changed no look's side of the judge's threshold (§17). The VPU computes exactly what
// cpu below computes.
//
// Any error from the VPU and the model goes back to the cores for good (until echod starts again),
// said once. The VPU is powered only while it is used: opened at the first look, let go after
// vpuIdle without one and when looking stops. It is shared with the camera stream's noise reduction
// in dim light (vpu.Acquire; feature/camstream's nr.go), each loading its algorithm in turn.

import (
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/HuskerMinion/techo5/echod/internal/hardware/vpu"
	"github.com/HuskerMinion/techo5/echod/internal/lib/tflite"
)

// vpuIdle is how long the VPU stays powered after the last look.
const vpuIdle = 30 * time.Second

// vpuTimeout is how long one inference may take on the VPU before it counts as failed (it takes
// about 2.5 ms).
const vpuTimeout = 500

var (
	// vpuFailed is set once the VPU failed, and the model runs on the cores from then on.
	vpuFailed atomic.Bool
	// vpuSaid is whether the VPU being used has been said, once a run.
	vpuSaid atomic.Bool

	// vpuAvailable is whether the board has the VPU's driver. Replaced in tests.
	vpuAvailable = vpu.Available
	// openTrunk powers the VPU up and readies the convolutions on it. Replaced in tests.
	openTrunk = openVPUTrunk

	quantOnce sync.Once
	quant     *quantModel
	quantErr  error
)

// trunk runs the model's convolutions somewhere other than the cores.
type trunk interface {
	// run runs them on a picture (96x96 uint8), writing the last one's output to out.
	run(gray, out []uint8) error
	close()
}

// accel is the model on the VPU, with the CPU's float model behind it.
type accel struct {
	q    *quantModel
	t    trunk     // nil while the VPU is let go
	used time.Time // the last look
	out  []uint8
}

// newAccel is the model on the VPU, or nil where there is none or it failed before.
func newAccel(m *tflite.Model, in *tflite.Interpreter) *accel {
	if vpuFailed.Load() || !vpuAvailable() {
		return nil
	}
	quantOnce.Do(func() { quant, quantErr = quantize(m, in) })
	if quantErr != nil {
		vpuFail(quantErr)
		return nil
	}
	return &accel{q: quant, out: make([]uint8, quant.trunkOut())}
}

func vpuFail(err error) {
	if !vpuFailed.Swap(true) {
		slog.Warn("person detection: the VPU failed; the model runs on the cores from now on", "err", err)
	}
}

// score runs the model on the VPU, powering it up first if it is not; false if it failed, and it
// is not to be used again.
func (a *accel) score(gray []uint8, now time.Time) (float32, bool) {
	if vpuFailed.Load() {
		return 0, false
	}
	if a.t == nil {
		began := time.Now()
		t, err := openTrunk(a.q)
		if err != nil {
			vpuFail(err)
			return 0, false
		}
		a.t = t
		if !vpuSaid.Swap(true) {
			slog.Info("person detection: the model runs on the VPU", "started_in", time.Since(began).Round(time.Millisecond))
		} else {
			slog.Debug("person detection: the VPU powered up", "in", time.Since(began).Round(time.Millisecond))
		}
	}
	a.used = now
	if err := a.t.run(gray, a.out); err != nil {
		a.close()
		vpuFail(err)
		return 0, false
	}
	return a.q.tail(a.out), true
}

// idle lets the VPU go once it has not been used for vpuIdle.
func (a *accel) idle(now time.Time) {
	if a.t != nil && now.Sub(a.used) >= vpuIdle {
		slog.Debug("person detection: the VPU powered down")
		a.close()
	}
}

func (a *accel) close() {
	if a.t != nil {
		a.t.close()
		a.t = nil
	}
}

// vpuTrunk is the convolutions placed on the VPU, apunn booted and loaded.
type vpuTrunk struct {
	d   *vpu.Device
	net *vpu.Net
}

var (
	netMu  sync.Mutex
	netFor vpu.LocalMem
	netAt  *vpu.Net
)

func openVPUTrunk(q *quantModel) (trunk, error) {
	d, lm, err := vpu.OpenAPUNN()
	if err != nil {
		return nil, err
	}
	// The kernels' tiling depends on the DSP's local memory only: worked out once.
	netMu.Lock()
	if netAt == nil || netFor != lm {
		netAt, err = vpu.PrepareNet(q.convs, lm)
		netFor = lm
		if err != nil {
			netAt = nil
		}
	}
	n := netAt
	netMu.Unlock()
	if err == nil {
		err = d.Place(n)
	}
	if err != nil {
		d.Close()
		return nil, err
	}
	return &vpuTrunk{d: d, net: n}, nil
}

func (t *vpuTrunk) run(gray, out []uint8) error { return t.d.RunNet(t.net, gray, out, vpuTimeout) }

func (t *vpuTrunk) close() { t.d.Close() }

// quantModel is the model in NNAPI 1.1's uint8: its convolutions, then what follows them.
type quantModel struct {
	convs []*vpu.Conv
	// final is the 1x1 to two classes, after the average pool (which keeps the last conv's
	// quantization).
	final                 *vpu.Conv
	logitScale, logitZero float64
}

func (q *quantModel) trunkOut() int {
	c := q.convs[len(q.convs)-1]
	return c.OutW() * c.OutH() * c.OutC
}

// quantize converts the model: its CONV_2D and DEPTHWISE_CONV_2D in order, then a global
// AVERAGE_POOL_2D, a 1x1 CONV_2D, RESHAPE and SOFTMAX. Anything else is not this model.
func quantize(m *tflite.Model, in *tflite.Interpreter) (*quantModel, error) {
	if len(m.Subgraphs) != 1 {
		return nil, errors.New("person detection: not the model expected")
	}
	g := m.Subgraphs[0]
	act := func(i int) (float64, uint8, error) {
		d := g.Tensors[i]
		if d.Type != tflite.Int8 || len(d.Scale) != 1 || len(d.ZeroPoint) != 1 {
			return 0, 0, fmt.Errorf("person detection: tensor %d is not int8 per tensor", i)
		}
		return float64(d.Scale[0]), uint8(d.ZeroPoint[0] + 128), nil
	}
	q := &quantModel{}
	var ws, bs [][]float64 // the convs' weights and biases, in float
	pooled := false
	for k, o := range g.Ops {
		switch o.Op {
		case tflite.OpConv2D, tflite.OpDepthwiseConv:
			if len(o.Inputs) < 3 || q.final != nil {
				return nil, fmt.Errorf("person detection: op %d is not what the model has there", k)
			}
			c, w, b, err := convOf(g, in, o, act)
			if err != nil {
				return nil, fmt.Errorf("person detection: op %d: %w", k, err)
			}
			ws, bs = append(ws, w), append(bs, b)
			if pooled {
				if c.OutW() != 1 || c.OutH() != 1 || c.OutC != 2 {
					return nil, errors.New("person detection: the last conv is not to two classes")
				}
				q.final, q.logitScale, q.logitZero = c, c.OutScale, float64(c.OutZero)
			} else {
				q.convs = append(q.convs, c)
			}
		case tflite.OpAveragePool2D:
			if y := g.Tensors[o.Outputs[0]]; len(q.convs) == 0 || len(y.Shape) != 4 || y.Shape[1] != 1 || y.Shape[2] != 1 {
				return nil, errors.New("person detection: not a global average pool")
			}
			xs, xz, err := act(o.Inputs[0])
			if err != nil {
				return nil, err
			}
			ys, yz, err := act(o.Outputs[0])
			if err != nil {
				return nil, err
			}
			if xs != ys || xz != yz {
				return nil, errors.New("person detection: the pool requantizes")
			}
			pooled = true
		case tflite.OpReshape, tflite.OpSoftmax:
			if !pooled {
				return nil, fmt.Errorf("person detection: a %s before the pool", o.Op)
			}
		default:
			return nil, fmt.Errorf("person detection: an operator %s the VPU path does not run", o.Op)
		}
	}
	if len(q.convs) == 0 || q.final == nil || q.final.InC != q.convs[len(q.convs)-1].OutC {
		return nil, errors.New("person detection: not the model expected")
	}
	all := append(append([]*vpu.Conv(nil), q.convs...), q.final)
	equalize(all, ws, bs, 2, 2)
	for i, c := range all {
		if err := quantizeWeights(c, ws[i], bs[i]); err != nil {
			return nil, fmt.Errorf("person detection: conv %d: %w", i, err)
		}
	}
	return q, nil
}

// convOf is one conv's shape and quantization in uint8, its weights and bias still in float: SAME
// padding and the stride worked out from the shapes (and checked against them), RELU6 where the
// output's range starts at zero.
func convOf(g *tflite.Subgraph, in *tflite.Interpreter, o *tflite.OpDesc,
	act func(int) (float64, uint8, error)) (c *vpu.Conv, wf, bf []float64, err error) {
	x, w, b, y := g.Tensors[o.Inputs[0]], in.Tensor(o.Inputs[1]), in.Tensor(o.Inputs[2]), g.Tensors[o.Outputs[0]]
	if len(x.Shape) != 4 || len(y.Shape) != 4 || len(w.Shape) != 4 || x.Shape[0] != 1 || w.F32 == nil || b == nil || b.F32 == nil {
		return nil, nil, nil, errors.New("not a conv of this model's kind")
	}
	c = &vpu.Conv{Depthwise: o.Op == tflite.OpDepthwiseConv, InH: x.Shape[1], InW: x.Shape[2], InC: x.Shape[3],
		OutC: y.Shape[3], KH: w.Shape[1], KW: w.Shape[2]}
	if c.InScale, c.InZero, err = act(o.Inputs[0]); err != nil {
		return nil, nil, nil, err
	}
	if c.OutScale, c.OutZero, err = act(o.Outputs[0]); err != nil {
		return nil, nil, nil, err
	}
	c.Stride = (c.InH + y.Shape[1] - 1) / y.Shape[1]
	padH := max((y.Shape[1]-1)*c.Stride+c.KH-c.InH, 0)
	padW := max((y.Shape[2]-1)*c.Stride+c.KW-c.InW, 0)
	c.PadT, c.PadB, c.PadL, c.PadR = padH/2, padH-padH/2, padW/2, padW-padW/2
	if c.OutH() != y.Shape[1] || c.OutW() != y.Shape[2] {
		return nil, nil, nil, errors.New("the padding is not SAME")
	}
	if c.OutZero == 0 {
		c.Act = 3 // RELU6: an int8 output from -128 is one that starts at zero
	}
	nw := c.OutC * c.KH * c.KW * c.InC
	if c.Depthwise {
		nw = c.KH * c.KW * c.OutC
		if c.InC != 1 && c.OutC != c.InC {
			return nil, nil, nil, errors.New("a depthwise conv with a multiplier apunn does not take")
		}
	}
	if len(w.F32) != nw || len(b.F32) != c.OutC {
		return nil, nil, nil, errors.New("weights or bias of the wrong size")
	}
	wf, bf = make([]float64, nw), make([]float64, c.OutC)
	for i, v := range w.F32 {
		wf[i] = float64(v)
	}
	for i, v := range b.F32 {
		bf[i] = float64(v)
	}
	return c, wf, bf, nil
}

// quantizeWeights quantizes a conv's weights again, per tensor (asymmetric, over their range), and
// its bias at the input's scale times theirs.
func quantizeWeights(c *vpu.Conv, wf, bf []float64) error {
	lo, hi := 0.0, 0.0
	for _, v := range wf {
		lo, hi = min(lo, v), max(hi, v)
	}
	if hi <= lo {
		return errors.New("weights all zero")
	}
	c.WScale = (hi - lo) / 255
	c.WZero = uint8(math.Round(-lo / c.WScale))
	c.Weights = make([]uint8, len(wf))
	for i, v := range wf {
		c.Weights[i] = uint8(min(max(math.Round(v/c.WScale)+float64(c.WZero), 0), 255))
	}
	c.Bias = make([]int32, c.OutC)
	for i, v := range bf {
		c.Bias[i] = int32(math.Round(v / (c.InScale * c.WScale)))
	}
	if c.Depthwise && c.InC == 1 {
		// apunn's depthwise kernel takes a multiplier of 1 only (the HAL's isSupported says so): with
		// one input channel the layer is a conv, its weights [KH][KW][OutC] as [OutC][KH][KW][1].
		t := make([]uint8, len(c.Weights))
		for k, v := range c.Weights {
			t[(k%c.OutC)*c.KH*c.KW+k/c.OutC] = v
		}
		c.Depthwise, c.Weights = false, t
	}
	return nil
}

// equalize is cross-layer equalization (Nagel et al. 2019, "Data-free quantization through weight
// equalization and bias correction") over the chain's first pairs (convs 0 and 1, 1 and 2, up to
// upTo), rounds times: an output channel of a conv scaled by 1/s, and the next conv's weights on that
// channel by s, leave the chain as it was (up to RELU6's clip and the activations' resolution) while
// bringing the two layers' channel ranges together, which one scale a tensor can then cover.
//
// It is there for the model's second layer, a depthwise conv whose 8 channels' weights reach 0.3 to
// 40.6: over one scale for the tensor most of its channels had 3 steps, and a picture of a person
// (the whole frame, as echod looks at it) fell from 0.79 to 0.24. Taken further down the chain, or
// for one pair alone, it shifts the scores of the camera's empty room one way or the other (§17).
func equalize(convs []*vpu.Conv, ws, bs [][]float64, upTo, rounds int) {
	for range rounds {
		for i := 0; i < upTo && i+1 < len(convs); i++ {
			p, n := convs[i], convs[i+1]
			if n.InC != p.OutC || n.KW*n.KH != 1 && !n.Depthwise {
				continue
			}
			for ch := 0; ch < p.OutC; ch++ {
				r1, r2 := 0.0, 0.0
				eachOut(p, ch, func(k int) { r1 = max(r1, math.Abs(ws[i][k])) })
				eachIn(n, ch, func(k int) { r2 = max(r2, math.Abs(ws[i+1][k])) })
				if r1 == 0 || r2 == 0 {
					continue
				}
				sc := math.Sqrt(r1 / r2)
				eachOut(p, ch, func(k int) { ws[i][k] /= sc })
				bs[i][ch] /= sc
				eachIn(n, ch, func(k int) { ws[i+1][k] *= sc })
			}
		}
	}
}

// eachOut calls f with the index of every weight of the conv's output channel ch.
func eachOut(c *vpu.Conv, ch int, f func(int)) {
	if c.Depthwise {
		for k := ch; k < c.KH*c.KW*c.OutC; k += c.OutC {
			f(k)
		}
		return
	}
	n := c.KH * c.KW * c.InC
	for k := ch * n; k < (ch+1)*n; k++ {
		f(k)
	}
}

// eachIn calls f with the index of every weight that reads the conv's input channel ch.
func eachIn(c *vpu.Conv, ch int, f func(int)) {
	if c.Depthwise {
		eachOut(c, ch, f)
		return
	}
	for k := ch; k < c.OutC*c.KH*c.KW*c.InC; k += c.InC {
		f(k)
	}
}

// tail is what follows the convolutions, on the CPU: the global average pool (TFLite's rounding),
// the 1x1 to two classes, and the softmax; the chance of a person.
func (q *quantModel) tail(last []uint8) float32 {
	c := q.convs[len(q.convs)-1]
	px := c.OutW() * c.OutH()
	pooled := make([]uint8, c.OutC)
	for ch := range pooled {
		s := 0
		for p := 0; p < px; p++ {
			s += int(last[p*c.OutC+ch])
		}
		pooled[ch] = uint8((s + px/2) / px)
	}
	l := q.final.Reference(pooled)
	a := (float64(l[0]) - q.logitZero) * q.logitScale
	b := (float64(l[1]) - q.logitZero) * q.logitScale
	return float32(1 / (1 + math.Exp(a-b)))
}

// cpu is the uint8 model on the CPU: what the VPU computes, byte for byte (§16.4), and slow. For
// tests.
func (q *quantModel) cpu(gray []uint8) []uint8 {
	x := gray
	for _, c := range q.convs {
		x = c.Reference(x)
	}
	return x
}
