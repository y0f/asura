package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/y0f/asura/v3/internal/validate"
)

func TestMaintenanceFormTargetParsing(t *testing.T) {
	h := &Handler{}
	for _, tc := range []struct {
		name    string
		values  []string
		all     string
		want    []int64
		wantErr bool
	}{
		{"repeated select values", []string{"1", "2"}, "", []int64{1, 2}, false},
		{"legacy comma values", []string{"1, 2"}, "", []int64{1, 2}, false},
		{"explicit all", nil, "on", nil, false},
		{"invalid token", []string{"no-such-monitor"}, "", nil, true},
		{"partially invalid", []string{"1,invalid"}, "", nil, true},
		{"zero ID", []string{"0"}, "", nil, true},
		{"negative ID", []string{"-1"}, "", nil, true},
		{"ambiguous all and IDs", []string{"1"}, "on", nil, true},
		{"duplicate IDs", []string{"1", "1"}, "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			form := url.Values{"name": {"Maintenance"}, "recurring": {"manual"}, "monitor_ids": tc.values, "all_monitors": {tc.all}}
			window, err := h.parseMaintenanceForm(adminRequest(http.MethodPost, "/maintenance", form))
			if err == nil {
				err = validate.ValidateMaintenanceWindow(window)
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("got error %v, want error %t", err, tc.wantErr)
			}
			if !tc.wantErr && !reflect.DeepEqual(window.MonitorIDs, tc.want) {
				t.Fatalf("parsed targets %v, want %v", window.MonitorIDs, tc.want)
			}
		})
	}
}

func TestMaintenanceInvalidTargetsNeverBecomeAllMonitorWindow(t *testing.T) {
	h, store := storeHandler(t)
	r := adminRequest(http.MethodPost, "/maintenance", url.Values{
		"name": {"Invalid target"}, "recurring": {"manual"}, "monitor_ids": {"not-a-monitor"},
	})
	w := httptest.NewRecorder()
	h.MaintenanceCreate(w, r)
	windows, err := store.ListMaintenanceWindows(context.Background())
	if err != nil || len(windows) != 0 {
		t.Fatalf("invalid target silently broadened maintenance: %+v, %v", windows, err)
	}
}
