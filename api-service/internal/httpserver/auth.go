package httpserver

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

func nsToString(ns sql.NullString) string {
	if ns.Valid {
		return ns.String
	}
	return ""
}

func (s *Server) mountAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout) // 👈 thêm nè

	mux.HandleFunc("/auth/refresh", s.handleRefreshToken)
	// nếu muốn logout xoá cookie thì thêm:
	// mux.HandleFunc("/logout", s.handleLogout)
}

// hàm tiện ích để hash password
func hashPassword(pw string) string {
	h := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(h[:])
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	ID          int64  `json:"id,omitempty"`
	Username    string `json:"username,omitempty"`
	Full_Name   string `json:"full_name,omitempty"`
	Email       string `json:"email,omitempty"`
	Phone       string `json:"phone,omitempty"`
	AvatarURL   string `json:"avatar_url,omitempty"`
	Role        string `json:"role,omitempty"`
	LastLogin   string `json:"last_login,omitempty"`
	LoginIP     string `json:"login_ip,omitempty"`
	CreatedIp   string `json:"created_ip,omitempty"`
	AccessToken string `json:"accessToken,omitempty"` // access token trả về cho FE (lưu RAM)
	Error       string `json:"error,omitempty"`
}

type refreshResponse struct {
	AccessToken string `json:"accessToken,omitempty"`
	Error       string `json:"error,omitempty"`
}

// ⚠️ đổi đúng tên cookie refresh của mày nếu khác
const RefreshCookieName = "refresh_token"

// Verify refresh token từ cookie cho WebSocket
func (s *Server) VerifyWSAuth(r *http.Request) (int64, error) {
	// 1) lấy refresh token từ cookie
	c, err := r.Cookie(RefreshCookieName)
	if err != nil {
		return 0, errors.New("missing refresh cookie")
	}

	refreshToken := strings.TrimSpace(c.Value)
	if refreshToken == "" {
		return 0, errors.New("empty refresh cookie")
	}

	// 2) parse + verify JWT
	claims, err := ParseToken(refreshToken, []byte(s.jwtSecret))
	if err != nil {
		return 0, err
	}

	// 3) check token type
	if claims.TokenType != TokenTypeRefresh {
		return 0, errors.New("invalid token type for ws")
	}

	// 4) OK
	return int64(claims.UserID), nil
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, loginResponse{Error: "invalid JSON"})
		return
	}
	if req.Username == "" || req.Password == "" {
		writeJSON(w, http.StatusBadRequest, loginResponse{Error: "username/password required"})
		return
	}

	u, err := s.userRepo.FindByUsername(req.Username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusUnauthorized, loginResponse{Error: "invalid credentials"})
			return
		}
		log.Println("db error:", err)
		writeJSON(w, http.StatusInternalServerError, loginResponse{Error: "internal error"})
		return
	}

	// 🚫 Check user disabled
	if u.Is_active == 0 {
		writeJSON(w, http.StatusForbidden, loginResponse{
			Error: "account is locked or disabled",
		})
		return
	}

	// Hash input password
	hashedInput := hashPassword(req.Password)
	if u.Password != hashedInput {
		writeJSON(w, http.StatusUnauthorized, loginResponse{Error: "invalid credentials"})
		return
	}

	// Lấy IP request
	ip := getIP(r)
	loginTime := time.Now().Format("2006-01-02 15:04:05")

	// 🔥 Update login IP + last_login
	if err := s.userRepo.UpdateLoginAudit(u.Username, ip, loginTime); err != nil {
		log.Println("update login audit error:", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "failed to update login info",
		})
		return
	}

	// Tạo tokens
	accessToken, err := GenerateAccessToken(int(u.ID), u.Username, u.Role, s.jwtSecret)
	if err != nil {
		log.Println("jwt error:", err)
		writeJSON(w, http.StatusInternalServerError, loginResponse{Error: "cannot generate access token"})
		return
	}

	refreshToken, err := GenerateRefreshToken(int(u.ID), u.Username, s.jwtSecret)
	if err != nil {
		log.Println("jwt error:", err)
		writeJSON(w, http.StatusInternalServerError, loginResponse{Error: "cannot generate refresh token"})
		return
	}

	// 👉 Set refresh token vào HttpOnly cookie
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    refreshToken,
		Path:     "/", // scope cho toàn API
		HttpOnly: true,
		Secure:   false, // Để true khi chạy HTTPS
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(RefreshTokenTTL),
	})

	// 👉 Gửi response FULL DATA nhưng KHÔNG gửi refreshToken nữa
	writeJSON(w, http.StatusOK, loginResponse{
		ID:          int64(u.ID),
		Username:    u.Username,
		Full_Name:   nsToString(u.Full_name),
		Email:       nsToString(u.Email),
		Phone:       nsToString(u.Phone),
		AvatarURL:   nsToString(u.AvatarURL),
		Role:        u.Role,
		LastLogin:   nsToString(u.Last_login),
		LoginIP:     nsToString(u.Login_ip),
		CreatedIp:   nsToString(u.Created_ip),
		AccessToken: accessToken,
	})
}

// POST /auth/refresh
// FE sẽ gọi endpoint này (kèm credentials) để xin accessToken mới
func (s *Server) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 👉 Lấy refresh_token từ cookie
	cookie, err := r.Cookie("refresh_token")
	if err != nil || cookie.Value == "" {
		writeJSON(w, http.StatusUnauthorized, refreshResponse{
			Error: "missing refresh token",
		})
		return
	}

	refreshToken := cookie.Value

	// 👉 Parse + verify JWT refresh
	claims, err := ParseToken(refreshToken, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, refreshResponse{
			Error: "invalid refresh token",
		})
		return
	}

	// đảm bảo đúng loại token
	if claims.TokenType != TokenTypeRefresh {
		writeJSON(w, http.StatusUnauthorized, refreshResponse{
			Error: "invalid token type",
		})
		return
	}

	// 👉 Generate access token mới
	accessToken, err := GenerateAccessToken(claims.UserID, claims.Username, claims.Role, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, refreshResponse{
			Error: "cannot generate access token",
		})
		return
	}

	// (tuỳ chọn) Rotate refresh token (an toàn hơn):
	// newRefresh, err := GenerateRefreshToken(claims.UserID, claims.Username, s.jwtSecret)
	// if err == nil {
	// 	http.SetCookie(w, &http.Cookie{
	// 		Name:     "refresh_token",
	// 		Value:    newRefresh,
	// 		Path:     "/",
	// 		HttpOnly: true,
	// 		Secure:   false,
	// 		SameSite: http.SameSiteLaxMode,
	// 		Expires:  time.Now().Add(RefreshTokenTTL),
	// 	})
	// }

	writeJSON(w, http.StatusOK, refreshResponse{
		AccessToken: accessToken,
	})
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Set cookie refresh_token hết hạn → xoá
	http.SetCookie(w, &http.Cookie{
		Name:     "refresh_token",
		Value:    "",
		Expires:  time.Unix(0, 0), // Hết hạn
		MaxAge:   -1,              // Xoá liền
		Path:     "/",
		HttpOnly: true,
		Secure:   false,
		SameSite: http.SameSiteLaxMode,
	})

	writeJSON(w, http.StatusOK, map[string]string{
		"message": "logged out",
	})
}
