package httpserver

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ============================
// JSON responses
// ============================

// writeJSON ghi 1 payload JSON kèm status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// respondError ghi lỗi dạng {"error": "..."} — dùng chung cho mọi handler.
func respondError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// ============================
// Auth helpers
// ============================

// bearerToken lấy token từ header "Authorization: Bearer <token>".
func bearerToken(r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", errors.New("missing Authorization header")
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", errors.New("invalid Authorization header format")
	}
	return strings.TrimSpace(parts[1]), nil
}

// GetUserIDFromRequest verify access token và trả về userID.
func GetUserIDFromRequest(r *http.Request, secret []byte) (int64, error) {
	tokenStr, err := bearerToken(r)
	if err != nil {
		return 0, err
	}

	claims, err := ParseToken(tokenStr, secret)
	if err != nil {
		return 0, errors.New("invalid or expired token")
	}

	return int64(claims.UserID), nil
}

// ============================
// URL / path helpers
// ============================

// getIDFromURL lấy id là segment CUỐI của path.
// Hoạt động với mọi độ sâu route, ví dụ:
//
//	/rooms/messages/{id}
//	/messages/seen/summary/{id}
//	/messages/seen/users/{id}
func getIDFromURL(r *http.Request) (int64, error) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) == 0 {
		return 0, errors.New("invalid URL")
	}

	last := parts[len(parts)-1]
	id, err := strconv.ParseInt(last, 10, 64)
	if err != nil {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

// ============================
// Misc helpers
// ============================

// nsToString trả về giá trị string của sql.NullString (rỗng nếu NULL).
func nsToString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

// formatTime format time theo "2006-01-02 15:04:05" (rỗng nếu zero).
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

// getIP lấy IP client (ưu tiên header reverse proxy nếu có).
func getIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}

	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}
