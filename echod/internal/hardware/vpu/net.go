//go:build linux

package vpu

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Net is a chain of convolutions apunn runs as one command, each conv's output the next one's input
// (research/vpu-vision-p6.md §16.4: the person detector's 27 in 2.55 ms). Its constants (the
// coefficients and biases, in each kernel's order) are written into the data area once, by Place;
// a run writes only the picture and reads only the last output.
type Net struct {
	ops     []Op
	tensors []Tensor
	consts  []placed
	inOff   uint32
	inLen   int
	outOff  uint32
	outLen  int
	size    uint32 // the data area it takes, from its start
}

type placed struct {
	off  uint32
	data []byte
}

// PrepareNet works out every conv's record for the runtime (Prepare, against the DSP's local memory
// as LocalMem reports it) and where every tensor goes in the data area, from ReservedData on.
func PrepareNet(convs []*Conv, lm LocalMem) (*Net, error) {
	if len(convs) == 0 || (inDataOff+len(convs)*recordSize) > ReservedData {
		return nil, errors.New("vpu: a net of the wrong size")
	}
	n := &Net{}
	at := uint32(ReservedData)
	place := func(b []byte, size int, write bool) int {
		if b != nil {
			n.consts = append(n.consts, placed{at, b})
		}
		n.tensors = append(n.tensors, Tensor{Offset: at, Size: uint32(size), Write: write})
		at = align(at+uint32(size), 64)
		return len(n.tensors) - 1
	}
	c0 := convs[0]
	x := place(nil, c0.InW*c0.InH*c0.InC, false)
	n.inOff, n.inLen = n.tensors[x].Offset, int(n.tensors[x].Size)
	for i, c := range convs {
		if i > 0 {
			p := convs[i-1]
			if c.InW != p.OutW() || c.InH != p.OutH() || c.InC != p.OutC || c.InScale != p.OutScale || c.InZero != p.OutZero {
				return nil, fmt.Errorf("vpu: conv %d does not take what conv %d gives", i, i-1)
			}
		}
		pr, err := c.Prepare(lm)
		if err != nil {
			return nil, fmt.Errorf("vpu: conv %d: %w", i, err)
		}
		coef := place(pr.Coef, len(pr.Coef), false)
		bias := place(pr.Bias, len(pr.Bias), false)
		y := place(nil, c.OutW()*c.OutH()*c.OutC, true)
		n.ops = append(n.ops, Op{Type: pr.Type, Params: pr.Params, In: []int{x, coef, bias}, Out: []int{y}})
		x = y
	}
	if dspBufOff+12*len(n.tensors) > inDataOff {
		return nil, errors.New("vpu: a net of too many tensors")
	}
	n.outOff, n.outLen, n.size = n.tensors[x].Offset, int(n.tensors[x].Size), at
	return n, nil
}

// Size is how much of the data area the net takes, from its start.
func (n *Net) Size() uint32 { return n.size }

// OutLen is the size of the last conv's output.
func (n *Net) OutLen() int { return n.outLen }

// Place writes the net's constants into the device's data area.
func (d *Device) Place(n *Net) error {
	data := d.Data()
	if int(n.size) > len(data) {
		return fmt.Errorf("vpu: the net needs %d bytes of the data area, it has %d", n.size, len(data))
	}
	if err := d.reserve(n.size, false); err != nil {
		return err
	}
	for _, c := range n.consts {
		copy(data[c.off:], c.data)
	}
	return nil
}

// RunNet runs a placed net on in (the first conv's input, NHWC uint8) and copies the last conv's
// output to out.
func (d *Device) RunNet(n *Net, in, out []byte, timeoutMs int) error {
	if len(in) != n.inLen || len(out) != n.outLen {
		return errors.New("vpu: an input or output of the wrong size")
	}
	data := d.Data()
	// Command clears its own area only: the constants stay where Place put them.
	copy(data[n.inOff:], in)
	status, err := d.Command(n.ops, n.tensors, 4, timeoutMs)
	if err != nil {
		return err
	}
	if s := binary.LittleEndian.Uint32(status); s != 0 {
		return fmt.Errorf("vpu: the net: status %#x", s)
	}
	copy(out, data[n.outOff:])
	return nil
}
