package addonapi

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // DecodeConfig reads a JPEG's size
	_ "image/png"  // and a PNG's
	"math"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// A scene is what an add-on with the screen capability shows: a tree of blocks, which the daemon lays
// out and draws in its own theme, fonts and colors, at the size of the device's screen. An add-on sends
// no pixels but its images, and nothing in a block names a color, a position or a size in pixels: the
// theme's palette is offered by name, and the name bar over the page is the daemon's and always there.
// That is what keeps a page from passing for one of the questions the device itself asks (Cast's
// Accept or Decline, the PIN pad, the call screen): there is no accept or decline color to give a block,
// no way to cover the name bar, and the daemon reserves those screens.
//
// Both ends check a scene with ValidScene, so an author's client refuses what the device would.

// Where a scene is shown.
const (
	// OnPage is the add-on's own page, a tile under the Add-ons tab, full screen under the name bar.
	OnPage = "page"
	// OnCard is the add-on's small card over the clock: at most a third of the screen, of a few blocks.
	OnCard = "card"
)

// Block types.
const (
	BlockColumn   = "column"
	BlockRow      = "row"
	BlockList     = "list"
	BlockCard     = "card"
	BlockText     = "text"
	BlockImage    = "image"
	BlockButton   = "button"
	BlockChip     = "chip"
	BlockProgress = "progress"
	BlockDivider  = "divider"
	BlockSpacer   = "spacer"
	BlockStepper  = "stepper"
	BlockToggle   = "toggle"
)

// BlockTypes is every block a scene may hold.
var BlockTypes = []string{BlockColumn, BlockRow, BlockList, BlockCard, BlockText, BlockImage, BlockButton,
	BlockChip, BlockProgress, BlockDivider, BlockSpacer, BlockStepper, BlockToggle}

// CardBlocks are the blocks a card over the clock may hold: words, a chip, a progress and one image,
// arranged in rows and columns. Nothing in it scrolls or is a button: the card is a glance at the clock,
// and the add-on's page is where it is used.
var CardBlocks = []string{BlockColumn, BlockRow, BlockText, BlockChip, BlockProgress, BlockImage, BlockSpacer, BlockStepper, BlockToggle}

// Text sizes, from the theme's fonts.
const (
	SizeSmall = "small"
	SizeBody  = "body"
	SizeTitle = "title"
	SizeHuge  = "huge"
)

// TextSizes are the sizes a text block may be; body when it names none.
var TextSizes = []string{SizeSmall, SizeBody, SizeTitle, SizeHuge}

// Spacings are the room a column, a row or a card leaves between its children; normal when it names
// none.
var Spacings = []string{"none", "small", "normal", "large"}

// Aligns are where a text block's words sit across its room; start when it names none.
var Aligns = []string{"start", "center", "end"}

// Fits are how an image fills its room: contain shows all of it, cover fills the room and crops it.
var Fits = []string{"contain", "cover"}

// Accents are the theme's colors a card may be marked with, by the role they have in the theme: its
// accent, its dim and its rules. They follow the theme the owner picked, and dim at night with it. The
// green and red of the device's own Answer and Decline are not among them, nor any color by value, so
// no two blocks of a scene can be an accept and a decline pair.
var Accents = []string{"accent", "dim", "rules"}

// Swipe directions.
const (
	SwipeLeft  = "left"
	SwipeRight = "right"
)

// Limits of a scene. Past one, scene.set and scene.patch are answered too_large and nothing changes.
const (
	// MaxScene is the most a scene may come to, as JSON, after a patch as well as when set.
	MaxScene = 64 << 10
	// MaxBlocks is the most blocks a scene may hold, every one counted; MaxCardBlocks a card's.
	MaxBlocks     = 500
	MaxCardBlocks = 12
	// MaxSceneDepth is how deeply blocks may nest: the root is 1, a child one more, and a list's item
	// two more than the list, its section being a level of its own. MaxCardDepth is a card's.
	MaxSceneDepth = 12
	MaxCardDepth  = 3
	// MaxText is the most a text block may say, in characters; MaxCardText on a card.
	MaxText     = 1000
	MaxCardText = 200
	// MaxBlockLabel is the most a button's or a chip's label may say, and MaxLabel a list section's
	// heading.
	MaxBlockLabel = 40
	// MaxLines is the most lines a text block may be held to; MaxCardLines on a card, where it is the
	// most a text block may have.
	MaxLines     = 20
	MaxCardLines = 3
	// MaxWeight is the largest share of a row's or a column's room a child may ask for.
	MaxWeight = 12
	// MaxImages is how many images a page may hold, and a card one.
	MaxImages     = 32
	MaxCardImages = 1
	// MaxImage is the most an image may be, its bytes in a scene or what its address serves, and
	// MaxImageLong and MaxImageShort the most pixels on its longer and its shorter side: a Show 8's
	// screen, either way up.
	MaxImage      = 256 << 10
	MaxImageLong  = 1280
	MaxImageShort = 800
	// SceneUpdatesPerSecond is how often an add-on's page and card are drawn again, all together. An
	// add-on may set and patch faster (each is checked and kept at once), but what is drawn is the
	// latest, at most this often: a faster sender's frames are merged, never queued.
	SceneUpdatesPerSecond = 10
	// ScreenOpenBurst and ScreenOpenEvery bound screen.open: that many at once, then one every
	// ScreenOpenEvery. Bringing a page up takes the screen from whoever looks at it.
	ScreenOpenBurst = 3
	ScreenOpenEvery = 10 * time.Second
	// ScreenOpenWindow is how long after a person's action screen.open may bring the add-on's page up:
	// one of its commands sent to it (from Home Assistant, by voice or by its button), or a tap on its
	// tile, its card or its page. Outside it screen.open is refused (busy): an add-on's page comes up
	// only because somebody asked for something of it, never at 3 a.m. of its own accord.
	ScreenOpenWindow = 5 * time.Second
	// MaxImageBudget is the most an add-on's images may take in RAM, all together: those it put
	// (image.put) and those fetched from their addresses for its scenes, each counted by ImageCost. A put
	// past it is refused (too_large); fetched ones are let go, the least recently drawn first, to make
	// room for the next, as they can be fetched again.
	MaxImageBudget = 8 << 20
	// MaxImageNames is how many images an add-on may have put at once.
	MaxImageNames = 64
	// MaxMarks is the most combining marks (accents and the like) one character may carry: a few are
	// how many scripts are written, and a stack of dozens is drawn far above and below its line, over
	// whatever is there.
	MaxMarks = 4
	// MaxDigitRun is the fewest buttons and chips labeled with one digit each, anywhere in a scene, that
	// it is refused for: ten of them, 0 to 9, are a PIN pad, which is the device's own and must not be
	// passed for on a page or a card.
	MaxDigitRun = 10
	// MaxStepperSteps is the most steps a stepper's range may hold ((max - min) / step), MaxStepperDecimals
	// the most decimal places its numbers may have, MaxStepperMagnitude the largest any may be either way,
	// and MaxUnit the most characters its unit may have.
	MaxStepperSteps     = 1000
	MaxStepperDecimals  = 2
	MaxStepperMagnitude = 1e9
	MaxUnit             = 16
)

// ImageCost is what an image counts toward MaxImageBudget: its pixels, four bytes each, which is what it
// takes decoded, or its bytes, when they are more.
func ImageCost(w, h, n int) int { return max(w*h*4, n) }

// Block is one building block of a scene. Which fields a block may have is its type's; one it may not
// have is refused, as an unknown key is. There is deliberately no field for a color, a position, a size
// in pixels or the name bar: see the package's note on scenes.
type Block struct {
	Type string `json:"type"`
	// ID names the block, for scene.patch and, on a button or a chip, for the touch event a tap sends.
	// A button and a chip must have one; any block may. Ids are unique within a scene.
	ID string `json:"id,omitempty"`
	// Weight, on a child of a row or a column, is its share of the room left after the children that
	// have none, from 1 to MaxWeight; 0 is its own size.
	Weight float64 `json:"weight,omitempty"`

	// Children are a column's, a row's or a card's, top to bottom or start to end.
	Children []Block `json:"children,omitempty"`
	// Spacing is the room between a column's, a row's or a card's children (Spacings).
	Spacing string `json:"spacing,omitempty"`
	// Sections are a list's, which scrolls: the daemon's to scroll, never the add-on's.
	Sections []Section `json:"sections,omitempty"`
	// Accent marks a card with one of the theme's colors (Accents).
	Accent string `json:"accent,omitempty"`

	// Text is a text block's words; a line break in them is kept.
	Text string `json:"text,omitempty"`
	// Size is a text block's (TextSizes), Bold its weight, Align where it sits (Aligns), and MaxLines
	// the most lines it is drawn in, the last ending with an ellipsis; 0 is as many as it needs.
	Size     string `json:"size,omitempty"`
	Bold     bool   `json:"bold,omitempty"`
	Align    string `json:"align,omitempty"`
	MaxLines int    `json:"max_lines,omitempty"`

	// URL is where an image is fetched from, an http or https address on the LAN or Home Assistant; Data
	// its bytes instead, a JPEG or a PNG (base64 in JSON); Name one the add-on put (image.put) under that
	// name. Exactly one of the three. Fit is how it fills its room (Fits).
	URL  string `json:"url,omitempty"`
	Data []byte `json:"data,omitempty"`
	Name string `json:"name,omitempty"`
	Fit  string `json:"fit,omitempty"`

	// Label and Icon are a button's or a chip's: words, an icon from Material Design Icons ("mdi:timer"),
	// or both. Selected is a chip that is picked, as one of a row of tabs.
	Label    string `json:"label,omitempty"`
	Icon     string `json:"icon,omitempty"`
	Selected bool   `json:"selected,omitempty"`

	// Value is a progress block's, from 0 to 1; Timer instead ties it to one of the add-on's own timers by
	// its id (timer.start's answer), which the daemon counts down with the clock's.
	Value *float64 `json:"value,omitempty"`
	Timer string   `json:"timer,omitempty"`

	// Min, Max and Step are a stepper's: Value is its number, from Min to Max in steps of Step (1 when it
	// gives none), drawn as − value unit + with its Label beside it. A tap on − or + changes the number at
	// once on the screen and sends the change event; the add-on may patch the block to set it otherwise.
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	Step *float64 `json:"step,omitempty"`
	Unit string   `json:"unit,omitempty"`

	// On is a toggle's: on or off, drawn as the device's own switch beside its Label. A tap flips it at
	// once on the screen and sends the change event; the add-on may patch the block to set it otherwise.
	On bool `json:"on,omitempty"`
}

// Section is one part of a list: a heading, if it likes, and its items.
type Section struct {
	Heading string  `json:"heading,omitempty"`
	Items   []Block `json:"items"`
}

// blockFields are the fields each type may have besides type, id and weight. Every field of Block is in
// one of these or among those three; a test holds Block to that, so a field cannot be added without
// saying which blocks have it.
var blockFields = map[string][]string{
	BlockColumn:   {"children", "spacing"},
	BlockRow:      {"children", "spacing", "icon", "name", "url"},
	BlockList:     {"sections"},
	BlockCard:     {"children", "spacing", "accent"},
	BlockText:     {"text", "size", "bold", "align", "max_lines"},
	BlockImage:    {"url", "data", "name", "fit"},
	BlockButton:   {"label", "icon"},
	BlockChip:     {"label", "icon", "selected"},
	BlockProgress: {"value", "timer"},
	BlockDivider:  {},
	BlockSpacer:   {},
	BlockStepper:  {"label", "value", "min", "max", "step", "unit"},
	BlockToggle:   {"label", "on"},
}

// Pressable is whether a block of this type can be tapped, and so must have an id: a button and a chip,
// whose tap sends touch, and a stepper, whose − and + send change. A list's item of any type can be tapped
// too, as a whole, when it has an id (TapTarget): a recipe in a list of them.
func Pressable(blockType string) bool {
	return blockType == BlockButton || blockType == BlockChip || blockType == BlockStepper || blockType == BlockToggle
}

// Changes is whether a block of this type is changed by a tap on the screen itself, and the add-on told
// so by the change event: a stepper's number, a toggle's on or off.
func Changes(blockType string) bool { return blockType == BlockStepper || blockType == BlockToggle }

// Thumb is a list row's picture at its start, as an image block (its name or its address, filling a small
// square), when it has one; an icon instead is the row's Icon.
func (b *Block) Thumb() (Block, bool) {
	if b.Type != BlockRow || (b.Name == "" && b.URL == "") {
		return Block{}, false
	}
	return Block{Type: BlockImage, Name: b.Name, URL: b.URL, Fit: "cover"}, true
}

// setFields is the JSON names of the block's fields that are not their zero value.
func (b *Block) setFields() []string {
	v := reflect.ValueOf(b).Elem()
	t := v.Type()
	var out []string
	for i := range t.NumField() {
		if v.Field(i).IsZero() {
			continue
		}
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		out = append(out, name)
	}
	return out
}

// iconName is an icon as a block names it, the same as an entity's.
var iconName = iconPattern

// sceneCheck is one scene being checked: its target's limits, and what it has counted so far.
type sceneCheck struct {
	card   bool
	timer  func(string) bool
	ids    map[string]bool
	blocks int
	images int
}

func tooLarge(format string, a ...any) *Error { return Errorf(CodeTooLarge, format, a...) }
func badScene(format string, a ...any) *Error { return Errorf(CodeBadRequest, format, a...) }

// ValidTarget is whether on names where a scene is shown.
func ValidTarget(on string) bool { return on == OnPage || on == OnCard }

// ValidScene checks a scene for the page or the card (OnPage, OnCard): every block of a type it may be
// there, with only its type's fields and each within its limits, ids that are names and unique, and the
// whole within the scene's limits. timer, when not nil, says whether an id is one of the add-on's own
// timers, for a progress tied to one; nil leaves that to the daemon. A refusal is an *Error: too_large
// for a limit, bad_request for anything else.
func ValidScene(on string, root Block, timer func(string) bool) error {
	if !ValidTarget(on) {
		return badScene("a scene is shown on the %q or the %q", OnPage, OnCard)
	}
	c := &sceneCheck{card: on == OnCard, timer: timer, ids: map[string]bool{}}
	if err := c.block(&root, 1, ""); err != nil {
		return err
	}
	if err := digitKeys(&root); err != nil {
		return err
	}
	// Measured as Marshal writes it, without HTML escaping, as it goes on the wire.
	b, err := Marshal(root)
	if err != nil {
		return badScene("the scene does not encode: %v", err)
	}
	if len(b) > MaxScene {
		return tooLarge("a scene is at most %d bytes as JSON; this one is %d", MaxScene, len(b))
	}
	return nil
}

func (c *sceneCheck) block(b *Block, depth int, parent string) error {
	what := "a " + b.Type
	if b.Type == BlockImage {
		what = "an image"
	}
	if b.ID != "" {
		what = fmt.Sprintf("the %s %s", b.Type, b.ID)
	}
	maxDepth, maxBlocks := MaxSceneDepth, MaxBlocks
	if c.card {
		maxDepth, maxBlocks = MaxCardDepth, MaxCardBlocks
	}
	if c.blocks++; c.blocks > maxBlocks {
		return tooLarge("a %s holds at most %d blocks", c.target(), maxBlocks)
	}
	if depth > maxDepth {
		return tooLarge("blocks on a %s nest at most %d deep", c.target(), maxDepth)
	}
	allowed, ok := blockFields[b.Type]
	switch {
	case b.Type == "":
		return badScene("a block has no type")
	case !ok:
		return badScene("there is no block %q; there are %s", b.Type, strings.Join(BlockTypes, ", "))
	case c.card && !slices.Contains(CardBlocks, b.Type):
		return badScene("a card over the clock holds only %s, not %s", strings.Join(CardBlocks, ", "), what)
	}
	for _, f := range b.setFields() {
		if f != "type" && f != "id" && f != "weight" && !slices.Contains(allowed, f) {
			return badScene("%s has no %s", what, f)
		}
	}
	if b.ID != "" {
		if !ValidName(b.ID) {
			return badScene("the block id %q is not a name (lowercase letters, digits and underscores, starting with a letter)", b.ID)
		}
		if c.ids[b.ID] {
			return badScene("the block id %s is given twice in one scene", b.ID)
		}
		c.ids[b.ID] = true
	} else if Pressable(b.Type) {
		return badScene("%s can be tapped, so it needs an id", what)
	}
	if b.Weight != 0 && (parent != BlockRow && parent != BlockColumn) {
		return badScene("%s has a weight, which only a child of a row or a column has", what)
	}
	if b.Weight < 0 || b.Weight > MaxWeight {
		return badScene("%s's weight is from 1 to %d", what, MaxWeight)
	}
	if b.Spacing != "" && !slices.Contains(Spacings, b.Spacing) {
		return badScene("%s's spacing is one of %s", what, strings.Join(Spacings, ", "))
	}

	if b.Type == BlockRow && (b.Icon != "" || b.Name != "" || b.URL != "") {
		// A list's row may have a picture or an icon at its start: a recipe's photo, a room's icon.
		switch {
		case parent != BlockList:
			return badScene("%s has a picture or an icon, which only a row that is a list's item has", what)
		case b.Icon != "" && (b.Name != "" || b.URL != ""):
			return badScene("%s has a picture or an icon, not both", what)
		case b.Icon != "" && !iconName.MatchString(b.Icon):
			return badScene("%s's icon %q is not an mdi: icon", what, b.Icon)
		}
		if thumb, ok := b.Thumb(); ok {
			if err := c.image(&thumb, what+"'s picture"); err != nil {
				return err
			}
		}
	}
	switch b.Type {
	case BlockColumn, BlockRow, BlockCard:
		if b.Accent != "" && !slices.Contains(Accents, b.Accent) {
			return badScene("%s's accent is one of the theme's %s", what, strings.Join(Accents, ", "))
		}
		for i := range b.Children {
			if err := c.block(&b.Children[i], depth+1, b.Type); err != nil {
				return err
			}
		}
	case BlockList:
		if len(b.Sections) == 0 {
			return badScene("%s has no sections", what)
		}
		for _, s := range b.Sections {
			if err := words(what+"'s heading", s.Heading, MaxLabel, false); err != nil {
				return err
			}
			for i := range s.Items {
				// A list in a list would be a scroll inside a scroll.
				if s.Items[i].Type == BlockList || hasList(s.Items[i]) {
					return badScene("%s holds a list, and a list does not go inside a list", what)
				}
				if err := c.block(&s.Items[i], depth+2, b.Type); err != nil {
					return err
				}
			}
		}
	case BlockText:
		most, lines := MaxText, MaxLines
		if c.card {
			most, lines = MaxCardText, MaxCardLines
		}
		if err := words(what, b.Text, most, true); err != nil {
			return err
		}
		switch {
		case b.Size != "" && !slices.Contains(TextSizes, b.Size):
			return badScene("%s's size is one of %s", what, strings.Join(TextSizes, ", "))
		case c.card && b.Size == SizeHuge:
			return badScene("a card's words are at most %s", SizeTitle)
		case b.Align != "" && !slices.Contains(Aligns, b.Align):
			return badScene("%s's align is one of %s", what, strings.Join(Aligns, ", "))
		case b.MaxLines < 0 || b.MaxLines > lines:
			return badScene("%s is held to at most %d lines here", what, lines)
		}
	case BlockImage:
		return c.image(b, what)
	case BlockButton, BlockChip:
		if b.Label == "" && b.Icon == "" {
			return badScene("%s has a label, an icon or both", what)
		}
		if err := words(what+"'s label", b.Label, MaxBlockLabel, false); err != nil {
			return err
		}
		if b.Icon != "" && !iconName.MatchString(b.Icon) {
			return badScene("%s's icon %q is not an mdi: icon", what, b.Icon)
		}
	case BlockStepper:
		return stepperCheck(b, what)
	case BlockToggle:
		if b.Label == "" {
			return badScene("%s has a label", what)
		}
		return words(what+"'s label", b.Label, MaxBlockLabel, false)
	case BlockProgress:
		switch {
		case (b.Value == nil) == (b.Timer == ""):
			return badScene("%s has a value or a timer, and not both", what)
		case b.Value != nil && !(*b.Value >= 0 && *b.Value <= 1):
			return badScene("%s's value is from 0 to 1", what)
		case b.Timer != "" && c.timer != nil && !c.timer(b.Timer):
			return badScene("%s names %q, which is not one of the add-on's timers", what, b.Timer)
		}
	}
	return nil
}

func (c *sceneCheck) target() string {
	if c.card {
		return OnCard
	}
	return OnPage
}

// hasList is whether a list is anywhere among b's children.
func hasList(b Block) bool {
	for _, ch := range b.Children {
		if ch.Type == BlockList || hasList(ch) {
			return true
		}
	}
	return false
}

// words checks words a block shows: at most most characters, none that is not seen but changes what is
// (HasHidden) but, where lines may break, a line break, and no character carrying more than MaxMarks
// combining marks.
func words(what, s string, most int, breaks bool) error {
	if !utf8.ValidString(s) {
		return badScene("%s is not UTF-8", what)
	}
	if n := utf8.RuneCountInString(s); n > most {
		return tooLarge("%s is at most %d characters; it is %d", what, most, n)
	}
	check := s
	if breaks {
		check = strings.ReplaceAll(s, "\n", " ")
	}
	if HasHidden(check) {
		return badScene("%s holds a control character, or one that is not seen (a zero-width space, a direction mark, a format character) or reorders the words around it", what)
	}
	marks := 0
	for _, r := range s {
		if !unicode.Is(unicode.M, r) {
			marks = 0
			continue
		}
		if marks++; marks > MaxMarks {
			return badScene("%s has a character with more than %d combining marks", what, MaxMarks)
		}
	}
	return nil
}

// Hidden is a character that words an add-on shows may not hold, as it is not seen but changes what is:
// a control character (C0, DEL and C1, the line break among them, which only a text's words may have), a
// format character (Unicode's Cf: the bidi embeddings, overrides, isolates and marks, the zero-width
// characters, the soft hyphen, the invisible operators, U+FEFF, the tag characters), or a line or
// paragraph separator. Any of them could make words read as other words than they are, or hide some.
// The zero-width non-joiner and joiner (U+200C, U+200D) are Hidden alone, but words may hold one between
// two characters that are seen, where Persian and other scripts and emoji sequences need them
// (HasHidden).
func Hidden(r rune) bool {
	switch {
	case r < 0x20, r >= 0x7f && r <= 0x9f:
		return true
	case r == 0x2028, r == 0x2029:
		return true
	}
	return unicode.Is(unicode.Cf, r) || isBidiControl(r)
}

// joiner is the zero-width non-joiner or joiner.
func joiner(r rune) bool { return r == 0x200c || r == 0x200d }

// seen is a character that is drawn: not Hidden, not a joiner, not a space.
func seen(r rune) bool { return !Hidden(r) && !unicode.IsSpace(r) }

// hiddenAt is whether rs[i] is a character words may not hold where it is: Hidden, but a joiner between
// two characters that are seen.
func hiddenAt(rs []rune, i int) bool {
	r := rs[i]
	if joiner(r) {
		return i == 0 || i == len(rs)-1 || !seen(rs[i-1]) || !seen(rs[i+1])
	}
	return Hidden(r)
}

// HasHidden is whether s holds a character words may not hold: one that is Hidden, other than a joiner
// between two characters that are seen.
func HasHidden(s string) bool {
	if !strings.ContainsFunc(s, Hidden) {
		return false
	}
	rs := []rune(s)
	for i := range rs {
		if hiddenAt(rs, i) {
			return true
		}
	}
	return false
}

// StripHidden is s without what HasHidden finds in it: words the device draws that were checked
// elsewhere, or not at all (an add-on's name, as its catalog entry gives it).
func StripHidden(s string) string {
	if !HasHidden(s) {
		return s
	}
	rs := []rune(s)
	out := make([]rune, 0, len(rs))
	for i, r := range rs {
		if !hiddenAt(rs, i) {
			out = append(out, r)
		}
	}
	return string(out)
}

// isBidiControl is a character that reorders the text around it, which could make words read as
// other words than they are.
func isBidiControl(r rune) bool {
	return (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) || r == 0x200e || r == 0x200f || r == 0x061c
}

// digitKeys checks that the scene has fewer than MaxDigitRun buttons and chips whose labels are each a
// single digit, wherever they are: ten are a PIN pad's keys (the device's own has 1 to 9, then back, 0
// and OK), which the device's own PIN pad has and no add-on may pass for. A keycap ("1️⃣") is a digit:
// its variation selector and enclosing keycap are not counted.
func digitKeys(root *Block) error {
	n := 0
	var walk func(b *Block)
	walk = func(b *Block) {
		if (b.Type == BlockButton || b.Type == BlockChip) && singleDigit(b.Label) {
			n++
		}
		for i := range b.Children {
			walk(&b.Children[i])
		}
		for s := range b.Sections {
			for i := range b.Sections[s].Items {
				walk(&b.Sections[s].Items[i])
			}
		}
	}
	walk(root)
	if n >= MaxDigitRun {
		return badScene("a scene may not have %d buttons or chips labeled with a digit each: that is a PIN pad, which is the device's own", MaxDigitRun)
	}
	return nil
}

// singleDigit is a label that is one digit, whatever the script, spaces, variation selectors and an
// enclosing keycap aside.
func singleDigit(label string) bool {
	bare := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || unicode.Is(unicode.Variation_Selector, r) || r == 0x20e3 {
			return -1
		}
		return r
	}, label)
	r, n := utf8.DecodeRuneInString(bare)
	return n > 0 && n == len(bare) && unicode.IsDigit(r)
}

// image checks an image block: an address or bytes, not both, within its limits.
func (c *sceneCheck) image(b *Block, what string) error {
	most := MaxImages
	if c.card {
		most = MaxCardImages
	}
	if c.images++; c.images > most {
		return tooLarge("a %s holds at most %d images", c.target(), most)
	}
	if b.Fit != "" && !slices.Contains(Fits, b.Fit) {
		return badScene("%s's fit is one of %s", what, strings.Join(Fits, ", "))
	}
	given := 0
	for _, set := range []bool{b.URL != "", len(b.Data) != 0, b.Name != ""} {
		if set {
			given++
		}
	}
	switch {
	case given != 1:
		return badScene("%s has an address, bytes or the name of an image put, and only one of them", what)
	case b.URL != "":
		return ImageURL(b.URL)
	case b.Name != "":
		if !ValidName(b.Name) {
			return badScene("%s's name %q is not a name (lowercase letters, digits and underscores, starting with a letter)", what, b.Name)
		}
		return nil
	}
	return ImageBytes(b.Data)
}

// ImageURL checks an image's address: http or https, with a host, at most MaxURL characters, no user
// or password in it, and, when the host is an address rather than a name, one on the LAN, never the
// device itself, loopback or link-local. A name is checked when the image is fetched, against what it
// resolves to.
func ImageURL(s string) error {
	u, err := url.Parse(s)
	switch {
	case len(s) > MaxURL:
		return tooLarge("an image's address is at most %d characters", MaxURL)
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "":
		return badScene("an image's address is an http or https address")
	case u.User != nil:
		return badScene("an image's address holds no user or password")
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && !LANAddr(ip) {
		return badScene("an image is fetched from the LAN or Home Assistant, not from %s", ip)
	}
	return nil
}

// cgnat is 100.64.0.0/10, the shared address space, which Tailscale's addresses are in.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// LANAddr is whether an image may be fetched from ip: a private address (10/8, 172.16/12, 192.168/16,
// fc00::/7), or one in 100.64/10, where a Home Assistant reached over Tailscale is. Loopback,
// link-local, multicast and the unspecified address are not, nor any public address.
func LANAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsPrivate() || cgnat.Contains(ip)
}

// pngMagic and jpegMagic begin a PNG and a JPEG.
var (
	pngMagic  = []byte("\x89PNG\r\n\x1a\n")
	jpegMagic = []byte{0xff, 0xd8, 0xff}
)

// ImageBytes checks an image's bytes: a JPEG or a PNG, at most MaxImage bytes and MaxImageLong by
// MaxImageShort pixels either way up, as its header says. Only the header is read; the daemon decodes
// the rest when it draws it, and refuses one whose pixels are not what its header said.
func ImageBytes(b []byte) error {
	if len(b) > MaxImage {
		return tooLarge("an image is at most %d bytes", MaxImage)
	}
	if !bytes.HasPrefix(b, pngMagic) && !bytes.HasPrefix(b, jpegMagic) {
		return badScene("an image is a JPEG or a PNG")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil || (format != "png" && format != "jpeg") {
		return badScene("the image does not read as a JPEG or a PNG")
	}
	return ImageSize(cfg.Width, cfg.Height)
}

// ImageSize checks an image's pixels: MaxImageLong on its longer side and MaxImageShort on its
// shorter at most, and some of each.
func ImageSize(w, h int) error {
	long, short := max(w, h), min(w, h)
	if short <= 0 || long > MaxImageLong || short > MaxImageShort {
		return tooLarge("an image is at most %d by %d pixels, either way up; this one is %d by %d",
			MaxImageLong, MaxImageShort, w, h)
	}
	return nil
}

// Find is the block with this id in the scene, and whether there is one.
func (b *Block) Find(id string) (*Block, bool) {
	if b.ID == id {
		return b, true
	}
	for i := range b.Children {
		if f, ok := b.Children[i].Find(id); ok {
			return f, true
		}
	}
	for s := range b.Sections {
		for i := range b.Sections[s].Items {
			if f, ok := b.Sections[s].Items[i].Find(id); ok {
				return f, true
			}
		}
	}
	return nil, false
}

// Clone is a copy of the scene that shares nothing with it, so one can be patched while the other is
// being drawn.
func (b Block) Clone() Block {
	if b.Children != nil {
		ch := make([]Block, len(b.Children))
		for i := range b.Children {
			ch[i] = b.Children[i].Clone()
		}
		b.Children = ch
	}
	if b.Sections != nil {
		ss := make([]Section, len(b.Sections))
		for i, s := range b.Sections {
			ss[i] = Section{Heading: s.Heading, Items: make([]Block, len(s.Items))}
			for j := range s.Items {
				ss[i].Items[j] = s.Items[j].Clone()
			}
		}
		b.Sections = ss
	}
	b.Data = slices.Clone(b.Data)
	if b.Value != nil {
		v := *b.Value
		b.Value = &v
	}
	return b
}

// Patched is a copy of the scene with the block of this id replaced by with, which keeps the id (with
// may leave it out). The copy shares nothing with the scene; it is checked as any scene is, by the
// caller. false is a scene with no block of that id.
func (b Block) Patched(id string, with Block) (Block, bool, error) {
	if with.ID != "" && with.ID != id {
		return Block{}, false, badScene("a patch keeps the block's id, %s; it gives %s", id, with.ID)
	}
	with.ID = id
	out := b.Clone()
	at, ok := out.Find(id)
	if !ok {
		return Block{}, false, nil
	}
	*at = with.Clone()
	return out, true, nil
}

// TapTarget is whether id is a block in the scene that can be tapped: a button, a chip, or an item of a
// list that has an id, which is tapped as a whole.
func (b *Block) TapTarget(id string) bool {
	if id == "" {
		return false
	}
	if f, ok := b.Find(id); ok && (f.Type == BlockButton || f.Type == BlockChip) {
		return true
	}
	return b.isItem(id)
}

// isItem is whether id is one of the scene's list items.
func (b *Block) isItem(id string) bool {
	for i := range b.Children {
		if b.Children[i].isItem(id) {
			return true
		}
	}
	for s := range b.Sections {
		for i := range b.Sections[s].Items {
			it := &b.Sections[s].Items[i]
			if it.ID == id || it.isItem(id) {
				return true
			}
		}
	}
	return false
}

// Images is the names of the images put (image.put) that the scene shows.
func (b *Block) Images() []string {
	var out []string
	var walk func(*Block)
	walk = func(x *Block) {
		if x.Name != "" && !slices.Contains(out, x.Name) {
			out = append(out, x.Name)
		}
		for i := range x.Children {
			walk(&x.Children[i])
		}
		for s := range x.Sections {
			for i := range x.Sections[s].Items {
				walk(&x.Sections[s].Items[i])
			}
		}
	}
	walk(b)
	return out
}

// stepperCheck checks a stepper: a label and a unit that are words, a value, a min and a max, a step above
// nothing, all finite, at most MaxStepperDecimals places and MaxStepperMagnitude either way, the value
// within the range, and at most MaxStepperSteps steps across it.
func stepperCheck(b *Block, what string) error {
	if err := words(what+"'s label", b.Label, MaxBlockLabel, false); err != nil {
		return err
	}
	if err := words(what+"'s unit", b.Unit, MaxUnit, false); err != nil {
		return err
	}
	if b.Value == nil || b.Min == nil || b.Max == nil {
		return badScene("%s has a value, a min and a max", what)
	}
	v, lo, hi, step := *b.Value, *b.Min, *b.Max, b.StepOr1()
	for _, x := range []float64{v, lo, hi, step} {
		if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > MaxStepperMagnitude {
			return badScene("%s's numbers are finite and at most %g either way", what, float64(MaxStepperMagnitude))
		}
		if !decimals(x) {
			return badScene("%s's numbers have at most %d decimal places", what, MaxStepperDecimals)
		}
	}
	switch {
	case step <= 0:
		return badScene("%s's step is more than 0", what)
	case lo >= hi:
		return badScene("%s's min is less than its max", what)
	case v < lo || v > hi:
		return badScene("%s's value is from its min to its max", what)
	case (hi-lo)/step > MaxStepperSteps+1e-9:
		return tooLarge("%s has at most %d steps from its min to its max", what, MaxStepperSteps)
	}
	return nil
}

// decimals is whether x has at most MaxStepperDecimals decimal places.
func decimals(x float64) bool {
	scaled := x * math.Pow10(MaxStepperDecimals)
	return math.Abs(scaled-math.Round(scaled)) < 1e-6
}

// StepOr1 is a stepper's step, 1 when it gives none.
func (b *Block) StepOr1() float64 {
	if b.Step == nil {
		return 1
	}
	return *b.Step
}

// Stepped is a stepper's value moved by n steps (−1 for −, 1 for +), held to its range and to the
// stepper's decimal places: what a tap makes it, on the screen and in the change event.
func (b *Block) Stepped(n int) float64 {
	if b.Value == nil || b.Min == nil || b.Max == nil {
		return 0
	}
	return b.Clamped(*b.Value + float64(n)*b.StepOr1())
}

// Clamped is x held to the stepper's range, rounded to its decimal places.
func (b *Block) Clamped(x float64) float64 {
	if b.Min == nil || b.Max == nil || math.IsNaN(x) {
		return 0
	}
	p := math.Pow10(MaxStepperDecimals)
	return math.Round(min(max(x, *b.Min), *b.Max)*p) / p
}

// URLs is the addresses of the images by address that the scene shows.
func (b *Block) URLs() []string {
	var out []string
	var walk func(*Block)
	walk = func(x *Block) {
		if x.URL != "" && !slices.Contains(out, x.URL) {
			out = append(out, x.URL)
		}
		for i := range x.Children {
			walk(&x.Children[i])
		}
		for s := range x.Sections {
			for i := range x.Sections[s].Items {
				walk(&x.Sections[s].Items[i])
			}
		}
	}
	walk(b)
	return out
}

// Timers is the add-on timers a scene's progress blocks are tied to.
func (b *Block) Timers() []string {
	var out []string
	var walk func(*Block)
	walk = func(x *Block) {
		if x.Timer != "" {
			out = append(out, x.Timer)
		}
		for i := range x.Children {
			walk(&x.Children[i])
		}
		for s := range x.Sections {
			for i := range x.Sections[s].Items {
				walk(&x.Sections[s].Items[i])
			}
		}
	}
	walk(b)
	return out
}
