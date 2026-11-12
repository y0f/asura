package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/y0f/asura/v3/internal/storage"
)

// DecodeConfiguration is shared by JSON and file-upload routes. Neither parser
// errors nor arbitrary uploaded values are copied into logs or error responses.
func DecodeConfiguration(reader io.Reader) (*storage.ConfigurationSnapshot, error) {
	body, err := io.ReadAll(io.LimitReader(reader, MaxConfigurationBytes+1))
	if err != nil {
		return nil, fmt.Errorf("configuration could not be read or exceeds the 32 MiB limit")
	}
	if int64(len(body)) > MaxConfigurationBytes {
		return nil, fmt.Errorf("configuration exceeds the 32 MiB limit")
	}
	if err := checkConfigurationJSON(body); err != nil {
		return nil, err
	}
	var header struct {
		Version  int  `json:"version"`
		Redacted bool `json:"redacted"`
	}
	if err := json.Unmarshal(body, &header); err != nil {
		return nil, fmt.Errorf("configuration must be a JSON object with a version")
	}
	if header.Redacted {
		return nil, fmt.Errorf("redacted configuration is for sharing only; import a full export or restore a database backup")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	switch header.Version {
	case 1:
		var legacy ExportData
		if err := decoder.Decode(&legacy); err != nil {
			return nil, fmt.Errorf("invalid v1 configuration fields or values")
		}
		return legacyConfigurationSnapshot(&legacy)
	case 2:
		var file ConfigurationFile
		if err := decoder.Decode(&file); err != nil {
			return nil, fmt.Errorf("invalid v2 configuration fields or values")
		}
		return file.snapshot(), nil
	default:
		return nil, fmt.Errorf("unsupported configuration version; expected 1 or 2")
	}
}

// The standard decoder accepts duplicate members and case-insensitive struct
// keys. Reject ambiguous files before interpreting either the version or data.
func checkConfigurationJSON(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("configuration JSON exceeds the nesting limit")
		}
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("invalid configuration JSON")
		}
		delim, container := token.(json.Delim)
		if !container {
			return nil
		}
		switch delim {
		case '{':
			members := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return fmt.Errorf("invalid configuration JSON")
				}
				name, ok := key.(string)
				if !ok {
					return fmt.Errorf("invalid configuration JSON object")
				}
				name = strings.ToLower(name)
				if members[name] {
					return fmt.Errorf("configuration contains duplicate or case-ambiguous JSON members")
				}
				members[name] = true
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("invalid configuration JSON")
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("invalid configuration JSON")
		}
		return nil
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("configuration must contain exactly one JSON document")
	}
	return nil
}

// ParseConfigurationAgentMapping accepts source:destination pairs. Mappings
// are deployment-local request options, never embedded destination IDs in an
// otherwise portable configuration file.
func ParseConfigurationAgentMapping(raw string) (map[int64]int64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	if len(raw) > 32768 {
		return nil, fmt.Errorf("agent mapping exceeds size limit")
	}
	out := make(map[int64]int64)
	for _, pair := range strings.Split(raw, ",") {
		parts := strings.Split(strings.TrimSpace(pair), ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("agent mapping must use source:destination pairs separated by commas")
		}
		source, err1 := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
		destination, err2 := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err1 != nil || err2 != nil || source <= 0 || destination <= 0 {
			return nil, fmt.Errorf("agent mapping IDs must be positive integers")
		}
		if _, duplicate := out[source]; duplicate {
			return nil, fmt.Errorf("agent mapping repeats a source ID")
		}
		out[source] = destination
	}
	return out, nil
}
