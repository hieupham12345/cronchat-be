package httpserver

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// middleware type cho tiện chain
type Middleware func(http.Handler) http.Handler

// LoggerMiddleware: log request
func LoggerMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("▶ %s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
		log.Printf("▲ done %s %s in %v", r.Method, r.URL.Path, time.Since(start))
	})
}

// Middleware yêu cầu role = admin
func (s *Server) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		// Lấy header Authorization
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing Authorization header"})
			return
		}

		// Expect: "Bearer <token>"
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token format"})
			return
		}

		tokenStr := parts[1]

		// Parse token
		claims, err := ParseToken(tokenStr, s.jwtSecret)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid token"})
			return
		}

		// Chỉ chấp nhận access token
		if claims.TokenType != TokenTypeAccess {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "access token required"})
			return
		}

		// Check role
		if claims.Role != "admin" {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "admin required"})
			return
		}

		// Pass xuống handler
		next.ServeHTTP(w, r)
	})
}

func WithCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		origin := r.Header.Get("Origin")
		if origin != "" {
			// Cho phép TẤT CẢ origin (echo lại origin),
			// vẫn dùng được với cookie (credentials)
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Credentials", "true")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// Preflight
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
