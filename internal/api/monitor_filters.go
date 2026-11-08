package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
	"github.com/y0f/asura/internal/validate"
)

func monitorListFilter(r *http.Request) (storage.MonitorListFilter, error) {
	q := r.URL.Query()
	f := storage.MonitorListFilter{Access: httputil.MonitorAccess(r.Context(), "monitors.read"), Type: q.Get("type"), Search: strings.TrimSpace(q.Get("q")), Status: q.Get("status"), Sort: q.Get("sort")}
	if f.Type != "" && !validate.ValidMonitorTypes[f.Type] {
		return f, fmt.Errorf("invalid monitor type filter")
	}
	if len(f.Search) > 255 {
		return f, fmt.Errorf("q must be a monitor name prefix of at most 255 UTF-8 bytes")
	}
	switch f.Status {
	case "", "up", "down", "degraded", "paused", "pending":
	default:
		return f, fmt.Errorf("invalid monitor status filter")
	}
	switch f.Sort {
	case "", "name", "status", "last_check", "response_time":
	default:
		return f, fmt.Errorf("invalid monitor sort order")
	}
	for _, field := range []struct {
		name   string
		target **int64
	}{{"group", &f.GroupID}, {"tag", &f.TagID}} {
		if value := q.Get(field.name); value != "" {
			id, err := strconv.ParseInt(value, 10, 64)
			if err != nil || id <= 0 {
				return f, fmt.Errorf("%s must be a positive ID", field.name)
			}
			*field.target = &id
		}
	}
	return f, nil
}
