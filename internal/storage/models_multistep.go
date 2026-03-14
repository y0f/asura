package storage

// MultiStepHTTPSettings holds configuration for multi-step HTTP checks.
type MultiStepHTTPSettings struct {
	Steps []HTTPStep `json:"steps"`
}

// HTTPStep defines a single request in a multi-step sequence.
type HTTPStep struct {
	Name           string            `json:"name"`
	URL            string            `json:"url"`
	Method         string            `json:"method"`
	Headers        map[string]string `json:"headers,omitempty"`
	Body           string            `json:"body,omitempty"`
	ExpectedStatus int               `json:"expected_status,omitempty"`
	ExtractVar     string            `json:"extract_var,omitempty"`   // variable name to set
	ExtractRegex   string            `json:"extract_regex,omitempty"` // regex with capture group
	ExtractJSON    string            `json:"extract_json,omitempty"`  // simple dot-path like "data.token"
}
