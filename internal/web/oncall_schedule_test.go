package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/y0f/asura/v2/internal/storage"
)

func TestOnCallFormRejectsInvalidTargetsAndKeepsOverride(t *testing.T) {
	h, s := storeHandler(t)
	ctx := context.Background()
	ch := &storage.NotificationChannel{Name: "primary", Type: "webhook", Settings: []byte("{}"), Enabled: true}
	if err := s.CreateNotificationChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	for _, channel := range []string{"invalid", "0", "-1", "99999"} {
		w := httptest.NewRecorder()
		h.OnCallCreate(w, adminRequest(http.MethodPost, "/on-call", url.Values{
			"name": {"invalid"}, "period": {"daily"}, "channel_ids[]": {channel},
		}))
	}
	rotations, err := s.ListOnCallRotations(ctx)
	if err != nil || len(rotations) != 0 {
		t.Fatalf("invalid channel form created rotations: %+v %v", rotations, err)
	}
	expiry := time.Now().UTC().Add(time.Hour).Truncate(time.Second)
	r := &storage.OnCallRotation{Name: "primary", Period: "weekly", ChannelIDs: []int64{ch.ID}, OverrideChannelID: &ch.ID, OverrideUntil: &expiry}
	if err := s.CreateOnCallRotation(ctx, r); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ channel, until string }{
		{"invalid", expiry.Format("2006-01-02T15:04")},
		{strconv.FormatInt(ch.ID, 10), "invalid"},
		{strconv.FormatInt(ch.ID, 10), time.Now().Add(-time.Hour).Format("2006-01-02T15:04")},
	} {
		req := adminRequest(http.MethodPost, fmt.Sprintf("/on-call/%d/override", r.ID), url.Values{
			"override_channel_id": {tc.channel}, "override_until": {tc.until},
		})
		req.SetPathValue("id", strconv.FormatInt(r.ID, 10))
		h.OnCallOverride(httptest.NewRecorder(), req)
		got, err := s.GetOnCallRotation(ctx, r.ID)
		if err != nil || got.OverrideChannelID == nil || *got.OverrideChannelID != ch.ID || got.OverrideUntil == nil || !got.OverrideUntil.Equal(expiry) {
			t.Fatalf("invalid override cleared existing state: %+v %v", got, err)
		}
	}
	req := adminRequest(http.MethodPost, fmt.Sprintf("/on-call/%d/override", r.ID), url.Values{
		"override_channel_id": {""}, "override_until": {expiry.Format("2006-01-02T15:04")},
	})
	req.SetPathValue("id", strconv.FormatInt(r.ID, 10))
	h.OnCallOverride(httptest.NewRecorder(), req)
	got, err := s.GetOnCallRotation(ctx, r.ID)
	if err != nil || got.OverrideChannelID != nil || !got.PeriodStartedAt.Equal(r.PeriodStartedAt) {
		t.Fatalf("explicit clear failed or changed cadence: %+v %v", got, err)
	}
}
