package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSLAReportPeriodBoundaries(t *testing.T) {
	srv, _ := regressionServer(t, nil)
	t.Cleanup(srv.Close)
	cookie := webLoginCookie(t, srv, regressionAdminKey)
	future := time.Now().UTC().AddDate(0, 2, 0).Format("2006-01")
	for _, path := range []string{"/api/v1/reports/sla", "/api/v1/reports/sla/export", "/sla", "/sla/export"} {
		for _, period := range []string{"invalid", "0000-01", future} {
			t.Run(path+"/"+period, func(t *testing.T) {
				req := httptest.NewRequest("GET", path+"?period="+period, nil)
				req.Header.Set("X-API-Key", regressionAdminKey)
				req.AddCookie(cookie)
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, req)
				if w.Code != http.StatusBadRequest {
					t.Fatalf("period %q: status=%d, body=%s", period, w.Code, w.Body.String())
				}
			})
		}
	}
}
