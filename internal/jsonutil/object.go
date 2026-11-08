// Package jsonutil decodes configuration objects without ambiguous JSON fields.
package jsonutil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

var settingsFields sync.Map // reflect.Type -> immutable JSON member types

// DecodeObject rejects duplicate or noncanonical typed fields before decoding.
// Map keys retain their spelling. The limit applies to the complete input.
func DecodeObject(raw []byte, target any, maxBytes int) error {
	trimmed := bytes.TrimSpace(raw)
	if len(raw) > maxBytes || len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return fmt.Errorf("expected a JSON object of at most %d bytes", maxBytes)
	}
	if err := checkSettingsKeys(trimmed, target); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("JSON object contains an unknown field or invalid field type")
	}
	return nil
}

func settingFields(t reflect.Type) map[string]reflect.Type {
	if cached, ok := settingsFields.Load(t); ok {
		return cached.(map[string]reflect.Type)
	}
	fields := make(map[string]reflect.Type, t.NumField())
	for i := range t.NumField() {
		field := t.Field(i)
		name := strings.Split(field.Tag.Get("json"), ",")[0]
		if name != "" && name != "-" && field.IsExported() {
			fields[name] = field.Type
		}
	}
	actual, _ := settingsFields.LoadOrStore(t, fields)
	return actual.(map[string]reflect.Type)
}

// Go's struct decoder accepts case-insensitive names. Settings also pass through
// maps for credential preservation and redaction, so writes use exact JSON names
// and reject duplicate members. Map keys (for example custom header names) keep
// their spelling; nested typed objects follow the same rule as the root.
func checkSettingsKeys(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var visit func(reflect.Type, int) error
	visit = func(t reflect.Type, depth int) error {
		if depth > 64 {
			return errors.New("settings exceed the JSON nesting limit")
		}
		for t != nil && t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		token, err := decoder.Token()
		if err != nil {
			return errors.New("settings must be valid JSON")
		}
		delim, container := token.(json.Delim)
		if !container {
			return nil
		}
		switch delim {
		case '{':
			seen := make(map[string]bool)
			var fields map[string]reflect.Type
			if t != nil && t.Kind() == reflect.Struct {
				fields = settingFields(t)
			}
			for decoder.More() {
				member, err := decoder.Token()
				if err != nil {
					return errors.New("settings must be valid JSON")
				}
				name, ok := member.(string)
				if !ok || seen[name] {
					return errors.New("settings contain duplicate JSON members")
				}
				seen[name] = true
				var child reflect.Type
				if fields != nil {
					var exists bool
					child, exists = fields[name]
					if !exists {
						return errors.New("settings contain an unknown or incorrectly capitalized field")
					}
				} else if t != nil && t.Kind() == reflect.Map {
					child = t.Elem()
				}
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
		case '[':
			var child reflect.Type
			if t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
				child = t.Elem()
			}
			for decoder.More() {
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
		default:
			return errors.New("settings must be valid JSON")
		}
		if _, err := decoder.Token(); err != nil {
			return errors.New("settings must be valid JSON")
		}
		return nil
	}
	return visit(reflect.TypeOf(target), 0)
}
