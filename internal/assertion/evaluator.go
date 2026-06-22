package assertion

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var _regexCache sync.Map

func compileRegex(pattern string) (*regexp.Regexp, error) {
	if cached, ok := _regexCache.Load(pattern); ok {
		return cached.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	_regexCache.Store(pattern, re)
	return re, nil
}

func Evaluate(assertionsJSON json.RawMessage, statusCode int, body string,
	headers map[string]string, responseTimeMs int64, certExpiry *int64, dnsRecords []string) AssertionResult {

	var cs ConditionSet
	if err := json.Unmarshal(assertionsJSON, &cs); err != nil || len(cs.Groups) == 0 {
		return AssertionResult{Pass: true}
	}

	var allDetails []AssertionDetail
	var groupPasses []bool
	var messages []string
	degraded := false

	for _, g := range cs.Groups {
		gr := evalGroup(g, statusCode, body, headers, responseTimeMs, certExpiry, dnsRecords)
		allDetails = append(allDetails, gr.Details...)
		groupPasses = append(groupPasses, gr.Pass)
		if !gr.Pass && gr.Message != "" {
			messages = append(messages, gr.Message)
		}
		if gr.Degraded {
			degraded = true
		}
	}

	pass := combinePasses(groupPasses, cs.Operator)
	return AssertionResult{
		Pass:     pass,
		Degraded: !pass && degraded,
		Message:  strings.Join(messages, "; "),
		Details:  allDetails,
	}
}

func evalGroup(g ConditionGroup, statusCode int, body string,
	headers map[string]string, responseTimeMs int64, certExpiry *int64, dnsRecords []string) AssertionResult {

	if len(g.Conditions) == 0 {
		return AssertionResult{Pass: true}
	}

	var details []AssertionDetail
	var condPasses []bool
	var messages []string
	degraded := false

	for _, a := range g.Conditions {
		detail := evaluateSingle(a, statusCode, body, headers, responseTimeMs, certExpiry, dnsRecords)
		details = append(details, detail)
		condPasses = append(condPasses, detail.Pass)
		if !detail.Pass {
			if a.Degraded {
				degraded = true
			}
			if detail.Message != "" {
				messages = append(messages, detail.Message)
			}
		}
	}

	pass := combinePasses(condPasses, g.Operator)
	return AssertionResult{
		Pass:     pass,
		Degraded: !pass && degraded,
		Message:  strings.Join(messages, "; "),
		Details:  details,
	}
}

func combinePasses(passes []bool, operator string) bool {
	if len(passes) == 0 {
		return true
	}
	if operator == "or" {
		for _, p := range passes {
			if p {
				return true
			}
		}
		return false
	}
	for _, p := range passes {
		if !p {
			return false
		}
	}
	return true
}

func evaluateSingle(a Assertion, statusCode int, body string,
	headers map[string]string, responseTimeMs int64, certExpiry *int64, dnsRecords []string) AssertionDetail {

	switch a.Type {
	case "status_code":
		return evalStatusCode(a, statusCode)
	case "body_contains":
		return evalBodyContains(a, body)
	case "body_regex":
		return evalBodyRegex(a, body)
	case "json_path":
		return evalJSONPath(a, body)
	case "header":
		return evalHeader(a, headers)
	case "response_time":
		return evalResponseTime(a, responseTimeMs)
	case "cert_expiry":
		return evalCertExpiry(a, certExpiry)
	case "dns_record":
		return evalDNSRecord(a, dnsRecords)
	default:
		return AssertionDetail{
			Assertion: a,
			Pass:      false,
			Message:   fmt.Sprintf("unknown assertion type: %s", a.Type),
		}
	}
}

func evalStatusCode(a Assertion, statusCode int) AssertionDetail {
	expected, _ := strconv.Atoi(a.Value)
	actual := strconv.Itoa(statusCode)
	pass := compareInt(statusCode, expected, a.Operator)
	msg := ""
	if !pass {
		msg = fmt.Sprintf("status_code: expected %s %s, got %d", a.Operator, a.Value, statusCode)
	}
	return AssertionDetail{Assertion: a, Pass: pass, Actual: actual, Message: msg}
}

func evalBodyContains(a Assertion, body string) AssertionDetail {
	pass := false
	switch a.Operator {
	case "contains", "":
		pass = strings.Contains(body, a.Value)
	case "not_contains":
		pass = !strings.Contains(body, a.Value)
	}
	msg := ""
	if !pass {
		msg = fmt.Sprintf("body_contains: %s '%s' failed", a.Operator, truncate(a.Value, 50))
	}
	return AssertionDetail{Assertion: a, Pass: pass, Message: msg}
}

func evalBodyRegex(a Assertion, body string) AssertionDetail {
	re, err := compileRegex(a.Value)
	if err != nil {
		return AssertionDetail{
			Assertion: a, Pass: false,
			Message: fmt.Sprintf("body_regex: invalid pattern: %v", err),
		}
	}
	pass := false
	switch a.Operator {
	case "matches", "":
		pass = re.MatchString(body)
	case "not_matches":
		pass = !re.MatchString(body)
	}
	msg := ""
	if !pass {
		msg = fmt.Sprintf("body_regex: pattern '%s' %s failed", truncate(a.Value, 50), a.Operator)
	}
	return AssertionDetail{Assertion: a, Pass: pass, Message: msg}
}

func evalJSONPath(a Assertion, body string) AssertionDetail {
	val, err := walkJSONPath(body, a.Target)
	if err != nil {
		if a.Operator == "exists" {
			return AssertionDetail{
				Assertion: a, Pass: false, Actual: "",
				Message: fmt.Sprintf("json_path: %s does not exist", a.Target),
			}
		}
		return AssertionDetail{
			Assertion: a, Pass: false,
			Message: fmt.Sprintf("json_path: %v", err),
		}
	}

	actual := fmt.Sprintf("%v", val)

	if a.Operator == "exists" {
		return AssertionDetail{Assertion: a, Pass: true, Actual: actual}
	}

	pass := compareString(actual, a.Value, a.Operator)
	msg := ""
	if !pass {
		msg = fmt.Sprintf("json_path %s: expected %s %s, got %s", a.Target, a.Operator, a.Value, truncate(actual, 100))
	}
	return AssertionDetail{Assertion: a, Pass: pass, Actual: actual, Message: msg}
}

func evalHeader(a Assertion, headers map[string]string) AssertionDetail {
	headerName := a.Target
	val, exists := headers[headerName]
	if !exists {
		// Try case-insensitive
		for k, v := range headers {
			if strings.EqualFold(k, headerName) {
				val = v
				exists = true
				break
			}
		}
	}

	if a.Operator == "exists" {
		pass := exists
		msg := ""
		if !pass {
			msg = fmt.Sprintf("header: %s does not exist", headerName)
		}
		return AssertionDetail{Assertion: a, Pass: pass, Actual: val, Message: msg}
	}

	if !exists {
		return AssertionDetail{
			Assertion: a, Pass: false,
			Message: fmt.Sprintf("header: %s not found", headerName),
		}
	}

	pass := compareString(val, a.Value, a.Operator)
	msg := ""
	if !pass {
		msg = fmt.Sprintf("header %s: expected %s %s, got %s", headerName, a.Operator, a.Value, truncate(val, 100))
	}
	return AssertionDetail{Assertion: a, Pass: pass, Actual: val, Message: msg}
}

func evalResponseTime(a Assertion, responseTimeMs int64) AssertionDetail {
	expected, _ := strconv.ParseInt(a.Value, 10, 64)
	actual := strconv.FormatInt(responseTimeMs, 10)
	pass := compareInt64(responseTimeMs, expected, a.Operator)
	msg := ""
	if !pass {
		msg = fmt.Sprintf("response_time: expected %s %sms, got %dms", a.Operator, a.Value, responseTimeMs)
	}
	return AssertionDetail{Assertion: a, Pass: pass, Actual: actual, Message: msg}
}

func evalCertExpiry(a Assertion, certExpiry *int64) AssertionDetail {
	if certExpiry == nil {
		return AssertionDetail{
			Assertion: a, Pass: false,
			Message: "cert_expiry: no certificate expiry data",
		}
	}

	// Value is in days
	expectedDays, _ := strconv.ParseInt(a.Value, 10, 64)
	daysUntilExpiry := (*certExpiry - _nowFunc().Unix()) / 86400
	actual := strconv.FormatInt(daysUntilExpiry, 10)

	pass := compareInt64(daysUntilExpiry, expectedDays, a.Operator)
	msg := ""
	if !pass {
		msg = fmt.Sprintf("cert_expiry: expected %s %s days, got %d days", a.Operator, a.Value, daysUntilExpiry)
	}
	return AssertionDetail{Assertion: a, Pass: pass, Actual: actual, Message: msg}
}

func evalDNSRecord(a Assertion, dnsRecords []string) AssertionDetail {
	actual := strings.Join(dnsRecords, ", ")

	switch a.Operator {
	case "contains", "":
		for _, r := range dnsRecords {
			if strings.Contains(r, a.Value) {
				return AssertionDetail{Assertion: a, Pass: true, Actual: actual}
			}
		}
		return AssertionDetail{
			Assertion: a, Pass: false, Actual: actual,
			Message: fmt.Sprintf("dns_record: '%s' not found in records", a.Value),
		}
	case "eq":
		for _, r := range dnsRecords {
			if r == a.Value {
				return AssertionDetail{Assertion: a, Pass: true, Actual: actual}
			}
		}
		return AssertionDetail{
			Assertion: a, Pass: false, Actual: actual,
			Message: fmt.Sprintf("dns_record: '%s' not found (exact match)", a.Value),
		}
	default:
		return AssertionDetail{
			Assertion: a, Pass: false,
			Message: fmt.Sprintf("dns_record: unsupported operator %s", a.Operator),
		}
	}
}

// walkJSONPath walks a JSON document using dot notation with array indexing.
// Examples: "status", "data.name", "items[0].id", "nested.list[2].value"
func walkJSONPath(jsonStr string, path string) (any, error) {
	var root any
	if err := json.Unmarshal([]byte(jsonStr), &root); err != nil {
		return nil, fmt.Errorf("invalid JSON body")
	}

	parts := splitPath(path)
	current := root

	for _, part := range parts {
		key, indices, err := parsePathPart(part)
		if err != nil {
			return nil, err
		}

		if key != "" {
			obj, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("expected object at %s", key)
			}
			val, exists := obj[key]
			if !exists {
				return nil, fmt.Errorf("key %s not found", key)
			}
			current = val
		}

		for _, idx := range indices {
			arr, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("expected array at index %d", idx)
			}
			if idx < 0 || idx >= len(arr) {
				return nil, fmt.Errorf("index %d out of range (len=%d)", idx, len(arr))
			}
			current = arr[idx]
		}
	}

	return current, nil
}

// splitPath splits "a.b[0].c" into ["a", "b[0]", "c"]
func splitPath(path string) []string {
	var parts []string
	current := ""
	for _, ch := range path {
		if ch == '.' {
			if current != "" {
				parts = append(parts, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// parsePathPart parses "name[0][1]" into ("name", []int{0, 1}, nil) or
// "name" into ("name", nil, nil). Malformed bracket segments return an error.
func parsePathPart(part string) (string, []int, error) {
	bracketIdx := strings.Index(part, "[")
	if bracketIdx == -1 {
		return part, nil, nil
	}

	key := part[:bracketIdx]
	rest := part[bracketIdx:]

	var indices []int
	for rest != "" {
		if rest[0] != '[' {
			return "", nil, fmt.Errorf("malformed path segment %q", part)
		}
		closeIdx := strings.IndexByte(rest, ']')
		if closeIdx == -1 {
			return "", nil, fmt.Errorf("malformed path segment %q: missing ']'", part)
		}
		idx, err := strconv.Atoi(rest[1:closeIdx])
		if err != nil {
			return "", nil, fmt.Errorf("malformed array index in %q", part)
		}
		indices = append(indices, idx)
		rest = rest[closeIdx+1:]
	}

	return key, indices, nil
}

func compareInt(actual, expected int, op string) bool {
	switch op {
	case "eq", "":
		return actual == expected
	case "neq":
		return actual != expected
	case "gt":
		return actual > expected
	case "lt":
		return actual < expected
	case "gte":
		return actual >= expected
	case "lte":
		return actual <= expected
	default:
		return actual == expected
	}
}

func compareInt64(actual, expected int64, op string) bool {
	switch op {
	case "eq", "":
		return actual == expected
	case "neq":
		return actual != expected
	case "gt":
		return actual > expected
	case "lt":
		return actual < expected
	case "gte":
		return actual >= expected
	case "lte":
		return actual <= expected
	default:
		return actual == expected
	}
}

func compareString(actual, expected, op string) bool {
	switch op {
	case "eq", "":
		return actual == expected
	case "neq":
		return actual != expected
	case "contains":
		return strings.Contains(actual, expected)
	case "not_contains":
		return !strings.Contains(actual, expected)
	case "gt":
		a, _ := strconv.ParseFloat(actual, 64)
		e, _ := strconv.ParseFloat(expected, 64)
		return a > e
	case "lt":
		a, _ := strconv.ParseFloat(actual, 64)
		e, _ := strconv.ParseFloat(expected, 64)
		return a < e
	case "gte":
		a, _ := strconv.ParseFloat(actual, 64)
		e, _ := strconv.ParseFloat(expected, 64)
		return a >= e
	case "lte":
		a, _ := strconv.ParseFloat(actual, 64)
		e, _ := strconv.ParseFloat(expected, 64)
		return a <= e
	default:
		return actual == expected
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// _nowFunc allows overriding time in tests.
var _nowFunc = time.Now
