package addonapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// pngOf is a PNG of w by h pixels.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// meals is a page with one of every block, as the Meals add-on's cooking view might be.
func meals(t *testing.T) Block {
	half := 0.5
	return Column(
		Row(Chip("shopping", "Shopping", ""), Chip("plan", "Plan", "mdi:calendar"), Block{Type: BlockChip, ID: "cooking", Label: "Cooking", Selected: true}),
		Block{Type: BlockText, ID: "step", Text: "Step 2 of 5\nStir the onions", Size: SizeTitle, Bold: true, Align: "center", MaxLines: 3},
		Card("accent", Text("Next: add the rice"), Block{Type: BlockProgress, Value: &half}),
		Block{Type: BlockRow, Spacing: "large", Children: []Block{
			{Type: BlockImage, URL: "http://192.168.1.20:9000/api/media/recipes/1/images/min-original.webp", Fit: "cover", Weight: 2},
			{Type: BlockImage, Data: pngOf(t, 64, 48), Weight: 1},
			{Type: BlockSpacer, Weight: 1},
		}},
		Divider(),
		List(Section{Heading: "Ingredients", Items: []Block{Text("1 onion"), Row(Text("2 cups rice"), Button("rice_done", "", "mdi:check"))}},
			Section{Items: []Block{Text("Salt")}}),
		Row(Button("back", "Back", "mdi:arrow-left"), Spacer(), Button("next", "Next", "mdi:arrow-right")),
		Countdown("local:abc"),
	)
}

func refuses(t *testing.T, what, on string, b Block, code string) {
	t.Helper()
	err := ValidScene(on, b, func(id string) bool { return id == "local:abc" })
	if err == nil {
		t.Errorf("%s was taken", what)
		return
	}
	if CodeOf(err) != code {
		t.Errorf("%s: %s, not %s (%v)", what, CodeOf(err), code, err)
	}
}

func TestSceneValid(t *testing.T) {
	if err := ValidScene(OnPage, meals(t), func(id string) bool { return id == "local:abc" }); err != nil {
		t.Fatalf("a page of every block: %v", err)
	}
	card := Row(ImageData(pngOf(t, 32, 32)), Column(Block{Type: BlockText, Text: "Stir", Size: SizeTitle, MaxLines: 2}, Countdown("local:abc")), Chip("timer", "4:12", "mdi:timer"))
	if err := ValidScene(OnCard, card, nil); err != nil {
		t.Fatalf("a card: %v", err)
	}
	if err := ValidScene("clock", card, nil); CodeOf(err) != CodeBadRequest {
		t.Errorf("a scene for nowhere: %v", err)
	}
}

func TestSceneRefusals(t *testing.T) {
	one := 1.0
	for what, b := range map[string]Block{
		"no type":                  {Children: []Block{Text("x")}},
		"an unknown type":          {Type: "webview"},
		"a field not its type's":   {Type: BlockButton, ID: "a", Label: "A", Text: "words"},
		"an accent on a column":    {Type: BlockColumn, Accent: "accent"},
		"an accent not a role":     Card("green", Text("x")),
		"a button with no id":      Column(Block{Type: BlockButton, Label: "Go"}),
		"a chip with no id":        Column(Block{Type: BlockChip, Label: "Go"}),
		"a bad id":                 Column(Button("Next", "Next", "")),
		"an id with a dash":        Column(Button("go-on", "Next", "")),
		"a duplicate id":           Column(Button("next", "Next", ""), Block{Type: BlockText, ID: "next", Text: "x"}),
		"a weight outside a row":   Card("", Block{Type: BlockText, Text: "x", Weight: 1}),
		"a weight too great":       Row(Block{Type: BlockSpacer, Weight: MaxWeight + 1}),
		"a button with nothing":    Button("a", "", ""),
		"an icon not mdi":          Button("a", "", "fa:check"),
		"a control character":      Text("a\x07b"),
		"a bidi override":          Text("abc\u202edef"),
		"a bidi isolate":           Text("abc\u2066def"),
		"a C1 control":             Text("a\u0085b"),
		"DEL":                      Text("a\x7fb"),
		"a zero-width space":       Text("pay\u200bpal"),
		"a lone zero-width joiner": Text("\u200dab"),
		"a joiner by a space":      Text("a \u200cb"),
		"a word joiner":            Text("a\u2060b"),
		"an invisible operator":    Text("f\u2061x"),
		"an invisible plus":        Text("1\u20642"),
		"a soft hyphen":            Text("pay\u00adpal"),
		"a Mongolian separator":    Text("a\u180eb"),
		"a tag character":          Text("a\U000e0041b"),
		"a language tag":           Text("\U000e0001ab"),
		"a byte order mark":        Text("\ufeffhello"),
		"a line separator":         Text("one\u2028two"),
		"a paragraph separator":    Text("one\u2029two"),
		"a hidden label":           Button("a", "O\u200bK", ""),
		"a hidden heading":         List(Section{Heading: "A\u202eB", Items: []Block{Text("x")}}),
		"a stack of marks":         Text("e" + strings.Repeat("\u0301", MaxMarks+1)),
		"a break in a label":       Button("a", "two\nlines", ""),
		"a size not one":           {Type: BlockText, Text: "x", Size: "giant"},
		"too many lines":           {Type: BlockText, Text: "x", MaxLines: MaxLines + 1},
		"a progress past 1":        {Type: BlockProgress, Value: func() *float64 { v := 1.5; return &v }()},
		"a progress of both":       {Type: BlockProgress, Value: &one, Timer: "local:abc"},
		"a progress of neither":    {Type: BlockProgress},
		"another's timer":          Countdown("local:theirs"),
		"a list of nothing":        {Type: BlockList},
		"a list in a list":         List(Section{Items: []Block{List(Section{Items: []Block{Text("x")}})}}),
		"a list deep in a list":    List(Section{Items: []Block{Column(List(Section{Items: []Block{Text("x")}}))}}),
		"an image of both":         {Type: BlockImage, URL: "http://192.168.1.2/a.png", Data: []byte{1}},
		"an image of neither":      {Type: BlockImage},
		"an image by ftp":          Image("ftp://192.168.1.2/a.png"),
		"an image from outside":    Image("http://8.8.8.8/a.png"),
		"an image from loopback":   Image("http://127.0.0.1:8080/a.png"),
		"an image link-local":      Image("http://[fe80::1]/a.png"),
		"an image with a user":     Image("http://me:pw@192.168.1.2/a.png"),
		"an image not an image":    ImageData([]byte("GIF89a......")),
		"a fit not one":            {Type: BlockImage, URL: "http://ha.local/a.png", Fit: "stretch"},
		"an image put and by url":  {Type: BlockImage, URL: "http://192.168.1.2/a.png", Name: "photo"},
		"an image put, bad name":   ImageNamed("Photo 1"),
		"a name on a text":         {Type: BlockText, Text: "x", Name: "photo"},
	} {
		refuses(t, what, OnPage, b, CodeBadRequest)
	}
	for what, b := range map[string]Block{
		"words too long":      Text(strings.Repeat("a", MaxText+1)),
		"a label too long":    Button("a", strings.Repeat("a", MaxBlockLabel+1), ""),
		"an image too wide":   ImageData(pngOf(t, MaxImageLong+1, 10)),
		"an image too tall":   ImageData(pngOf(t, MaxImageShort+1, MaxImageShort+1)),
		"an image too heavy":  ImageData(append(pngOf(t, 8, 8), make([]byte, MaxImage)...)),
		"an address too long": Image("http://ha.local/" + strings.Repeat("a", MaxURL)),
	} {
		refuses(t, what, OnPage, b, CodeTooLarge)
	}
	// Either way up.
	if err := ValidScene(OnPage, ImageData(pngOf(t, MaxImageShort, MaxImageLong)), nil); err != nil {
		t.Errorf("a portrait image within the limits: %v", err)
	}
	// A name is checked when it is fetched, against what it resolves to.
	if err := ValidScene(OnPage, Image("https://homeassistant.local:8123/local/a.jpg"), nil); err != nil {
		t.Errorf("an image by name: %v", err)
	}
	if err := ValidScene(OnPage, Image("http://100.64.0.10/a.jpg"), nil); err != nil {
		t.Errorf("an image over Tailscale: %v", err)
	}
}

// An image put is shown by its name, on a page or a card; whether it is there is the daemon's to say
// when it draws, as an image put may come after the scene that shows it.
func TestImageByName(t *testing.T) {
	if err := ValidScene(OnPage, Column(ImageNamed("photo_1")), nil); err != nil {
		t.Errorf("an image put, on a page: %v", err)
	}
	if err := ValidScene(OnCard, Row(ImageNamed("photo_1"), Text("Soup")), nil); err != nil {
		t.Errorf("an image put, on a card: %v", err)
	}
	b := Column(ImageNamed("a"), List(Section{Items: []Block{ImageNamed("b"), ImageNamed("a")}}))
	if got := b.Images(); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("images shown: %v", got)
	}
}

// A list's item with an id is tapped as a whole, whatever its type; a block with an id outside a list
// is tapped only when it is a button or a chip.
func TestTapTargets(t *testing.T) {
	b := Column(
		Block{Type: BlockText, ID: "title", Text: "Recipes"},
		Button("add", "Add", "mdi:plus"),
		List(Section{Heading: "Soups", Items: []Block{
			{Type: BlockRow, ID: "soup", Children: []Block{{Type: BlockText, ID: "soup_name", Text: "Soup"}, Chip("soup_fav", "", "mdi:star")}},
			Text("no id"),
			{Type: BlockImage, ID: "bread", Name: "bread"},
		}}),
	)
	if err := ValidScene(OnPage, b, nil); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{
		"title": false, "add": true, "soup": true, "soup_name": false, "soup_fav": true, "bread": true, "": false, "none": false,
	} {
		if got := b.TapTarget(id); got != want {
			t.Errorf("TapTarget(%q) = %v, want %v", id, got, want)
		}
	}
}

// deep is a column that nests n blocks deep.
func deep(n int) Block {
	b := Text("bottom")
	for range n - 1 {
		b = Column(b)
	}
	return b
}

func TestSceneLimits(t *testing.T) {
	if err := ValidScene(OnPage, deep(MaxSceneDepth), nil); err != nil {
		t.Errorf("%d deep: %v", MaxSceneDepth, err)
	}
	refuses(t, "too deep", OnPage, deep(MaxSceneDepth+1), CodeTooLarge)
	// A list's item is two deeper than the list.
	inList := List(Section{Items: []Block{deep(MaxSceneDepth - 2)}})
	if err := ValidScene(OnPage, inList, nil); err != nil {
		t.Errorf("a list's item at the deepest: %v", err)
	}
	refuses(t, "too deep in a list", OnPage, List(Section{Items: []Block{deep(MaxSceneDepth - 1)}}), CodeTooLarge)

	many := Column()
	for range MaxBlocks - 1 {
		many.Children = append(many.Children, Divider())
	}
	if err := ValidScene(OnPage, many, nil); err != nil {
		t.Errorf("%d blocks: %v", MaxBlocks, err)
	}
	many.Children = append(many.Children, Divider())
	refuses(t, "too many blocks", OnPage, many, CodeTooLarge)

	big := Column()
	for range MaxScene/MaxText + 1 {
		big.Children = append(big.Children, Text(strings.Repeat("a", MaxText)))
	}
	refuses(t, "too big", OnPage, big, CodeTooLarge)

	images := Column()
	for range MaxImages + 1 {
		images.Children = append(images.Children, Image("http://ha.local/a.png"))
	}
	refuses(t, "too many images", OnPage, images, CodeTooLarge)
}

// The deepest scene there may be still reads within the message's nesting.
func TestSceneFitsTheNesting(t *testing.T) {
	inList := List(Section{Items: []Block{deep(MaxSceneDepth - 2)}})
	for _, b := range []Block{deep(MaxSceneDepth), inList} {
		if err := ValidScene(OnPage, b, nil); err != nil {
			t.Fatal(err)
		}
		msg, err := Marshal(Request{ID: 1, Op: OpSceneSet, Args: mustMarshal(t, SceneSet{On: OnPage, Scene: b})})
		if err != nil {
			t.Fatal(err)
		}
		var req Request
		if err := Unmarshal(msg, &req); err != nil {
			t.Fatalf("a scene at the deepest does not read: %v", err)
		}
		var set SceneSet
		if err := Unmarshal(req.Args, &set); err != nil {
			t.Fatalf("its arguments do not read: %v", err)
		}
	}
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCardRefusals(t *testing.T) {
	img := ImageData(pngOf(t, 16, 16))
	for what, b := range map[string]Block{
		"a button":   Row(Text("x"), Button("go", "Go", "")),
		"a list":     List(Section{Items: []Block{Text("x")}}),
		"a card":     Card("", Text("x")),
		"a divider":  Column(Text("x"), Divider()),
		"two images": Row(img, img),
		"huge words": {Type: BlockText, Text: "12:00", Size: SizeHuge},
		"many lines": {Type: BlockText, Text: "x", MaxLines: MaxCardLines + 1},
		"too deep":   Row(Column(Row(Text("x")))),
		"long words": Text(strings.Repeat("a", MaxCardText+1)),
		"many blocks": Row(Text("1"), Text("2"), Text("3"), Text("4"), Text("5"), Text("6"), Text("7"), Text("8"),
			Text("9"), Text("10"), Text("11"), Text("12")),
	} {
		err := ValidScene(OnCard, b, nil)
		if err == nil {
			t.Errorf("a card of %s was taken", what)
		}
		// Each would be fine on the page.
		if what != "two images" && what != "many lines" && what != "long words" && what != "huge words" {
			if err := ValidScene(OnPage, b, nil); err != nil && what != "too deep" {
				t.Errorf("%s on the page: %v", what, err)
			}
		}
	}
}

// Nothing in a scene can name a color, a position or a size in pixels, or hide the name bar: a page
// must never pass for one of the questions the device asks (Cast's Accept or Decline, the PIN pad, a
// call), whose colors and place are the daemon's. Block's fields are held to this list, so one cannot
// be added without this test, and its reason, being looked at.
func TestNothingImitatesTheSystem(t *testing.T) {
	want := []string{"type", "id", "weight", "children", "spacing", "sections", "accent", "text", "size", "bold",
		"align", "max_lines", "url", "data", "name", "fit", "label", "icon", "selected", "value", "timer",
		// A stepper's: numbers and words, drawn as the device's own − and +, with no color or place.
		"min", "max", "step", "unit",
		// A toggle's: on or off, drawn as the device's own switch.
		"on"}
	var got []string
	bt := reflect.TypeOf(Block{})
	for i := range bt.NumField() {
		name, _, _ := strings.Cut(bt.Field(i).Tag.Get("json"), ",")
		got = append(got, name)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Block's fields are %v; a new one must say why it cannot imitate the device's own screens, here", got)
	}
	// Every field is some block's, and only by blockFields.
	for _, f := range want[3:] {
		found := false
		for _, fs := range blockFields {
			found = found || slices.Contains(fs, f)
		}
		if !found {
			t.Errorf("%s is no block's", f)
		}
	}
	for _, a := range Accents {
		for _, bad := range []string{"green", "red", "accept", "decline", "answer", "#"} {
			if strings.Contains(a, bad) {
				t.Errorf("the accent %q could make an accept or decline pair", a)
			}
		}
	}
	// A button, which is what a question's answer would be, has no color at all.
	refuses(t, "a button with an accent", OnPage, Block{Type: BlockButton, ID: "yes", Label: "Accept", Accent: "accent"}, CodeBadRequest)
	// And what an author might try is not read at all.
	for _, msg := range []string{
		`{"on":"page","scene":{"type":"button","id":"yes","label":"Accept","color":"#2e7d32"}}`,
		`{"on":"page","scene":{"type":"column","background":"#c62828"}}`,
		`{"on":"page","hide_name_bar":true,"scene":{"type":"column"}}`,
		`{"on":"page","fullscreen":true,"scene":{"type":"column"}}`,
		`{"on":"page","scene":{"type":"column","x":0,"y":0,"width":960,"height":480}}`,
		`{"on":"system","scene":{"type":"column"}}`,
	} {
		var s SceneSet
		err := Unmarshal([]byte(msg), &s)
		if err == nil {
			err = ValidScene(s.On, s.Scene, nil)
		}
		if err == nil {
			t.Errorf("%s was taken", msg)
		}
	}
}

// Words are written as people write them, accents and all, but nothing in them hides or reorders what is
// seen: a few combining marks on a character are taken, and a line break in a text.
func TestWordsAsWritten(t *testing.T) {
	for what, text := range map[string]string{
		"accents":                "Crème brûlée, jalapeño",
		"marks within the limit": "e" + strings.Repeat("\u0301", MaxMarks),
		"Devanagari":             "\u0928\u092e\u0938\u094d\u0924\u0947",
		"Arabic":                 "\u0645\u0631\u062d\u0628\u0627",
		"a line break":           "Step 2\nStir",
		"Persian with a ZWNJ":    "\u0645\u06cc\u200c\u062e\u0648\u0627\u0647\u0645",
		"an emoji sequence":      "\U0001f469\u200d\U0001f4bb and \u2764\ufe0f\u200d\U0001f525",
	} {
		if err := ValidScene(OnPage, Text(text), nil); err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}
	if got := StripHidden("Me\u202eals\u200b\x07 "); got != "Meals " {
		t.Errorf("StripHidden left %q", got)
	}
	if StripHidden("Meals") != "Meals" {
		t.Error("StripHidden changed plain words")
	}
}

// Ten buttons or chips of a digit each, one after another, are a PIN pad, which is the device's own: a
// scene with them is refused, page or card, in rows or not; fewer, or with other keys among them, are
// taken.
func TestNoPINPad(t *testing.T) {
	key := func(d string) Block { return Button("k"+d, d, "") }
	var keys []Block
	for _, d := range []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "0"} {
		keys = append(keys, key(d))
	}
	pad := Column(Row(keys[0:3]...), Row(keys[3:6]...), Row(keys[6:9]...), Row(Spacer(), keys[9], Spacer()))
	refuses(t, "a PIN pad", OnPage, pad, CodeBadRequest)
	refuses(t, "a PIN pad in a row", OnPage, Row(keys...), CodeBadRequest)
	chips := make([]Block, 0, 10)
	for i := range 10 {
		chips = append(chips, Chip(fmt.Sprintf("c%d", i), fmt.Sprintf("%d", i), ""))
	}
	refuses(t, "a PIN pad of chips", OnPage, Column(Row(chips[:5]...), Row(chips[5:]...)), CodeBadRequest)
	refuses(t, "a PIN pad of other digits", OnPage, Row(func() []Block {
		var out []Block
		for i, d := range []rune("\u0661\u0662\u0663\u0664\u0665\u0666\u0667\u0668\u0669\u0660") {
			out = append(out, Button(fmt.Sprintf("a%d", i), string(d), ""))
		}
		return out
	}()...), CodeBadRequest)
	if err := ValidScene(OnPage, Row(keys[:MaxDigitRun-1]...), nil); err != nil {
		t.Errorf("nine digit keys: %v", err)
	}
	// The device's own pad, back and OK among its digits; with words among them; spread over the page.
	dev := Column(Row(keys[0:3]...), Row(keys[3:6]...), Row(keys[6:9]...), Row(Button("back", "", "mdi:backspace"), keys[9], Button("ok", "OK", "")))
	refuses(t, "the device's own PIN pad", OnPage, dev, CodeBadRequest)
	broken := append(append(append([]Block{}, keys[:5]...), Text("and")), keys[5:]...)
	refuses(t, "digit keys with words among them", OnPage, Row(broken...), CodeBadRequest)
	refuses(t, "digit keys spread over a page", OnPage, Column(Row(keys[:5]...), Text("Pick a number"), List(Section{Heading: "More", Items: []Block{Row(keys[5:]...)}})), CodeBadRequest)
	var caps []Block
	for i := range 10 {
		caps = append(caps, Button(fmt.Sprintf("cap%d", i), fmt.Sprintf("%d\ufe0f\u20e3", i), ""))
	}
	refuses(t, "a pad of keycaps", OnPage, Row(caps...), CodeBadRequest)
	if err := ValidScene(OnPage, Row(Button("ten", "10", ""), key("1"), key("2")), nil); err != nil {
		t.Errorf("a few numbered buttons: %v", err)
	}
}

// A stepper is a number in a range, in steps: each is finite, of at most two decimal places, the value
// within the range, the step above nothing, and at most MaxStepperSteps steps across it. It needs an id,
// and it is no digit key.
func TestStepper(t *testing.T) {
	ok := Stepper("servings", "Servings", 4, 1, 12, 1, "people")
	if err := ValidScene(OnPage, Column(ok), nil); err != nil {
		t.Fatalf("a stepper: %v", err)
	}
	if err := ValidScene(OnCard, Row(ok), nil); err != nil {
		t.Errorf("a stepper on a card: %v", err)
	}
	if err := ValidScene(OnPage, Block{Type: BlockStepper, ID: "s", Value: ptr(0.5), Min: ptr(0), Max: ptr(2)}, nil); err != nil {
		t.Errorf("a stepper with no step, label or unit: %v", err)
	}
	with := func(f func(*Block)) Block { b := Stepper("s", "Servings", 4, 1, 12, 1, ""); f(&b); return b }
	for what, b := range map[string]Block{
		"no id":           with(func(b *Block) { b.ID = "" }),
		"no value":        with(func(b *Block) { b.Value = nil }),
		"no min":          with(func(b *Block) { b.Min = nil }),
		"no max":          with(func(b *Block) { b.Max = nil }),
		"min above max":   with(func(b *Block) { b.Min, b.Max = ptr(12), ptr(1) }),
		"min at max":      with(func(b *Block) { b.Min, b.Max, b.Value = ptr(3), ptr(3), ptr(3) }),
		"value below":     with(func(b *Block) { b.Value = ptr(0) }),
		"value above":     with(func(b *Block) { b.Value = ptr(13) }),
		"a step of 0":     with(func(b *Block) { b.Step = ptr(0) }),
		"a step below 0":  with(func(b *Block) { b.Step = ptr(-1) }),
		"NaN":             with(func(b *Block) { b.Value = ptr(math.NaN()) }),
		"infinity":        with(func(b *Block) { b.Max = ptr(math.Inf(1)) }),
		"huge":            with(func(b *Block) { b.Max = ptr(1e12) }),
		"three decimals":  with(func(b *Block) { b.Step = ptr(0.125) }),
		"a hidden label":  with(func(b *Block) { b.Label = "Serv\u202eings" }),
		"a unit too long": with(func(b *Block) { b.Unit = strings.Repeat("u", MaxUnit+1) }),
		"a field not its": with(func(b *Block) { b.Icon = "mdi:plus" }),
		"a color, say":    with(func(b *Block) { b.Accent = "accent" }),
	} {
		code := CodeBadRequest
		if what == "a unit too long" {
			code = CodeTooLarge
		}
		refuses(t, "a stepper with "+what, OnPage, b, code)
	}
	refuses(t, "a stepper of too many steps", OnPage, with(func(b *Block) { b.Max, b.Step = ptr(1002), ptr(1) }), CodeTooLarge)
	if err := ValidScene(OnPage, with(func(b *Block) { b.Min, b.Max, b.Step, b.Value = ptr(0), ptr(10), ptr(0.01), ptr(0.25) }), nil); err != nil {
		t.Errorf("a stepper of exactly 1000 steps: %v", err)
	}
	// Steppers labeled with a digit are not digit keys: ten of them are not a PIN pad.
	var many []Block
	for i := range 10 {
		many = append(many, Stepper(fmt.Sprintf("s%d", i), fmt.Sprintf("%d", i), 1, 0, 9, 1, ""))
	}
	if err := ValidScene(OnPage, Column(many...), nil); err != nil {
		t.Errorf("ten steppers: %v", err)
	}
	// A touch is not a stepper's: it sends change.
	if col := Column(ok); col.TapTarget("servings") {
		t.Error("a stepper is a touch target")
	}
}

// A tap moves a stepper by its step, held to its range and its decimal places.
func TestStepped(t *testing.T) {
	b := Stepper("s", "", 4, 1, 5, 1, "")
	if b.Stepped(1) != 5 || b.Stepped(-1) != 3 {
		t.Errorf("4 in steps of 1: %v %v", b.Stepped(1), b.Stepped(-1))
	}
	top := Stepper("s", "", 5, 1, 5, 1, "")
	bottom := Stepper("s", "", 1, 1, 5, 1, "")
	if top.Stepped(1) != 5 || bottom.Stepped(-1) != 1 {
		t.Error("a stepper went past its range")
	}
	tenths := Stepper("s", "", 0.3, 0, 1, 0.1, "")
	if got := tenths.Stepped(1); got != 0.4 {
		t.Errorf("0.3 + 0.1 is %v", got)
	}
	odd := Stepper("s", "", 9, 0, 10, 3, "")
	if odd.Stepped(1) != 10 {
		t.Errorf("a step past the max is held to it: %v", odd.Stepped(1))
	}
}

func ptr(x float64) *float64 { return &x }

// A toggle has an id and a label, and is on or off; it is no digit key. A chip may be selected, which is
// the add-on's to set: it is drawn as the theme's picked chip, and a tap on it is still a touch.
func TestToggleAndSelected(t *testing.T) {
	if err := ValidScene(OnPage, Column(Toggle("wifi", "Keep warm", true), Toggle("timer", "Timer sound", false)), nil); err != nil {
		t.Fatalf("toggles: %v", err)
	}
	if err := ValidScene(OnCard, Row(Toggle("warm", "Warm", true)), nil); err != nil {
		t.Errorf("a toggle on a card: %v", err)
	}
	refuses(t, "a toggle with no id", OnPage, Block{Type: BlockToggle, Label: "Warm"}, CodeBadRequest)
	refuses(t, "a toggle with no label", OnPage, Block{Type: BlockToggle, ID: "w"}, CodeBadRequest)
	refuses(t, "a toggle with a hidden label", OnPage, Toggle("w", "W\u202earm", false), CodeBadRequest)
	refuses(t, "a toggle with an icon", OnPage, Block{Type: BlockToggle, ID: "w", Label: "Warm", Icon: "mdi:fire"}, CodeBadRequest)
	var ten []Block
	for i := range 10 {
		ten = append(ten, Toggle(fmt.Sprintf("t%d", i), fmt.Sprintf("%d", i), false))
	}
	if err := ValidScene(OnPage, Column(ten...), nil); err != nil {
		t.Errorf("ten toggles labeled with digits are not a PIN pad: %v", err)
	}
	tabs := Row(Block{Type: BlockChip, ID: "a", Label: "Shopping", Selected: true}, Chip("b", "Plan", ""))
	if err := ValidScene(OnPage, tabs, nil); err != nil {
		t.Errorf("a selected chip: %v", err)
	}
	if !tabs.TapTarget("a") || !tabs.TapTarget("b") {
		t.Error("a chip, selected or not, is a touch target")
	}
	if col := Column(Toggle("w", "Warm", true)); col.TapTarget("w") {
		t.Error("a toggle is a touch target; it sends change")
	}
	refuses(t, "a selected button", OnPage, Block{Type: BlockButton, ID: "b", Label: "OK", Selected: true}, CodeBadRequest)
}

// A row that is a list's item may have a picture (an image put, or an address, by the image's rules) or
// an icon at its start; a row anywhere else may not, and not both.
func TestListRowPictures(t *testing.T) {
	item := func(f func(*Block)) Block {
		b := Block{Type: BlockRow, ID: "soup", Children: []Block{Text("Soup")}}
		f(&b)
		return List(Section{Items: []Block{b}})
	}
	for what, b := range map[string]Block{
		"an image put":      item(func(b *Block) { b.Name = "soup_photo" }),
		"an image by url":   item(func(b *Block) { b.URL = "http://192.168.1.20/soup.jpg" }),
		"an icon":           item(func(b *Block) { b.Icon = "mdi:pot-steam" }),
		"an unknown icon":   item(func(b *Block) { b.Icon = "mdi:no-such-icon-at-all" }),
		"a plain list item": item(func(b *Block) {}),
	} {
		if err := ValidScene(OnPage, b, nil); err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}
	for what, b := range map[string]Block{
		"a picture and an icon": item(func(b *Block) { b.Name, b.Icon = "soup_photo", "mdi:pot-steam" }),
		"an image from outside": item(func(b *Block) { b.URL = "http://8.8.8.8/soup.jpg" }),
		"a bad name":            item(func(b *Block) { b.Name = "Soup Photo" }),
		"an icon not mdi":       item(func(b *Block) { b.Icon = "fa:soup" }),
		"a picture on a row":    Column(Block{Type: BlockRow, Name: "soup_photo", Children: []Block{Text("x")}}),
		"an icon on a row":      Column(Block{Type: BlockRow, Icon: "mdi:lock", Children: []Block{Text("x")}}),
		"a picture on a column": List(Section{Items: []Block{{Type: BlockColumn, Name: "x", Children: []Block{Text("x")}}}}),
	} {
		refuses(t, what, OnPage, b, CodeBadRequest)
	}
	sc := item(func(b *Block) { b.URL = "http://192.168.1.20/soup.jpg" })
	if got := sc.URLs(); len(got) != 1 {
		t.Errorf("a row's picture is not among the scene's addresses: %v", got)
	}
	row := sc.Sections[0].Items[0]
	if th, ok := row.Thumb(); !ok || th.Type != BlockImage || th.URL == "" || th.Fit != "cover" {
		t.Errorf("Thumb %+v %v", th, ok)
	}
	// The row's pictures count among the page's images.
	var items []Block
	for i := range MaxImages + 1 {
		items = append(items, Block{Type: BlockRow, ID: fmt.Sprintf("r%d", i), Name: "p", Children: []Block{Text("x")}})
	}
	refuses(t, "more rows with pictures than a page's images", OnPage, List(Section{Items: items}), CodeTooLarge)
}

func TestURLs(t *testing.T) {
	sc := Column(Image("http://192.168.1.2/a.png"), List(Section{Items: []Block{Image("http://192.168.1.2/b.png"), Image("http://192.168.1.2/a.png")}}), ImageNamed("x"))
	if got := sc.URLs(); !slices.Equal(got, []string{"http://192.168.1.2/a.png", "http://192.168.1.2/b.png"}) {
		t.Errorf("URLs %v", got)
	}
}

func TestPatched(t *testing.T) {
	page := meals(t)
	out, ok, err := page.Patched("step", Text("Step 3 of 5"))
	if err != nil || !ok {
		t.Fatalf("patch: %v %v", ok, err)
	}
	if b, _ := out.Find("step"); b.Text != "Step 3 of 5" || b.ID != "step" {
		t.Errorf("the patched block is %+v", b)
	}
	// The scene it was made from is untouched, so the one being drawn never changes under the display.
	if b, _ := page.Find("step"); !strings.HasPrefix(b.Text, "Step 2") {
		t.Errorf("the original changed: %q", b.Text)
	}
	if _, ok, _ := page.Patched("nothing", Text("x")); ok {
		t.Error("a patch of no block was found")
	}
	if _, _, err := page.Patched("step", Block{Type: BlockText, ID: "other", Text: "x"}); CodeOf(err) != CodeBadRequest {
		t.Errorf("a patch that changes the id: %v", err)
	}
	// Inside a list, too.
	out, ok, _ = page.Patched("rice_done", Button("rice_done", "", "mdi:check-bold"))
	if b, _ := out.Find("rice_done"); !ok || b.Icon != "mdi:check-bold" {
		t.Error("a patch inside a list")
	}
	if !out.TapTarget("rice_done") || out.TapTarget("step") || out.TapTarget("nothing") {
		t.Error("TapTarget")
	}
	if got := page.Timers(); !slices.Equal(got, []string{"local:abc"}) {
		t.Errorf("timers %v", got)
	}
}
