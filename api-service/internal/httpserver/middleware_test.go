package httpserver

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// newTestServer builds a Server with only the JWT secret set — enough for the
// auth/middleware paths that never touch the DB.
func newTestServer() *Server {
	return &Server{jwtSecret: testSecret}
}

func TestRequireAdmin(t *testing.T) {
	s := newTestServer()
	adminTok, _ := GenerateAccessToken(1, "admin", "admin", testSecret)
	userTok, _ := GenerateAccessToken(2, "user", "user", testSecret)
	refreshTok, _ := GenerateRefreshToken(1, "admin", testSecret)

	tests := []struct {
		name       string
		authHeader string
		wantStatus int
		wantNext   bool
	}{
		{"no header", "", http.StatusUnauthorized, false},
		{"bad format", "Token abc", http.StatusUnauthorized, false},
		{"invalid token", "Bearer not-a-jwt", http.StatusUnauthorized, false},
		{"refresh token rejected", "Bearer " + refreshTok, http.StatusUnauthorized, false},
		{"non-admin forbidden", "Bearer " + userTok, http.StatusForbidden, false},
		{"admin allowed", "Bearer " + adminTok, http.StatusOK, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextCalled := false
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				nextCalled = true
				w.WriteHeader(http.StatusOK)
			})

			req := httptest.NewRequest(http.MethodGet, "/admin/x", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rr := httptest.NewRecorder()

			s.RequireAdmin(next).ServeHTTP(rr, req)

			if rr.Code != tt.wantStatus {
				t.Errorf("status: got %d, want %d", rr.Code, tt.wantStatus)
			}
			if nextCalled != tt.wantNext {
				t.Errorf("next called: got %v, want %v", nextCalled, tt.wantNext)
			}
		})
	}
}

func TestHandleSendMessageMethodGuard(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/rooms/send-messages/1", nil)
	rr := httptest.NewRecorder()

	s.handleSendMessage(rr, req)

	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleGetMyRoomsUnauthorized(t *testing.T) {
	s := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/rooms", nil)
	rr := httptest.NewRecorder()

	s.handleGetMyRooms(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestWithCORSPreflight(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("next must not be called on OPTIONS preflight")
	})
	req := httptest.NewRequest(http.MethodOptions, "/anything", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()

	WithCORS(next).ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Errorf("status: got %d, want %d", rr.Code, http.StatusNoContent)
	}
	if got := rr.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("CORS origin: got %q", got)
	}
	if got := rr.Header().Get("Access-Control-Allow-Credentials"); got != "true" {
		t.Errorf("CORS credentials: got %q", got)
	}
}
