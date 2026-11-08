package assertion

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/y0f/asura/internal/jsonutil"
)

// Parse validates the complete configuration before evaluation. Invalid rules
// cannot be hidden by a passing sibling in an OR group. Writes and runtime
// checks share the same limits and schema, including legacy stored input.
func Parse(raw json.RawMessage) (ConditionSet, error) {
	trimmed := bytes.TrimSpace(raw)
	var set ConditionSet
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[]")) || bytes.Equal(trimmed, []byte("null")) {
		return set, nil
	}
	if err := jsonutil.DecodeObject(raw, &set, 1<<20); err != nil {
		return ConditionSet{}, fmt.Errorf("assertions must be a valid condition set object")
	}
	if err := validateConditions(set); err != nil {
		return ConditionSet{}, err
	}
	return set, nil
}

func validateConditions(set ConditionSet) error {
	if !validLogic(set.Operator) || len(set.Groups) > 100 {
		return fmt.Errorf("assertion groups/operator exceed supported limits")
	}
	for _, group := range set.Groups {
		if !validLogic(group.Operator) || len(group.Conditions) > 100 {
			return fmt.Errorf("assertion conditions/operator exceed supported limits")
		}
		for _, condition := range group.Conditions {
			operators := []string{"", "eq", "neq", "gt", "lt", "gte", "lte"}
			switch condition.Type {
			case "status_code", "response_time", "cert_expiry":
				if _, err := strconv.ParseInt(condition.Value, 10, 64); err != nil {
					return fmt.Errorf("numeric assertion requires an integer value")
				}
			case "body_contains":
				operators = []string{"", "contains", "not_contains"}
			case "body_regex":
				operators = []string{"", "matches", "not_matches"}
				if _, err := compileRegex(condition.Value); err != nil {
					return fmt.Errorf("invalid assertion regular expression")
				}
			case "header", "json_path":
				operators = append(operators, "contains", "not_contains", "exists")
				if condition.Target == "" {
					return fmt.Errorf("header/JSON assertion target is required")
				}
				if slices.Contains([]string{"gt", "lt", "gte", "lte"}, condition.Operator) {
					value, err := strconv.ParseFloat(condition.Value, 64)
					if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
						return fmt.Errorf("numeric header/JSON assertion requires a finite number")
					}
				}
			case "dns_record":
				operators = []string{"", "contains", "eq"}
			default:
				return fmt.Errorf("unknown assertion type")
			}
			if !slices.Contains(operators, condition.Operator) {
				return fmt.Errorf("unsupported assertion operator")
			}
		}
	}
	return nil
}
