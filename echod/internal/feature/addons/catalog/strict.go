package catalog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// decodeStrict reads a catalog or a manifest the one way it can be read. Go's decoder takes the last of
// two keys with the same name, matches a key to a field whatever its case, and skips a key it has no
// field for. Each of those lets one file say two things: a reviewer reads the first "ports", the device
// the second; a field spelled "Root" sits beside "root"; a key this build does not know means something
// to a later one. Signed or pinned, such a file was not reviewed as the device reads it. So a key has to
// be lowercase, appear once in its object, and name a field; and nothing may follow the value.
func decodeStrict(b []byte, v any) error {
	if err := checkKeys(b); err != nil {
		return err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if _, err := d.Token(); !errors.Is(err, io.EOF) {
		return errors.New("something follows the JSON value")
	}
	return nil
}

// keyPattern is every key a catalog or a manifest has.
var keyPattern = regexp.MustCompile(`^[a-z0-9_]+$`)

// checkKeys walks the tokens and refuses a key with a capital in it, or one its object already has.
func checkKeys(b []byte) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	type frame struct {
		object bool
		keys   map[string]bool
		key    bool // the next token in this object is a key
	}
	var stack []*frame
	for {
		t, err := d.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var top *frame
		if len(stack) > 0 {
			top = stack[len(stack)-1]
		}
		if top != nil && top.object && top.key && t != json.Delim('}') {
			k, ok := t.(string)
			if !ok {
				return errors.New("an object key is not a string")
			}
			// ASCII lowercase, digits and underscores only: Go matches a key to a field with Unicode case
			// folding, so a key with a letter outside ASCII (U+017F, the long s, folds to s) can name the
			// same field as one spelled otherwise.
			if !keyPattern.MatchString(k) {
				return fmt.Errorf("the key %q is not lowercase ASCII letters, digits and underscores", k)
			}
			for seen := range top.keys {
				if strings.EqualFold(seen, k) {
					return fmt.Errorf("the key %q is given twice in one object", k)
				}
			}
			top.keys[k] = true
			top.key = false
			continue
		}
		switch t {
		case json.Delim('{'):
			stack = append(stack, &frame{object: true, keys: map[string]bool{}, key: true})
			continue
		case json.Delim('['):
			stack = append(stack, &frame{})
			continue
		case json.Delim('}'), json.Delim(']'):
			stack = stack[:len(stack)-1]
		}
		// A value has been read, whole: in an object, a key comes next.
		if len(stack) > 0 && stack[len(stack)-1].object {
			stack[len(stack)-1].key = true
		}
	}
}
