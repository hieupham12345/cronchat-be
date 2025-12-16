package httpserver

import (
	"cronhustler/api-service/internal/user" // dùng model User của m, KHÔNG phải os/user
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type createUserRequest struct {
	Username  string `json:"username"`
	Password  string `json:"password"`
	Role      string `json:"role"`      // admin / user
	Full_name string `json:"full_name"` // gửi lên vẫn là full_name
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	AvatarURL string `json:"avatar_url"`
}

type createUserResponse struct {
	ID       int64  `json:"id,omitempty"`
	Username string `json:"username,omitempty"`
	Role     string `json:"role,omitempty"`
	Message  string `json:"message,omitempty"` // 👈 thêm field này
	Error    string `json:"error,omitempty"`
}

type UserInfoResponse struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	FullName  string `json:"full_name"`
	Email     string `json:"email"`
	Phone     string `json:"phone"`
	AvatarURL string `json:"avatar_url"`
	LastLogin string `json:"last_login"`
	LoginIP   string `json:"login_ip"`
	CreatedIP string `json:"created_ip"`
}

type getAllUserResponse struct {
	Users []UserInfoResponse `json:"users"`
	Error string             `json:"error,omitempty"`
}

type getAllUserForListingResponse struct {
	Users []UserInfoResponse `json:"users"`
	Error string             `json:"error,omitempty"`
}

type updateUserRequest struct {
	Password  *string `json:"password"`  // nil = không update, != nil = update (có thể là "")
	FullName  *string `json:"full_name"` // tương tự
	Email     *string `json:"email"`
	Phone     *string `json:"phone"`
	AvatarURL *string `json:"avatar_url"`
	IsActive  *int    `json:"is_active"` // nil = không update, 0/1 = update
}

type updateUserResponse struct {
	Success bool   `json:"success,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) mountUserRoutes(mux *http.ServeMux) {
	mux.Handle("/create-user", http.HandlerFunc(s.handleCreateUser))
	mux.Handle("/me", http.HandlerFunc(s.handleGetUserInfo))
	mux.Handle("/admin/get-all-user", s.RequireAdmin(http.HandlerFunc(s.handleGetAllUser)))
	mux.Handle("/update-user", http.HandlerFunc(s.handleUpdateUser))
	mux.Handle("/get-all-user-listing", http.HandlerFunc(s.handleGetAllUserForListing))
	mux.Handle("/users/search", http.HandlerFunc(s.handleSearchUsers))
	mux.Handle("/users/avatar", http.HandlerFunc(s.handleUploadAvatar))
	mux.Handle("/update-password", http.HandlerFunc(s.handleChangePassword))

}

func getIDFromURL(r *http.Request) (int64, error) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	// parts = ["admin", "users", "{id}"]
	if len(parts) < 3 {
		return 0, errors.New("invalid URL")
	}

	id, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, errors.New("invalid id")
	}

	return id, nil
}

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)
var phoneRegex = regexp.MustCompile(`^0[0-9]{9}$`)

func isValidEmail(email string) bool {
	return emailRegex.MatchString(email)
}

func isValidPhone(phone string) bool {
	return phoneRegex.MatchString(phone)
}

// getIP: lấy IP client (ưu tiên header reverse proxy nếu có)
func getIP(r *http.Request) string {
	// X-Real-IP do nginx/nginx-proxy set
	ip := r.Header.Get("X-Real-IP")
	if ip != "" {
		return ip
	}

	// X-Forwarded-For: lấy IP đầu tiên
	xff := r.Header.Get("X-Forwarded-For")
	if xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}

	// fallback: RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

// Trả về userID (int64) hoặc lỗi
func GetUserIDFromRequest(r *http.Request, secret []byte) (int64, error) {
	// Lấy Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return 0, errors.New("missing Authorization header")
	}

	// Expect: "Bearer <token>"
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return 0, errors.New("invalid Authorization header format")
	}

	tokenStr := parts[1]

	// Parse token
	claims, err := ParseToken(tokenStr, secret)
	if err != nil {
		return 0, errors.New("invalid or expired token")
	}

	return int64(claims.UserID), nil
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	var req createUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, createUserResponse{
			Error:   "INVALID_JSON",
			Message: "Invalid JSON body",
		})
		return
	}

	// trim mấy field string cho chắc
	req.Username = strings.TrimSpace(req.Username)
	req.Password = strings.TrimSpace(req.Password)
	req.Role = strings.TrimSpace(req.Role)
	req.Email = strings.TrimSpace(req.Email)
	req.Phone = strings.TrimSpace(req.Phone)

	if req.Username == "" || req.Password == "" || req.Role == "" {
		writeJSON(w, http.StatusBadRequest, createUserResponse{
			Error:   "MISSING_FIELDS",
			Message: "Missing required fields",
		})
		return
	}

	// Check role hợp lệ
	if req.Role != "admin" && req.Role != "user" {
		writeJSON(w, http.StatusBadRequest, createUserResponse{
			Error:   "INVALID_ROLE",
			Message: "Role must be admin or user",
		})
		return
	}

	// 👇 NEW: Validate password length
	if len(req.Password) < 8 {
		writeJSON(w, http.StatusBadRequest, createUserResponse{
			Error:   "WEAK_PASSWORD",
			Message: "Password must be at least 8 characters",
		})
		return
	}

	// Validate email format
	if req.Email == "" || !isValidEmail(req.Email) {
		writeJSON(w, http.StatusBadRequest, createUserResponse{
			Error:   "INVALID_EMAIL",
			Message: "Invalid email format",
		})
		return
	}

	// Validate phone format
	if req.Phone == "" || !isValidPhone(req.Phone) {
		writeJSON(w, http.StatusBadRequest, createUserResponse{
			Error:   "INVALID_PHONE",
			Message: "Invalid phone number",
		})
		return
	}

	// Hash password
	hashed := hashPassword(req.Password)

	// Lấy IP từ request
	ip := getIP(r)
	u := &user.User{
		Username: req.Username,
		Password: hashed,
		Role:     req.Role,

		Full_name: sql.NullString{String: req.Full_name, Valid: req.Full_name != ""},
		Email:     sql.NullString{String: req.Email, Valid: req.Email != ""},
		Phone:     sql.NullString{String: req.Phone, Valid: req.Phone != ""},
		AvatarURL: sql.NullString{String: req.AvatarURL, Valid: req.AvatarURL != ""},

		Is_active: 1,

		Created_ip: sql.NullString{String: ip, Valid: ip != ""},
	}

	// Insert DB
	id, err := s.userRepo.CreateUser(u)
	if err != nil {
		// SQLite duplicate username thường trả lỗi chứa "UNIQUE"
		if strings.Contains(err.Error(), "UNIQUE") {
			writeJSON(w, http.StatusConflict, createUserResponse{
				Error:   "USERNAME_EXISTS",
				Message: "Username already exists",
			})
			return
		}

		log.Println("create user error:", err)
		writeJSON(w, http.StatusInternalServerError, createUserResponse{
			Error:   "DB_ERROR",
			Message: "Database error",
		})
		return
	}
	// Trả về
	writeJSON(w, http.StatusOK, createUserResponse{
		ID:       id,
		Username: req.Username,
		Role:     req.Role,
		Message:  "USER_CREATED", // 👈 FE bắt cái này là ok
	})
}

func (s *Server) handleGetUserInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Lấy userID từ token
	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": err.Error(),
		})
		return
	}

	// Lấy user từ DB
	u, err := s.userRepo.GetUserByID(int(userID))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"error": "user not found",
			})
			return
		}
		log.Printf("GetUserByID error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "db error",
		})
		return
	}

	resp := UserInfoResponse{
		ID:        int64(u.ID),
		Username:  u.Username,
		Role:      u.Role,
		FullName:  nsToString(u.Full_name),
		Email:     nsToString(u.Email),
		Phone:     nsToString(u.Phone),
		AvatarURL: nsToString(u.AvatarURL),
		LastLogin: nsToString(u.Last_login),
		LoginIP:   nsToString(u.Login_ip),
		CreatedIP: nsToString(u.Created_ip),
	}

	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleGetAllUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	users, err := s.userRepo.GetAllUsers()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, getAllUserResponse{
			Error: "db error",
		})
		return
	}

	// Map []*user.User -> []UserInfoResponse
	respUsers := make([]UserInfoResponse, 0, len(users))
	for _, u := range users {
		respUsers = append(respUsers, UserInfoResponse{
			ID:        int64(u.ID),
			Username:  u.Username,
			Role:      u.Role,
			FullName:  nsToString(u.Full_name),
			Email:     nsToString(u.Email),
			Phone:     nsToString(u.Phone),
			AvatarURL: nsToString(u.AvatarURL),
			LastLogin: nsToString(u.Last_login),
			LoginIP:   nsToString(u.Login_ip),
			CreatedIP: nsToString(u.Created_ip),
		})
	}

	writeJSON(w, http.StatusOK, getAllUserResponse{
		Users: respUsers,
	})
}

func (s *Server) handleGetAllUserForListing(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// 1. Query DB lấy danh sách users
	users, err := s.userRepo.GetAllUsersForListing()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, getAllUserResponse{
			Error: "db error",
		})
		return
	}

	// 2. Lấy userID từ token
	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": err.Error(),
		})
		return
	}

	// 3. Kiểm tra userID token có tồn tại trong DB không
	isValidUser := false
	for _, u := range users {
		if int64(u.ID) == userID {
			isValidUser = true
			break
		}
	}

	if !isValidUser {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "invalid user or user not exist",
		})
		return
	}

	// 4. Map kết quả trả về
	respUsers := make([]UserInfoResponse, 0, len(users))
	for _, u := range users {
		respUsers = append(respUsers, UserInfoResponse{
			ID:        int64(u.ID),
			Username:  u.Username,
			FullName:  nsToString(u.Full_name),
			AvatarURL: nsToString(u.AvatarURL),
		})
	}

	writeJSON(w, http.StatusOK, getAllUserResponse{
		Users: respUsers,
	})
}

// dùng chung cho nhiều handler
func (s *Server) applyUserUpdate(id int64, req updateUserRequest) error {
	fields := make(map[string]interface{})

	// nếu gửi password -> hash và update
	if req.Password != nil {
		hashed := hashPassword(*req.Password)
		fields["password"] = hashed
	}

	if req.FullName != nil {
		fields["full_name"] = *req.FullName
	}
	if req.Email != nil {
		fields["email"] = *req.Email
	}
	if req.Phone != nil {
		fields["phone"] = *req.Phone
	}
	if req.AvatarURL != nil {
		fields["avatar_url"] = *req.AvatarURL
	}
	if req.IsActive != nil {
		fields["is_active"] = *req.IsActive
	}

	if len(fields) == 0 {
		return fmt.Errorf("no fields to update")
	}

	return s.userRepo.UpdateUserDynamic(id, fields)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, updateUserResponse{Error: "method not allowed"})
		return
	}

	// Lấy userID từ token
	id, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": err.Error(),
		})
		return
	}

	// parse JSON body
	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, updateUserResponse{Error: "invalid JSON"})
		return
	}

	// gọi hàm chung
	if err := s.applyUserUpdate(id, req); err != nil {
		if err.Error() == "no fields to update" {
			writeJSON(w, http.StatusBadRequest, updateUserResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusInternalServerError, updateUserResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, updateUserResponse{Success: true})
}

// handleSearchUsers: search theo username / full_name, dùng cho gợi ý real-time
func (s *Server) handleSearchUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Lấy keyword từ query ?q=
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		// Gõ < 2 ký tự thì trả mảng rỗng, tránh spam DB
		writeJSON(w, http.StatusOK, getAllUserResponse{
			Users: []UserInfoResponse{},
		})
		return
	}

	// Optional: limit từ query ?limit=
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}

	// BẮT BUỘC login (có token) mới được search
	_, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": err.Error(),
		})
		return
	}

	// Query DB
	users, err := s.userRepo.SearchUsers(q, limit)
	if err != nil {
		log.Printf("SearchUsers error: %v", err)
		writeJSON(w, http.StatusInternalServerError, getAllUserResponse{
			Error: "db error",
		})
		return
	}

	// Map sang UserInfoResponse (chỉ dùng field cần thiết)
	respUsers := make([]UserInfoResponse, 0, len(users))
	for _, u := range users {
		respUsers = append(respUsers, UserInfoResponse{
			ID:        int64(u.ID),
			Username:  u.Username,
			FullName:  nsToString(u.Full_name),
			AvatarURL: nsToString(u.AvatarURL),
			// các field khác để trống
		})
	}

	writeJSON(w, http.StatusOK, getAllUserResponse{
		Users: respUsers,
	})
}

func (s *Server) handleUploadAvatar(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Lấy userID từ token
	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": err.Error(),
		})
		return
	}

	// 📦 Parse multipart form (max 10MB)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "cannot parse form", http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// đảm bảo avatarDir tồn tại (phòng khi vì lý do gì bị xóa)
	if err := os.MkdirAll(s.avatarDir, 0o755); err != nil {
		http.Error(w, "cannot create avatar dir", http.StatusInternalServerError)
		return
	}

	// 🧾 Tên file: u<id>_<timestamp>.ext
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = ".jpg"
	}
	filename := fmt.Sprintf("u%d_%d%s", userID, time.Now().UnixNano(), ext)

	// full path trên ổ đĩa (đã được mount bằng volume)
	fullPath := filepath.Join(s.avatarDir, filename)

	out, err := os.Create(fullPath)
	if err != nil {
		http.Error(w, "cannot save file", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		http.Error(w, "save file error", http.StatusInternalServerError)
		return
	}

	// 🌐 URL để FE load
	// Giả sử bên Server mount static như:
	//   /static/user_avatars/ -> http.Dir(s.avatarDir)
	avatarURL := "/static/user_avatars/" + filename

	// 💾 Update DB
	if err := s.userRepo.UpdateAvatar(int(userID), avatarURL); err != nil {
		http.Error(w, "db update failed", http.StatusInternalServerError)
		return
	}

	// 🔙 Trả JSON
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":    true,
		"avatar_url": avatarURL,
	})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		writeJSON(w, http.StatusMethodNotAllowed, updateUserResponse{Error: "method not allowed"})
		return
	}

	// Lấy userID từ token
	id, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": err.Error(),
		})
		return
	}

	// parse body
	var req changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, updateUserResponse{Error: "invalid JSON"})
		return
	}

	req.CurrentPassword = strings.TrimSpace(req.CurrentPassword)
	req.NewPassword = strings.TrimSpace(req.NewPassword)

	if req.CurrentPassword == "" || req.NewPassword == "" {
		writeJSON(w, http.StatusBadRequest, updateUserResponse{Error: "current_password and new_password are required"})
		return
	}

	// lấy user hiện tại từ DB để check mật khẩu cũ
	u, err := s.userRepo.GetUserByID(int(id))
	if err != nil {
		// tùy repo của bro trả gì, tạm cho 404 / 500
		writeJSON(w, http.StatusInternalServerError, updateUserResponse{Error: "user not found"})
		return
	}

	// verify mật khẩu cũ
	if hashPassword(req.CurrentPassword) != u.Password {
		writeJSON(w, http.StatusBadRequest, updateUserResponse{Error: "current password is incorrect"})
		return
	}

	// build updateUserRequest chỉ có Password
	newPass := req.NewPassword
	updateReq := updateUserRequest{
		Password: &newPass,
	}

	// gọi lại logic chung giống handleUpdateUser
	if err := s.applyUserUpdate(id, updateReq); err != nil {
		writeJSON(w, http.StatusInternalServerError, updateUserResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, updateUserResponse{Success: true})
}
