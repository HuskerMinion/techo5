package model

// techo5: tests of the ADD, AVERAGE_POOL_2D and MAX_POOL_2D options and kernels (#190).

import (
	"testing"

	flatbuffers "github.com/google/flatbuffers/go"
)

func finishTable(b *flatbuffers.Builder) *flatbuffers.Table {
	b.Finish(b.EndObject())
	buf := b.FinishedBytes()
	return &flatbuffers.Table{Bytes: buf, Pos: flatbuffers.GetUOffsetT(buf)}
}

func TestParsePool2DOptions(t *testing.T) {
	for _, code := range []int{OpAveragePool2D, OpMaxPool2D} {
		b := flatbuffers.NewBuilder(0)
		b.StartObject(6)
		b.PrependByteSlot(0, 1, 0)
		b.PrependInt32Slot(1, 2, 0)
		b.PrependInt32Slot(2, 17, 0)
		b.PrependInt32Slot(3, 3, 0)
		b.PrependInt32Slot(4, 5, 0)
		b.PrependByteSlot(5, 1, 0)
		opts := map[string]interface{}{}
		parseBuiltinOptions(code, 1, finishTable(b), opts)
		want := map[string]int{
			"padding": 1, "stride_w": 2, "stride_h": 17,
			"filter_width": 3, "filter_height": 5, "fused_activation": 1,
		}
		for k, v := range want {
			if opts[k] != v {
				t.Errorf("%s: %s = %v, want %d", OpName(code), k, opts[k], v)
			}
		}
	}
}

func TestParseAddOptions(t *testing.T) {
	b := flatbuffers.NewBuilder(0)
	b.StartObject(2)
	b.PrependByteSlot(0, 1, 0)
	opts := map[string]interface{}{}
	parseBuiltinOptions(OpAdd, 1, finishTable(b), opts)
	if opts["fused_activation"] != 1 {
		t.Errorf("fused_activation = %v, want 1", opts["fused_activation"])
	}
}
