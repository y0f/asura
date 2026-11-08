package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/y0f/asura/v2/internal/config"
	"github.com/y0f/asura/v2/internal/pageauth"
)

func TestStatusPagePasswordLegacyLoginAndSaltedResetAcrossAPIAndWeb(t *testing.T) {
	for _, legacy := range []string{"legacy-password", strings.Repeat("x", pageauth.MaxPasswordBytes+1)} {
		t.Run(fmt.Sprintf("legacy bytes %d", len(legacy)), func(t *testing.T) {
			env := newTestEnv(t, nil, false, nil)
			page := env.createStatusPage(t, `{"slug":"password-flow","title":"Password flow","enabled":true,"api_enabled":true}`)
			page.PasswordHash = config.HashAPIKey(legacy)
			if err := env.store.UpdateStatusPage(context.Background(), page); err != nil {
				t.Fatal(err)
			}
			publicPath := fmt.Sprintf("/api/v1/status-pages/%d/public", page.ID)
			if w := env.do(http.MethodGet, publicPath, "", map[string]string{"X-Status-Page-Password": legacy}); w.Code != http.StatusOK {
				t.Fatalf("legacy header rejected: %d", w.Code)
			}
			login := env.do(http.MethodPost, "/password-flow/auth", url.Values{"password": {legacy}}.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
			cookies := login.Result().Cookies()
			if login.Code != http.StatusSeeOther || len(cookies) != 1 {
				t.Fatalf("legacy web login rejected: %d", login.Code)
			}
			oldCookie := map[string]string{"Cookie": cookies[0].Name + "=" + cookies[0].Value}
			if w := env.do(http.MethodGet, publicPath, "", oldCookie); w.Code != http.StatusOK {
				t.Fatalf("legacy cookie rejected: %d", w.Code)
			}
			writePath := fmt.Sprintf("/api/v1/status-pages/%d", page.ID)
			if w := env.do(http.MethodPut, writePath, `{"password":"new-password"}`, admin); w.Code != http.StatusOK {
				t.Fatalf("password reset failed: %d %s", w.Code, w.Body.String())
			}
			stored, err := env.store.GetStatusPage(context.Background(), page.ID)
			if err != nil || !strings.HasPrefix(stored.PasswordHash, "$asura-pbkdf2-sha256$") || !pageauth.ValidHash(stored.PasswordHash) {
				t.Fatalf("reset did not store a supported salted hash: %v", err)
			}
			if w := env.do(http.MethodGet, publicPath, "", oldCookie); w.Code != http.StatusUnauthorized {
				t.Fatalf("reset retained old cookie authority: %d", w.Code)
			}
			if w := env.do(http.MethodGet, publicPath, "", map[string]string{"X-Status-Page-Password": "new-password"}); w.Code != http.StatusOK {
				t.Fatalf("salted password header rejected: %d", w.Code)
			}
			login = env.do(http.MethodPost, "/password-flow/auth", url.Values{"password": {"new-password"}}.Encode(), map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
			if login.Code != http.StatusSeeOther || len(login.Result().Cookies()) != 1 {
				t.Fatalf("salted web login rejected: %d", login.Code)
			}
			oversized, _ := json.Marshal(map[string]string{"password": strings.Repeat("x", pageauth.MaxPasswordBytes+1)})
			if w := env.do(http.MethodPut, writePath, string(oversized), admin); w.Code != http.StatusBadRequest {
				t.Fatalf("oversized replacement accepted: %d", w.Code)
			}
			after, err := env.store.GetStatusPage(context.Background(), page.ID)
			if err != nil || after.PasswordHash != stored.PasswordHash {
				t.Fatalf("rejected replacement changed existing protection: %v", err)
			}
		})
	}
}
