package validate

import (
	"encoding/json"
	"testing"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestMonitorMQTTExpectationValidation(t *testing.T) {
	for _, tc := range []struct {
		settings string
		valid    bool
	}{
		{`{}`, true},
		{`{"topic":"health/+","expect_message":"ready"}`, true},
		{`{"expect_message":"private-message"}`, false},
		{`{"topic":"health/#/invalid"}`, false},
	} {
		monitor := &storage.Monitor{
			Name: "MQTT fixture", Type: "mqtt", Target: "broker.example.test:1883",
			Interval: 60, Timeout: 10, FailureThreshold: 1, SuccessThreshold: 1,
			Settings: json.RawMessage(tc.settings),
		}
		if err := ValidateMonitor(monitor); (err == nil) != tc.valid {
			t.Errorf("validation=%v, valid=%v", err, tc.valid)
		}
	}
}
