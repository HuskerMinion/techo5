package model

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testModelPath() string {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Dir(filepath.Dir(file))
	return filepath.Join(root, "testdata", "hey_jarvis.tflite")
}

func TestParseHeyJarvis(t *testing.T) {
	path := testModelPath()
	if _, err := os.Stat(path); err != nil {
		t.Skipf("Model not found: %s", path)
	}
	m, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if m.Version != 3 {
		t.Errorf("Version = %d, want 3", m.Version)
	}
	if len(m.Subgraphs) != 2 {
		t.Fatalf("Subgraphs = %d, want 2", len(m.Subgraphs))
	}

	sg := m.Subgraphs[0]
	t.Logf("Subgraph 0: %d tensors, %d ops", len(sg.Tensors), len(sg.Operators))

	if len(sg.Inputs) != 1 {
		t.Errorf("Inputs = %v, want 1", sg.Inputs)
	}
	if len(sg.Outputs) != 1 {
		t.Errorf("Outputs = %v, want 1", sg.Outputs)
	}

	inT := sg.Tensors[sg.Inputs[0]]
	if len(inT.Shape) != 3 || inT.Shape[0] != 1 || inT.Shape[1] != 3 || inT.Shape[2] != 40 {
		t.Errorf("Input shape = %v, want [1,3,40]", inT.Shape)
	}
	if inT.Type != TensorInt8 {
		t.Errorf("Input type = %d, want INT8", inT.Type)
	}

	outT := sg.Tensors[sg.Outputs[0]]
	if outT.Type != TensorUint8 {
		t.Errorf("Output type = %d, want UINT8", outT.Type)
	}

	opCounts := map[int]int{}
	for _, op := range sg.Operators {
		opCounts[op.OpCode]++
	}
	t.Logf("Op counts: %v", opCounts)

	// Verify all 12 expected op types are present.
	expectedOps := []int{
		OpCallOnce, OpVarHandle, OpReshape, OpReadVariable,
		OpConcatenation, OpStridedSlice, OpAssignVariable,
		OpConv2D, OpDepthwiseConv2D, OpFullyConnected,
		OpLogistic, OpQuantize,
	}
	for _, code := range expectedOps {
		if opCounts[code] == 0 {
			t.Errorf("Missing op %s (%d)", OpName(code), code)
		}
	}

	for _, op := range sg.Operators {
		if op.OpCode == OpConv2D {
			t.Logf("Conv2D: stride_h=%v pad=%v act=%v",
				op.Options["stride_h"], op.Options["padding"], op.Options["fused_activation"])
			break
		}
	}

	// Check variable tensors across both subgraphs.
	varCount := 0
	for si, sub := range m.Subgraphs {
		for _, ten := range sub.Tensors {
			if ten.IsVariable {
				varCount++
				t.Logf("SG%d var: %s shape=%v buf=%d", si, ten.Name, ten.Shape, ten.BufferIdx)
			}
		}
	}
	t.Logf("Total variable tensors: %d", varCount)

	// Dump init subgraph (subgraph 1) structure.
	if len(m.Subgraphs) > 1 {
		init := m.Subgraphs[1]
		t.Logf("Init subgraph: %d tensors, %d ops", len(init.Tensors), len(init.Operators))
		for j, ten := range init.Tensors {
			t.Logf("  init tensor[%d]: %q type=%d shape=%v buf=%d datalen=%d",
				j, ten.Name, ten.Type, ten.Shape, ten.BufferIdx, len(m.GetBufferData(ten.BufferIdx)))
		}
		for j, op := range init.Operators {
			t.Logf("  init op[%d]: %s inputs=%v outputs=%v",
				j, OpName(op.OpCode), op.Inputs, op.Outputs)
		}
	}

	// Dump main subgraph VarHandle/ReadVariable/AssignVariable ops
	for j, op := range sg.Operators {
		if op.OpCode == OpVarHandle {
			sharedName := ExtractFlexBufferString(op.CustomOptions, "shared_name")
			outName := sg.Tensors[op.Outputs[0]].Name
			t.Logf("  main VarHandle op[%d]: out=%q shared_name=%q customOpts=%d bytes",
				j, outName, sharedName, len(op.CustomOptions))
		}
	}

	// Also check init subgraph VarHandle shared_names
	if len(m.Subgraphs) > 1 {
		initSG := m.Subgraphs[1]
		for j, op := range initSG.Operators {
			if op.OpCode == OpVarHandle {
				sharedName := ExtractFlexBufferString(op.CustomOptions, "shared_name")
				outName := initSG.Tensors[op.Outputs[0]].Name
				t.Logf("  init VarHandle op[%d]: out=%q shared_name=%q customOpts=%d bytes",
					j, outName, sharedName, len(op.CustomOptions))
			}
		}
	}
}
