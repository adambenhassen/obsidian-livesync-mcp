package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
}

func TestRequireBearerRejectsMissingOrWrong(t *testing.T) {
	h := RequireBearer("secret", okHandler())
	for _, hdr := range []string{"", "Bearer wrong", "secret"} {
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/", nil)
		if hdr != "" {
			req.Header.Set("Authorization", hdr)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("auth %q → %d, want 401", hdr, rec.Code)
		}
	}
}

func TestRequireBearerAllowsCorrect(t *testing.T) {
	h := RequireBearer("secret", okHandler())
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/", nil)
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("got %d, want 200", rec.Code)
	}
}

func TestEmptyTokenDisablesAuth(t *testing.T) {
	h := RequireBearer("", okHandler())
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("empty token should allow through, got %d", rec.Code)
	}
}
