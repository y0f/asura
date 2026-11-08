package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/internal/config"
	"github.com/y0f/asura/internal/httputil"
	"github.com/y0f/asura/internal/storage"
)

func TestGroupAndOnCallWriteOnlyNavigation(t *testing.T) {
	h, s := storeHandler(t)
	h.cfg.Server.BasePath = "/console"
	ctx := context.Background()
	key := &config.APIKeyConfig{Permissions: []string{"groups.create", "groups.update", "groups.delete", "on_call.create", "on_call.update", "on_call.delete", "notifications.read", "incidents.read"}}
	mutate := func(handler http.HandlerFunc, id string, form url.Values) *httptest.ResponseRecorder {
		r := adminRequest("POST", "/console/", form)
		r.SetPathValue("id", id)
		r = r.WithContext(context.WithValue(r.Context(), httputil.CtxKeyAPIKey, key))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	wantHome := func(w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != 303 || w.Header().Get("Location") != "/console/incidents" {
			t.Fatalf("write-only navigation: %d %s", w.Code, w.Header().Get("Location"))
		}
	}
	wantHome(mutate(h.GroupCreate, "", url.Values{"name": {"group"}, "sort_order": {"2"}}))
	groups, err := s.ListMonitorGroups(ctx)
	if err != nil || len(groups) != 1 {
		t.Fatal("group create failed")
	}
	g := groups[0]
	wantHome(mutate(h.GroupUpdate, strconvI(g.ID), url.Values{"name": {"renamed"}, "sort_order": {"3"}}))
	if w := mutate(h.GroupUpdate, strconvI(g.ID), url.Values{"name": {"wrong"}, "sort_order": {"bad"}}); w.Code != 400 {
		t.Fatal("invalid sort order silently replaced value")
	}
	current, err := s.GetMonitorGroup(ctx, g.ID)
	if err != nil || current.Name != "renamed" || current.SortOrder != 3 || !current.CreatedAt.Equal(g.CreatedAt) {
		t.Fatal("group update changed unrelated/invalid data")
	}
	wantHome(mutate(h.GroupDelete, strconvI(g.ID), nil))
	if w := mutate(h.GroupUpdate, strconvI(g.ID), url.Values{"name": {"missing"}}); w.Code != 404 {
		t.Fatalf("missing group update reported success: %d", w.Code)
	}
	ch := &storage.NotificationChannel{Name: "channel", Type: "webhook", Settings: []byte("{}"), Enabled: true}
	if err := s.CreateNotificationChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	wantHome(mutate(h.OnCallCreate, "", url.Values{"name": {"rotation"}, "period": {"weekly"}, "channel_ids[]": {strconvI(ch.ID)}}))
	rotations, err := s.ListOnCallRotations(ctx)
	if err != nil || len(rotations) != 1 {
		t.Fatal("rotation create failed")
	}
	wantHome(mutate(h.OnCallOverride, strconvI(rotations[0].ID), url.Values{"override_channel_id": {""}}))
	wantHome(mutate(h.OnCallDelete, strconvI(rotations[0].ID), nil))
}

type unreadableOnCallStore struct {
	storage.Store
	stage string
}

func (s unreadableOnCallStore) ListOnCallRotations(ctx context.Context) ([]*storage.OnCallRotation, error) {
	if s.stage == "rotations" {
		return nil, errors.New("rotation read failed")
	}
	return s.Store.ListOnCallRotations(ctx)
}
func (s unreadableOnCallStore) ListNotificationChannels(context.Context) ([]*storage.NotificationChannel, error) {
	return nil, errors.New("channel read failed")
}

func TestOnCallReadFailuresDoNotRenderEmptyEditableState(t *testing.T) {
	for _, stage := range []string{"rotations", "channels"} {
		h, s := storeHandler(t)
		h.store = unreadableOnCallStore{Store: s, stage: stage}
		w := httptest.NewRecorder()
		h.OnCallRotations(w, adminRequest("GET", "/on-call", nil))
		if w.Code != 500 || strings.Contains(w.Body.String(), "<form") {
			t.Fatalf("%s failure rendered usable empty form: %d", stage, w.Code)
		}
	}
}

func TestOnCallMalformedFormsDoNotApplyPartialValues(t *testing.T) {
	for _, method := range []string{"create", "override"} {
		h, _ := storeHandler(t)
		r := httptest.NewRequest("POST", "/on-call", strings.NewReader("name=parsed&period=weekly&channel_ids%5B%5D=1&invalid=%zz"))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.SetPathValue("id", "1")
		w := httptest.NewRecorder()
		if method == "create" {
			h.OnCallCreate(w, r)
		} else {
			h.OnCallOverride(w, r)
		}
		if w.Code != 400 {
			t.Fatalf("%s malformed form accepted: %d", method, w.Code)
		}
	}
}
