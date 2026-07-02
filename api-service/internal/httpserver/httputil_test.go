package httpserver

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetIDFromURL(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    int64
		wantErr bool
	}{
		{"rooms send-messages", "/rooms/send-messages/42", 42, false},
		{"rooms messages", "/rooms/messages/7", 7, false},
		{"rooms last-seen", "/rooms/last-seen/9", 9, false},
		{"rooms unread", "/rooms/unread/3", 3, false},
		// Regression: these 4-segment routes were broken when the id was
		// read from segment index 2 ("summary"/"users") instead of the last.
		{"messages seen summary", "/messages/seen/summary/123", 123, false},
		{"messages seen users", "/messages/seen/users/456", 456, false},
		{"trailing slash", "/rooms/messages/8/", 8, false},
		{"non-numeric id", "/messages/seen/summary/abc", 0, true},
		{"missing id", "/rooms/unread/", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tt.path, nil)
			got, err := getIDFromURL(r)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for path %q, got id=%d", tt.path, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error for path %q: %v", tt.path, err)
			}
			if got != tt.want {
				t.Errorf("path %q: got %d, want %d", tt.path, got, tt.want)
			}
		})
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    string
		wantErr bool
	}{
		{"valid", "Bearer abc.def.ghi", "abc.def.ghi", false},
		{"case-insensitive scheme", "bearer tok", "tok", false},
		{"missing header", "", "", true},
		{"wrong scheme", "Basic abc", "", true},
		{"no token part", "Bearer", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				r.Header.Set("Authorization", tt.header)
			}
			got, err := bearerToken(r)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for header %q", tt.header)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNsToString(t *testing.T) {
	if got := nsToString(sql.NullString{Valid: false}); got != "" {
		t.Errorf("invalid NullString: got %q, want empty", got)
	}
	if got := nsToString(sql.NullString{String: "x", Valid: true}); got != "x" {
		t.Errorf("valid NullString: got %q, want %q", got, "x")
	}
}

func TestGetIP(t *testing.T) {
	tests := []struct {
		name       string
		realIP     string
		forwarded  string
		remoteAddr string
		want       string
	}{
		{"x-real-ip wins", "1.2.3.4", "5.6.7.8", "9.9.9.9:1", "1.2.3.4"},
		{"x-forwarded-for first", "", "5.6.7.8, 9.9.9.9", "1.1.1.1:2", "5.6.7.8"},
		{"remote addr fallback", "", "", "1.1.1.1:2222", "1.1.1.1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tt.remoteAddr
			if tt.realIP != "" {
				r.Header.Set("X-Real-IP", tt.realIP)
			}
			if tt.forwarded != "" {
				r.Header.Set("X-Forwarded-For", tt.forwarded)
			}
			if got := getIP(r); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
