package addonapi

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// What an add-on declares in its addon.json for the API: its commands, its settings and its entities.
// The device's catalog format reads these as part of the manifest (feature/addons/catalog), and the
// daemon checks every command, setting and entity state against them.

// namePattern is a command's, an argument's, a setting's or an entity's name: it goes into a Home
// Assistant object id, and into a key the protocol reads strictly.
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// ValidName reports whether s can name a command, an argument, a setting or an entity.
func ValidName(s string) bool { return namePattern.MatchString(s) }

// Argument types, for a command's arguments.
const (
	TypeString = "string"
	TypeNumber = "number"
	TypeBool   = "bool"
)

// Setting types add these to the argument types.
const (
	TypeSecret = "secret"
	TypeSelect = "select"
	TypeEntity = "entity"
)

// Limits on what a manifest may declare.
const (
	MaxCommands     = 32
	MaxArgs         = 8
	MaxSettings     = 16
	MaxEntities     = 16
	MaxOptions      = 32
	MaxStringLength = 1024
	// MaxLabel is the longest label or option a setting, an entity or a command shows.
	MaxLabel = 64
)

// Command is one command the add-on answers, which Home Assistant and voice can call.
type Command struct {
	Name string         `json:"name"`
	Args map[string]Arg `json:"args,omitempty"`
}

// Arg is one argument of a command.
type Arg struct {
	Type     string `json:"type"`
	Required bool   `json:"required,omitempty"`
	// Max is the longest a string may be, in characters; 0 is MaxStringLength.
	Max int `json:"max,omitempty"`
}

// NeedsNothing is whether the command can be called with no arguments: such a one gets a button in
// Home Assistant.
func (c Command) NeedsNothing() bool {
	for _, a := range c.Args {
		if a.Required {
			return false
		}
	}
	return true
}

// Setting is one field of an add-on's settings, drawn on its card on the setup page.
type Setting struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Type  string `json:"type"`
	// Max is the longest a string or a secret may be; for a number, its highest value.
	Max float64 `json:"max,omitempty"`
	// Min and Step bound a number.
	Min  float64 `json:"min,omitempty"`
	Step float64 `json:"step,omitempty"`
	// Options are a select's.
	Options []string `json:"options,omitempty"`
	// Domains are the Home Assistant domains an entity setting may name ("sensor", "light").
	Domains []string `json:"domains,omitempty"`
}

// Settings is an add-on's settings: its fields, and whether its card has a Test button, which asks
// the add-on whether the values in the form work.
type Settings struct {
	Fields []Setting `json:"fields"`
	Test   bool      `json:"test,omitempty"`
}

// Entity domains an add-on may declare.
const (
	DomainSensor       = "sensor"
	DomainBinarySensor = "binary_sensor"
	DomainSwitch       = "switch"
	DomainSelect       = "select"
	DomainButton       = "button"
	DomainNumber       = "number"
)

// EntityDomains are the domains an add-on's entity can be.
var EntityDomains = []string{DomainSensor, DomainBinarySensor, DomainSwitch, DomainSelect, DomainButton, DomainNumber}

// Entity is one Home Assistant entity the add-on declares. The daemon makes it, prefixed with the
// add-on's id, and the add-on sets its state and hears its commands.
type Entity struct {
	Domain string `json:"domain"`
	ID     string `json:"id"`
	Name   string `json:"name"`
	Icon   string `json:"icon,omitempty"`
	// Unit is a sensor's or a number's ("°C", "min").
	Unit string `json:"unit,omitempty"`
	// Options are a select's.
	Options []string `json:"options,omitempty"`
	// Min, Max and Step are a number's.
	Min  float64 `json:"min,omitempty"`
	Max  float64 `json:"max,omitempty"`
	Step float64 `json:"step,omitempty"`
	// Text is whether a sensor's state is words rather than a number.
	Text bool `json:"text,omitempty"`
}

// Ref is the entity as the catalog lists it, domain and id: "sensor.step".
func (e Entity) Ref() string { return e.Domain + "." + e.ID }

// iconPattern is an icon from Material Design Icons, as Home Assistant names one.
var iconPattern = regexp.MustCompile(`^mdi:[a-z0-9-]{1,64}$`)

func label(what, s string) error {
	switch {
	case strings.TrimSpace(s) == "":
		return fmt.Errorf("%s has no name", what)
	case utf8.RuneCountInString(s) > MaxLabel:
		return fmt.Errorf("%s's name is longer than %d characters", what, MaxLabel)
	case strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }):
		return fmt.Errorf("%s's name holds a control character", what)
	}
	return nil
}

// ValidCommands checks the commands a manifest declares.
func ValidCommands(cmds []Command) error {
	if len(cmds) > MaxCommands {
		return fmt.Errorf("it declares %d commands, more than %d", len(cmds), MaxCommands)
	}
	seen := map[string]bool{}
	for _, c := range cmds {
		switch {
		case !ValidName(c.Name):
			return fmt.Errorf("the command %q is not a name (lowercase letters, digits and underscores, starting with a letter)", c.Name)
		case seen[c.Name]:
			return fmt.Errorf("the command %s is declared twice", c.Name)
		case len(c.Args) > MaxArgs:
			return fmt.Errorf("the command %s has %d arguments, more than %d", c.Name, len(c.Args), MaxArgs)
		}
		seen[c.Name] = true
		for name, a := range c.Args {
			switch {
			case !ValidName(name):
				return fmt.Errorf("the command %s's argument %q is not a name", c.Name, name)
			case a.Type != TypeString && a.Type != TypeNumber && a.Type != TypeBool:
				return fmt.Errorf("the command %s's argument %s is a %q, not a string, number or bool", c.Name, name, a.Type)
			case a.Max < 0 || a.Max > MaxStringLength:
				return fmt.Errorf("the command %s's argument %s has a longest length outside 1 to %d", c.Name, name, MaxStringLength)
			case a.Max != 0 && a.Type != TypeString:
				return fmt.Errorf("the command %s's argument %s gives a longest length, which only a string has", c.Name, name)
			}
		}
	}
	return nil
}

// CheckArgs checks what a command was called with against its declaration: every required argument
// there, nothing it does not declare, each of its type and a string no longer than its longest. A
// number arrives as JSON's, a float64.
func (c Command) CheckArgs(args map[string]any) error {
	for name, a := range c.Args {
		if _, ok := args[name]; !ok && a.Required {
			return fmt.Errorf("%s needs %s", c.Name, name)
		}
	}
	for name, v := range args {
		a, ok := c.Args[name]
		if !ok {
			return fmt.Errorf("%s takes no argument %q", c.Name, name)
		}
		switch a.Type {
		case TypeString:
			s, ok := v.(string)
			if !ok {
				return fmt.Errorf("%s's %s is a string", c.Name, name)
			}
			most := a.Max
			if most == 0 {
				most = MaxStringLength
			}
			if utf8.RuneCountInString(s) > most {
				return fmt.Errorf("%s's %s is at most %d characters", c.Name, name, most)
			}
		case TypeNumber:
			f, ok := v.(float64)
			if !ok || math.IsNaN(f) || math.IsInf(f, 0) {
				return fmt.Errorf("%s's %s is a number", c.Name, name)
			}
		case TypeBool:
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("%s's %s is true or false", c.Name, name)
			}
		}
	}
	return nil
}

// CoerceArgs is args with each value Home Assistant may have turned into another type put back in the
// type the command declares: a number or true/false where a string is declared becomes its text, and
// text that reads as a number where a number is declared becomes that number. A Home Assistant template
// renders "1" as the number 1 however it was quoted, so a season or an episode said as a number would
// otherwise never reach a command that takes it as text. Anything else is left for CheckArgs to refuse.
func (c Command) CoerceArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return args
	}
	out := make(map[string]any, len(args))
	for name, v := range args {
		out[name] = v
		a, ok := c.Args[name]
		if !ok {
			continue
		}
		switch a.Type {
		case TypeString:
			switch x := v.(type) {
			case float64:
				if !math.IsNaN(x) && !math.IsInf(x, 0) {
					out[name] = strconv.FormatFloat(x, 'f', -1, 64)
				}
			case bool:
				out[name] = strconv.FormatBool(x)
			}
		case TypeNumber:
			if x, ok := v.(string); ok {
				if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
					out[name] = f
				}
			}
		}
	}
	return out
}

// Find is the command named name.
func FindCommand(cmds []Command, name string) (Command, bool) {
	for _, c := range cmds {
		if c.Name == name {
			return c, true
		}
	}
	return Command{}, false
}

// CommandNames is the names of cmds, sorted.
func CommandNames(cmds []Command) []string {
	out := make([]string, 0, len(cmds))
	for _, c := range cmds {
		out = append(out, c.Name)
	}
	slices.Sort(out)
	return out
}

// Valid checks a settings schema.
func (s Settings) Valid() error {
	if len(s.Fields) == 0 {
		return errors.New("its settings have no fields")
	}
	if len(s.Fields) > MaxSettings {
		return fmt.Errorf("it has %d settings, more than %d", len(s.Fields), MaxSettings)
	}
	seen := map[string]bool{}
	for _, f := range s.Fields {
		what := "the setting " + f.Key
		if !ValidName(f.Key) {
			return fmt.Errorf("the setting %q is not a name", f.Key)
		}
		if seen[f.Key] {
			return fmt.Errorf("%s is declared twice", what)
		}
		seen[f.Key] = true
		if err := label(what, f.Label); err != nil {
			return err
		}
		switch f.Type {
		case TypeString, TypeSecret:
			if f.Max < 0 || f.Max > MaxStringLength || f.Max != math.Trunc(f.Max) {
				return fmt.Errorf("%s has a longest length outside 1 to %d", what, MaxStringLength)
			}
		case TypeBool:
		case TypeNumber:
			if f.Max <= f.Min || f.Step < 0 || math.IsInf(f.Max, 0) || math.IsInf(f.Min, 0) {
				return fmt.Errorf("%s needs a min below its max, and a step of 0 or more", what)
			}
		case TypeSelect:
			if err := options(what, f.Options); err != nil {
				return err
			}
		case TypeEntity:
			if len(f.Domains) == 0 {
				return fmt.Errorf("%s names no Home Assistant domain its entity may be of", what)
			}
			for _, d := range f.Domains {
				if !ValidName(d) {
					return fmt.Errorf("%s names %q, which is not a domain", what, d)
				}
			}
		default:
			return fmt.Errorf("%s is a %q, not a string, secret, bool, number, select or entity", what, f.Type)
		}
		if f.Type != TypeSelect && len(f.Options) > 0 {
			return fmt.Errorf("%s has options, which only a select has", what)
		}
		if f.Type != TypeEntity && len(f.Domains) > 0 {
			return fmt.Errorf("%s has domains, which only an entity setting has", what)
		}
	}
	return nil
}

func options(what string, opts []string) error {
	if len(opts) == 0 || len(opts) > MaxOptions {
		return fmt.Errorf("%s needs 1 to %d options", what, MaxOptions)
	}
	for i, o := range opts {
		if err := label(what+"'s option", o); err != nil {
			return err
		}
		if slices.Contains(opts[:i], o) {
			return fmt.Errorf("%s has the option %q twice", what, o)
		}
	}
	return nil
}

// Field is the setting named key.
func (s Settings) Field(key string) (Setting, bool) {
	for _, f := range s.Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Setting{}, false
}

// entityIDPattern is a Home Assistant entity id, for an entity setting.
var entityIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z0-9_]+$`)

// Check is whether v can be this setting's value: of its type, within its bounds.
func (f Setting) Check(v any) error {
	switch f.Type {
	case TypeString, TypeSecret:
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s is words", f.Label)
		}
		most := int(f.Max)
		if most == 0 {
			most = MaxStringLength
		}
		if utf8.RuneCountInString(s) > most {
			return fmt.Errorf("%s is at most %d characters", f.Label, most)
		}
		if strings.ContainsRune(s, 0) {
			return fmt.Errorf("%s holds a NUL", f.Label)
		}
	case TypeBool:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s is on or off", f.Label)
		}
	case TypeNumber:
		n, ok := v.(float64)
		if !ok || math.IsNaN(n) || n < f.Min || n > f.Max {
			return fmt.Errorf("%s is a number from %g to %g", f.Label, f.Min, f.Max)
		}
	case TypeSelect:
		s, ok := v.(string)
		if !ok || !slices.Contains(f.Options, s) {
			return fmt.Errorf("%s is one of its options", f.Label)
		}
	case TypeEntity:
		s, ok := v.(string)
		if !ok || (s != "" && !entityIDPattern.MatchString(s)) {
			return fmt.Errorf("%s is a Home Assistant entity id", f.Label)
		}
		if domain, _, _ := strings.Cut(s, "."); s != "" && !slices.Contains(f.Domains, domain) {
			return fmt.Errorf("%s is an entity of %s", f.Label, strings.Join(f.Domains, ", "))
		}
	}
	return nil
}

// ValidEntities checks the entities a manifest declares.
func ValidEntities(ents []Entity) error {
	if len(ents) > MaxEntities {
		return fmt.Errorf("it declares %d entities, more than %d", len(ents), MaxEntities)
	}
	seen := map[string]bool{}
	for _, e := range ents {
		what := "the entity " + e.Ref()
		switch {
		case !slices.Contains(EntityDomains, e.Domain):
			return fmt.Errorf("the entity %s is a %q, not one of %s", e.ID, e.Domain, strings.Join(EntityDomains, ", "))
		case !ValidName(e.ID):
			return fmt.Errorf("the entity id %q is not a name", e.ID)
		case seen[e.ID]:
			// One id for two domains would still be two object ids alike once prefixed.
			return fmt.Errorf("the entity id %s is declared twice", e.ID)
		case e.Icon != "" && !iconPattern.MatchString(e.Icon):
			return fmt.Errorf("%s's icon %q is not an mdi: icon", what, e.Icon)
		case utf8.RuneCountInString(e.Unit) > 16:
			return fmt.Errorf("%s's unit is longer than 16 characters", what)
		}
		seen[e.ID] = true
		if err := label(what, e.Name); err != nil {
			return err
		}
		switch e.Domain {
		case DomainSelect:
			if err := options(what, e.Options); err != nil {
				return err
			}
		case DomainNumber:
			if e.Max <= e.Min || e.Step < 0 {
				return fmt.Errorf("%s needs a min below its max, and a step of 0 or more", what)
			}
		}
		if e.Domain != DomainSelect && len(e.Options) > 0 {
			return fmt.Errorf("%s has options, which only a select has", what)
		}
		if e.Domain != DomainNumber && (e.Min != 0 || e.Max != 0 || e.Step != 0) {
			return fmt.Errorf("%s has a min, max or step, which only a number has", what)
		}
		if e.Domain != DomainSensor && e.Text {
			return fmt.Errorf("%s is text, which only a sensor can be", what)
		}
	}
	return nil
}

// EntityRefs is the entities as the catalog lists them, sorted.
func EntityRefs(ents []Entity) []string {
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, e.Ref())
	}
	slices.Sort(out)
	return out
}

// FindEntity is the entity with this id.
func FindEntity(ents []Entity, id string) (Entity, bool) {
	for _, e := range ents {
		if e.ID == id {
			return e, true
		}
	}
	return Entity{}, false
}

// MaxEntityText is the longest a text sensor's state may be, as Home Assistant keeps one.
const MaxEntityText = 255

// CheckState is whether v can be the entity's state: a number for a sensor (words for a text one),
// a bool for a binary sensor or a switch, one of a select's options, a number within a number's
// bounds. A button has none.
func (e Entity) CheckState(v any) error {
	switch e.Domain {
	case DomainSensor:
		if e.Text {
			s, ok := v.(string)
			if !ok || utf8.RuneCountInString(s) > MaxEntityText {
				return fmt.Errorf("%s's state is at most %d characters of words", e.ID, MaxEntityText)
			}
			return nil
		}
		if f, ok := v.(float64); !ok || math.IsNaN(f) || math.IsInf(f, 0) {
			return fmt.Errorf("%s's state is a number", e.ID)
		}
	case DomainBinarySensor, DomainSwitch:
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s's state is true or false", e.ID)
		}
	case DomainSelect:
		s, ok := v.(string)
		if !ok || !slices.Contains(e.Options, s) {
			return fmt.Errorf("%s's state is one of its options", e.ID)
		}
	case DomainNumber:
		f, ok := v.(float64)
		if !ok || math.IsNaN(f) || f < e.Min || f > e.Max {
			return fmt.Errorf("%s's state is a number from %g to %g", e.ID, e.Min, e.Max)
		}
	case DomainButton:
		return fmt.Errorf("%s is a button, which has no state", e.ID)
	}
	return nil
}
