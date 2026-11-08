package redact

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"
)

var errAmbiguousSettings = errors.New("settings must be an unambiguous JSON object with unique fields")

// Go's typed JSON decoder folds Unicode field names and merges repeated object
// members. A map decoder instead keeps only the last member. Do not let those
// two interpretations disagree about credentials in legacy configuration.
// Legacy typed keys are normalized, while custom header names keep their case.
// Incoming keys retain their spelling for subsequent strict schema validation.
func settingsObject(raw json.RawMessage, legacy bool) (map[string]any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	if len(raw) > 1<<20 {
		return nil, errAmbiguousSettings
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var read func(int, bool) (any, error)
	read = func(depth int, headers bool) (any, error) {
		if depth > 64 {
			return nil, errAmbiguousSettings
		}
		token, err := decoder.Token()
		if err != nil {
			return nil, errAmbiguousSettings
		}
		switch token {
		case json.Delim('{'):
			object := make(map[string]any)
			seen := make(map[string]bool)
			for decoder.More() {
				member, err := decoder.Token()
				if err != nil {
					return nil, errAmbiguousSettings
				}
				name, ok := member.(string)
				if !ok {
					return nil, errAmbiguousSettings
				}
				folded := foldSettingKey(name)
				if seen[folded] {
					return nil, errAmbiguousSettings
				}
				seen[folded] = true
				value, err := read(depth+1, !headers && folded == "headers")
				if err != nil {
					return nil, err
				}
				if legacy && !headers {
					name = folded
				}
				object[name] = value
			}
			if end, err := decoder.Token(); err != nil || end != json.Delim('}') {
				return nil, errAmbiguousSettings
			}
			return object, nil
		case json.Delim('['):
			array := make([]any, 0)
			for decoder.More() {
				value, err := read(depth+1, false)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			if end, err := decoder.Token(); err != nil || end != json.Delim(']') {
				return nil, errAmbiguousSettings
			}
			return array, nil
		default:
			return token, nil
		}
	}
	value, err := read(0, false)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, errAmbiguousSettings
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errAmbiguousSettings
	}
	return object, nil
}

// EqualFold also matches the long s and Kelvin sign. Simple lowercasing would
// miss those aliases, so choose the smallest rune in each Unicode fold cycle.
func foldSettingKey(key string) string {
	return strings.Map(func(r rune) rune {
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		return unicode.ToLower(minimum)
	}, key)
}
