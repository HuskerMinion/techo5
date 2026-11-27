package addonapi

import (
	"bytes"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
)

func TestFrames(t *testing.T) {
	var b bytes.Buffer
	if err := WriteFrame(&b, []byte(`{"id":1}`)); err != nil {
		t.Fatal(err)
	}
	if got := binary.BigEndian.Uint32(b.Bytes()); got != 8 {
		t.Fatalf("length prefix %d", got)
	}
	msg, n, err := ReadFrame(&b, MaxMessage)
	if err != nil || n != 8 || string(msg) != `{"id":1}` {
		t.Fatalf("read back %q %d %v", msg, n, err)
	}
	if err := WriteFrame(&b, make([]byte, MaxMessage+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("wrote a message past the limit: %v", err)
	}
	b.Reset()
	_ = binary.Write(&b, binary.BigEndian, uint32(MaxMessage+1))
	if _, n, err := ReadFrame(&b, MaxMessage); !errors.Is(err, ErrTooLarge) || n != MaxMessage+1 {
		t.Errorf("read a message past the limit: %d %v", n, err)
	}
}

// A message is read one way: unknown keys, keys given twice or in another case, and anything after
// the value refuse it.
func TestStrict(t *testing.T) {
	var r Request
	if err := Unmarshal([]byte(`{"id":7,"op":"speak","args":{"text":"hi"}}`), &r); err != nil || r.ID != 7 || r.Op != "speak" {
		t.Fatalf("a good request: %+v %v", r, err)
	}
	for name, msg := range map[string]string{
		"unknown key":   `{"id":1,"op":"hello","extra":1}`,
		"twice":         `{"id":1,"op":"hello","op":"speak"}`,
		"capital":       `{"id":1,"Op":"hello"}`,
		"long s":        `{"id":1,"ſop":"hello"}`,
		"trailing":      `{"id":1,"op":"hello"} {}`,
		"twice nested":  `{"id":1,"op":"x","args":{"a":1,"a":2}}`,
		"not an object": `[1]`,
	} {
		if err := Unmarshal([]byte(msg), &r); err == nil {
			t.Errorf("%s was read", name)
		}
	}
	var v any
	deep := strings.Repeat("[", MaxNesting+1) + strings.Repeat("]", MaxNesting+1)
	if err := Unmarshal([]byte(deep), &v); err == nil {
		t.Errorf("nesting %d deep was read", MaxNesting+1)
	}
	deep = strings.Repeat("[", MaxNesting) + strings.Repeat("]", MaxNesting)
	if err := Unmarshal([]byte(deep), &v); err != nil {
		t.Errorf("nesting %d deep, the most, was refused: %v", MaxNesting, err)
	}
}

func TestCommands(t *testing.T) {
	cmds := []Command{
		{Name: "step", Args: map[string]Arg{"to": {Type: TypeString, Max: 16}, "n": {Type: TypeNumber, Required: true}}},
		{Name: "start_cooking"},
	}
	if err := ValidCommands(cmds); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string][]Command{
		"bad name":  {{Name: "Step"}},
		"twice":     {{Name: "a"}, {Name: "a"}},
		"bad type":  {{Name: "a", Args: map[string]Arg{"x": {Type: "list"}}}},
		"bool max":  {{Name: "a", Args: map[string]Arg{"x": {Type: TypeBool, Max: 3}}}},
		"arg name":  {{Name: "a", Args: map[string]Arg{"X": {Type: TypeBool}}}},
		"too long":  {{Name: "a", Args: map[string]Arg{"x": {Type: TypeString, Max: MaxStringLength + 1}}}},
		"many args": {{Name: "a", Args: manyArgs(MaxArgs + 1)}},
	} {
		if err := ValidCommands(c); err == nil {
			t.Errorf("%s passed", name)
		}
	}
	step := cmds[0]
	if err := step.CheckArgs(map[string]any{"to": "3", "n": 2.0}); err != nil {
		t.Error(err)
	}
	for name, a := range map[string]map[string]any{
		"missing":   {"to": "3"},
		"unknown":   {"n": 1.0, "x": 1.0},
		"long":      {"n": 1.0, "to": strings.Repeat("x", 17)},
		"wrong":     {"n": "one"},
		"str a num": {"n": 1.0, "to": 3.0},
	} {
		if err := step.CheckArgs(a); err == nil {
			t.Errorf("%s args passed", name)
		}
	}
	// What Home Assistant renders as a number or text goes back to the declared type before the check.
	got := step.CoerceArgs(map[string]any{"to": 3.0, "n": "2"})
	if got["to"] != "3" || got["n"] != 2.0 {
		t.Errorf("coerced %v", got)
	}
	if err := step.CheckArgs(got); err != nil {
		t.Errorf("coerced args refused: %v", err)
	}
	if got := step.CoerceArgs(map[string]any{"to": true, "n": "two"}); got["to"] != "true" || got["n"] != "two" {
		t.Errorf("coerced %v", got)
	}
	if step.NeedsNothing() || !cmds[1].NeedsNothing() {
		t.Error("NeedsNothing")
	}
}

func manyArgs(n int) map[string]Arg {
	m := map[string]Arg{}
	for i := range n {
		m[string(rune('a'+i))] = Arg{Type: TypeBool}
	}
	return m
}

func TestSettings(t *testing.T) {
	s := Settings{Test: true, Fields: []Setting{
		{Key: "address", Label: "Mealie address", Type: TypeString, Max: 200},
		{Key: "token", Label: "API token", Type: TypeSecret},
		{Key: "loud", Label: "Read steps aloud", Type: TypeBool},
		{Key: "size", Label: "Servings", Type: TypeNumber, Min: 1, Max: 12, Step: 1},
		{Key: "unit", Label: "Units", Type: TypeSelect, Options: []string{"metric", "US"}},
		{Key: "light", Label: "Kitchen light", Type: TypeEntity, Domains: []string{"light"}},
	}}
	if err := s.Valid(); err != nil {
		t.Fatal(err)
	}
	f, _ := s.Field("size")
	if f.Check(3.0) != nil || f.Check(13.0) == nil || f.Check("3") == nil {
		t.Error("a number setting's bounds")
	}
	f, _ = s.Field("unit")
	if f.Check("US") != nil || f.Check("imperial") == nil {
		t.Error("a select's options")
	}
	f, _ = s.Field("light")
	if f.Check("light.kitchen") != nil || f.Check("switch.kitchen") == nil || f.Check("") != nil {
		t.Error("an entity setting's domains")
	}
	f, _ = s.Field("address")
	if f.Check(strings.Repeat("x", 201)) == nil {
		t.Error("a string's longest length")
	}
	for name, bad := range map[string]Settings{
		"none":       {},
		"twice":      {Fields: []Setting{{Key: "a", Label: "A", Type: TypeBool}, {Key: "a", Label: "A", Type: TypeBool}}},
		"no label":   {Fields: []Setting{{Key: "a", Type: TypeBool}}},
		"no options": {Fields: []Setting{{Key: "a", Label: "A", Type: TypeSelect}}},
		"no domains": {Fields: []Setting{{Key: "a", Label: "A", Type: TypeEntity}}},
		"bad number": {Fields: []Setting{{Key: "a", Label: "A", Type: TypeNumber, Min: 3, Max: 1}}},
		"type":       {Fields: []Setting{{Key: "a", Label: "A", Type: "color"}}},
		"stray opts": {Fields: []Setting{{Key: "a", Label: "A", Type: TypeBool, Options: []string{"x"}}}},
	} {
		if err := bad.Valid(); err == nil {
			t.Errorf("%s passed", name)
		}
	}
}

func TestEntities(t *testing.T) {
	ents := []Entity{
		{Domain: DomainSensor, ID: "step", Name: "Step", Text: true},
		{Domain: DomainSwitch, ID: "cooking", Name: "Cooking", Icon: "mdi:chef-hat"},
		{Domain: DomainSelect, ID: "recipe", Name: "Recipe", Options: []string{"a", "b"}},
		{Domain: DomainNumber, ID: "servings", Name: "Servings", Min: 1, Max: 12, Step: 1},
		{Domain: DomainButton, ID: "next", Name: "Next"},
	}
	if err := ValidEntities(ents); err != nil {
		t.Fatal(err)
	}
	if got := EntityRefs(ents); strings.Join(got, ",") != "button.next,number.servings,select.recipe,sensor.step,switch.cooking" {
		t.Errorf("refs %v", got)
	}
	if ents[0].CheckState("Chop onions") != nil || ents[0].CheckState(3.0) == nil {
		t.Error("a text sensor")
	}
	if ents[1].CheckState(true) != nil || ents[1].CheckState("on") == nil {
		t.Error("a switch")
	}
	if ents[2].CheckState("b") != nil || ents[2].CheckState("c") == nil {
		t.Error("a select")
	}
	if ents[3].CheckState(12.0) != nil || ents[3].CheckState(13.0) == nil {
		t.Error("a number")
	}
	if ents[4].CheckState(nil) == nil {
		t.Error("a button took a state")
	}
	many := make([]Entity, MaxEntities+1)
	for i := range many {
		many[i] = Entity{Domain: DomainButton, ID: "b" + string(rune('a'+i)), Name: "B"}
	}
	for name, bad := range map[string][]Entity{
		"domain": {{Domain: "light", ID: "a", Name: "A"}},
		"twice":  {{Domain: DomainButton, ID: "a", Name: "A"}, {Domain: DomainSwitch, ID: "a", Name: "A"}},
		"icon":   {{Domain: DomainButton, ID: "a", Name: "A", Icon: "fa:x"}},
		"many":   many,
		"text":   {{Domain: DomainSwitch, ID: "a", Name: "A", Text: true}},
		"number": {{Domain: DomainNumber, ID: "a", Name: "A"}},
	} {
		if err := ValidEntities(bad); err == nil {
			t.Errorf("%s passed", name)
		}
	}
}

func wav(channels, bits, rate, samples int) []byte {
	var b bytes.Buffer
	data := samples * channels * bits / 8
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+data))
	b.WriteString("WAVEfmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(rate*channels*bits/8))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels*bits/8))
	_ = binary.Write(&b, binary.LittleEndian, uint16(bits))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(data))
	b.Write(make([]byte, data))
	return b.Bytes()
}

func TestWAV(t *testing.T) {
	w, err := ParseWAV(wav(1, 16, 16000, 16000))
	if err != nil || w.Rate != 16000 || len(w.Samples) != 16000 {
		t.Fatalf("a second of mono: %+v %v", w.Rate, err)
	}
	for name, b := range map[string][]byte{
		"stereo":    wav(2, 16, 16000, 100),
		"8-bit":     wav(1, 8, 16000, 100),
		"too long":  wav(1, 16, 8000, 8000*6),
		"too large": wav(1, 16, 48000, 48000*5+100),
		"not wave":  []byte("RIFF....AVI "),
		"empty":     wav(1, 16, 16000, 0),
	} {
		if _, err := ParseWAV(b); err == nil {
			t.Errorf("%s was taken", name)
		}
	}
}
