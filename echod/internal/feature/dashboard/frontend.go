//go:build !dot

package dashboard

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/HuskerMinion/techo5/echod/internal/config"
	"github.com/HuskerMinion/techo5/echod/internal/lib/hass"
	"github.com/HuskerMinion/techo5/echod/internal/lib/mdi"
)

// haLook is what Home Assistant's own frontend picks a tile's icon and state words from (#760): the
// integration and translation key of each entity on the page, and the icons and words served for
// them. describe takes from it first and falls back on the device's own tables for anything it has
// no entry for, an older Home Assistant, or a fetch that failed. It is replaced whole, never changed
// in place; nil is an empty one.
type haLook struct {
	entities  map[string]hass.Registered
	component map[string]map[string]hass.IconRule            // domain, device class or "_"
	entity    map[string]map[string]map[string]hass.IconRule // platform, domain, translation key
	words     map[string]string                              // by full translation key
}

// icon is the entity's icon as the frontend picks it after the state's own icon attribute, which
// describe has looked at already: one set in its settings, then its integration's for its
// translation key, then its domain's for its device class, then its domain's own; in each the
// state's, else the default. Only an icon the device's font has counts, so that one it lacks goes on
// to the next, and to the device's own table after them all. "" for none.
func (h *haLook) icon(e hass.LiveEntity, domain, class string) string {
	if h == nil {
		return ""
	}
	reg := h.entities[e.ID]
	var tries []string
	tries = append(tries, reg.Icon)
	if reg.TranslationKey != "" {
		if r, ok := h.entity[reg.Platform][domain][reg.TranslationKey]; ok {
			tries = append(tries, r.For(e.State))
		}
	}
	if class != "" {
		if r, ok := h.component[domain][class]; ok {
			tries = append(tries, r.For(e.State))
		}
	}
	if r, ok := h.component[domain]["_"]; ok {
		tries = append(tries, r.For(e.State))
	}
	for _, ic := range tries {
		if _, ok := mdi.Rune(ic); ic != "" && ok {
			return ic
		}
	}
	return ""
}

// word is the entity's state in Home Assistant's words, in the order its frontend looks: the
// integration's for the translation key, then the domain's for the device class, then the domain's
// own. false when none of them has the state.
func (h *haLook) word(e hass.LiveEntity, domain, class string) (string, bool) {
	if h == nil || e.State == "" {
		return "", false
	}
	reg := h.entities[e.ID]
	var keys []string
	if reg.Platform != "" && reg.TranslationKey != "" {
		keys = append(keys, "component."+reg.Platform+".entity."+domain+"."+reg.TranslationKey+".state."+e.State)
	}
	if class != "" {
		keys = append(keys, "component."+domain+".entity_component."+class+".state."+e.State)
	}
	keys = append(keys, "component."+domain+".entity_component._.state."+e.State)
	for _, k := range keys {
		if w := h.words[k]; w != "" {
			return w, true
		}
	}
	return "", false
}

// withListing is h with the registry's entries replaced by listing's: what the registry says now, the
// icons and words kept.
func (h *haLook) withListing(listing []hass.Registered) *haLook {
	out := &haLook{entities: byID(listing)}
	if h != nil {
		out.component, out.entity, out.words = h.component, h.entity, h.words
	}
	return out
}

func byID(listing []hass.Registered) map[string]hass.Registered {
	m := make(map[string]hass.Registered, len(listing))
	for _, e := range listing {
		m[e.ID] = e
	}
	return m
}

// loadHALook asks Home Assistant for the icons and words of the domains and integrations of the
// entities ids, in the device's language. Whatever it will not give is left out, and describe uses the
// device's own for it: an older Home Assistant has no frontend/get_icons, and a tile that has its old
// icon is no reason for a dashboard not to load.
func loadHALook(ctx context.Context, live *hass.Live, ids []string, listing []hass.Registered) *haLook {
	h := &haLook{entities: byID(listing)}
	var domains, platforms []string
	for _, id := range ids {
		domain, _, _ := strings.Cut(id, ".")
		if domain != "" && !slices.Contains(domains, domain) {
			domains = append(domains, domain)
		}
		if p := h.entities[id].Platform; p != "" && !slices.Contains(platforms, p) {
			platforms = append(platforms, p)
		}
	}
	if len(domains) == 0 {
		return h
	}
	var err error
	if h.component, err = live.ComponentIcons(ctx, domains); err != nil {
		slog.Info("drawn dashboard: no icons from Home Assistant", "err", err)
		h.component = nil
	}
	if h.entity, err = live.EntityIcons(ctx, platforms); err != nil {
		slog.Info("drawn dashboard: no integration icons from Home Assistant", "err", err)
		h.entity = nil
	}
	lang := haLanguage(config.Get().Screen.Language)
	h.words = map[string]string{}
	for _, q := range []struct {
		category string
		of       []string
	}{{"entity_component", domains}, {"entity", platforms}} {
		w, err := live.Translations(ctx, lang, q.category, q.of)
		if err != nil {
			slog.Info("drawn dashboard: no state words from Home Assistant", "category", q.category, "err", err)
			continue
		}
		for k, v := range w {
			h.words[k] = v
		}
	}
	return h
}

// haLanguage is the screen's language as Home Assistant names its translations: English for none set,
// and Valencian as Catalan, which is the nearest Home Assistant has.
func haLanguage(screen string) string {
	switch screen {
	case "":
		return "en"
	case "ca-valencia":
		return "ca"
	}
	return screen
}
