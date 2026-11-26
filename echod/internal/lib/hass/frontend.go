package hass

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
)

// The icons and words Home Assistant serves its own frontend for drawing an entity: by domain and
// device class, and by integration and translation key (#760). A dashboard drawn on the device asks for
// the ones on its page, so that a tile shows what the browser shows, and in the device's language.

// IconRule is one entry of Home Assistant's icons: an icon for each state it has one for, an icon for
// each range of a number (keyed by where the range starts), and a default for anything else.
type IconRule struct {
	Default string            `json:"default"`
	State   map[string]string `json:"state"`
	Range   map[string]string `json:"range"`
}

// For is the icon for a state: the state's own, else the range a number is in, else the default.
func (r IconRule) For(state string) string {
	if ic := r.State[state]; ic != "" {
		return ic
	}
	if len(r.Range) > 0 {
		if v, err := strconv.ParseFloat(state, 64); err == nil {
			// The range a number is in starts at the highest key not above it, as the frontend has it.
			best, ic := math.Inf(-1), ""
			for k, name := range r.Range {
				if at, err := strconv.ParseFloat(k, 64); err == nil && at <= v && at > best {
					best, ic = at, name
				}
			}
			if ic != "" {
				return ic
			}
		}
	}
	return r.Default
}

// ComponentIcons is the icons of each domain asked for, by device class, with "_" for the domain's
// own: frontend/get_icons in the entity_component category.
func (l *Live) ComponentIcons(ctx context.Context, domains []string) (map[string]map[string]IconRule, error) {
	res, err := l.icons(ctx, "entity_component", domains)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]IconRule{}
	for domain, raw := range res {
		if rules := iconRules(raw); len(rules) > 0 {
			out[domain] = rules
		}
	}
	return out, nil
}

// EntityIcons is the icons of each integration asked for, by domain and translation key:
// frontend/get_icons in the entity category.
func (l *Live) EntityIcons(ctx context.Context, platforms []string) (map[string]map[string]map[string]IconRule, error) {
	res, err := l.icons(ctx, "entity", platforms)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]map[string]IconRule{}
	for platform, raw := range res {
		var byDomain map[string]json.RawMessage
		if json.Unmarshal(raw, &byDomain) != nil {
			continue
		}
		for domain, r := range byDomain {
			if rules := iconRules(r); len(rules) > 0 {
				if out[platform] == nil {
					out[platform] = map[string]map[string]IconRule{}
				}
				out[platform][domain] = rules
			}
		}
	}
	return out, nil
}

func (l *Live) icons(ctx context.Context, category string, integrations []string) (map[string]json.RawMessage, error) {
	if len(integrations) == 0 {
		return nil, nil
	}
	raw, err := l.Call(ctx, map[string]any{"type": "frontend/get_icons", "category": category, "integration": integrations})
	if err != nil {
		return nil, err
	}
	var res struct {
		Resources map[string]json.RawMessage `json:"resources"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return res.Resources, nil
}

// iconRules reads a key-to-rule map entry by entry: one entry of a shape this does not know (a
// state_attributes block, say, which a rule here has no use for) loses only itself.
func iconRules(raw json.RawMessage) map[string]IconRule {
	var byKey map[string]json.RawMessage
	if json.Unmarshal(raw, &byKey) != nil {
		return nil
	}
	out := map[string]IconRule{}
	for k, r := range byKey {
		var rule IconRule
		if json.Unmarshal(r, &rule) == nil && (rule.Default != "" || len(rule.State) > 0 || len(rule.Range) > 0) {
			out[k] = rule
		}
	}
	return out
}

// Translations is Home Assistant's words in language for the integrations asked for, in one category
// ("entity_component" or "entity"), each by its full key:
// "component.binary_sensor.entity_component.battery_charging.state.off" is "Not charging".
func (l *Live) Translations(ctx context.Context, language, category string, integrations []string) (map[string]string, error) {
	if len(integrations) == 0 {
		return nil, nil
	}
	raw, err := l.Call(ctx, map[string]any{"type": "frontend/get_translations", "language": language,
		"category": category, "integration": integrations})
	if err != nil {
		return nil, err
	}
	var res struct {
		Resources map[string]any `json:"resources"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(res.Resources))
	for k, v := range res.Resources {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out, nil
}
