package httpserver

import (
	"context"
	"cronhustler/api-service/internal/chat"
	"cronhustler/api-service/internal/room"
	"database/sql" // 👈 thêm cái này
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	// ...
)

func (s *Server) mountRoomRoutes(mux *http.ServeMux) {
	// GET /rooms  -> lấy tất cả room mà user (trong token) đang tham gia
	mux.Handle("/rooms", http.HandlerFunc(s.handleGetMyRooms))

	// GET /rooms/{id}/messages
	mux.Handle("/rooms/messages/", http.HandlerFunc(s.handleGetRoomMessages))

	// POST /rooms/direct -> tạo room direct cho 2 user id
	mux.Handle("/rooms/direct/", http.HandlerFunc(s.handleCreateDirectRoom))

	// ✅ GET /rooms/direct-name/{user_id} -> lấy full_name thằng partner (user_id thứ 2)
	mux.Handle("/rooms/direct-name/", http.HandlerFunc(s.handleGetDirectPartnerName))

	// POST /rooms/group -> tạo room group
	mux.Handle("/rooms/group", http.HandlerFunc(s.handleCreateGroupRoom))

	// POST /rooms/members -> thêm user vào room (chỉ member trong room mới được add)
	mux.Handle("/rooms/add-member", http.HandlerFunc(s.handleAddUserToRoom))

	// POST /rooms/read/{id} -> đánh dấu room đã đọc
	mux.Handle("/rooms/read/", http.HandlerFunc(s.handleMarkRoomAsRead))

	// GET /rooms/members/{roomID} -> lấy danh sách thành viên trong room
	mux.Handle("/rooms/members/", http.HandlerFunc(s.handleGetRoomMembers))

	// DELETE /rooms/{roomID}/members/{userID} -> xoá user khỏi group room
	mux.Handle("/rooms/", http.HandlerFunc(s.handleDeleteUserGroup))

	// DELETE /rooms/delete/{roomID} -> xoá room (chỉ owner mới được xoá)
	mux.Handle("/rooms/delete/", http.HandlerFunc(s.handleDeleteRoom))

	// POST /rooms/upload-image/ -> upload hình ảnh trong room chat
	mux.Handle("/rooms/upload-image/", http.HandlerFunc(s.handleUploadRoomImage))

}

// Response cho 1 room
type RoomInfoResponse struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"` // direct | group
	CreatedBy int64  `json:"created_by"`
	IsActive  int    `json:"is_active"`

	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Response cho list room của 1 user
type GetMyRoomsResponse struct {
	Rooms []RoomInfoResponse `json:"rooms,omitempty"`
	Error string             `json:"error,omitempty"`
}

// handleGetMyRooms: trả về danh sách room mà user trong token đang ở
// GET /rooms
// Header: Authorization: Bearer <access_token>
func (s *Server) handleGetMyRooms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		writeJSON(w, http.StatusUnauthorized, GetMyRoomsResponse{
			Error: "missing Authorization header",
		})
		return
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		writeJSON(w, http.StatusUnauthorized, GetMyRoomsResponse{
			Error: "invalid Authorization header",
		})
		return
	}

	tokenStr := parts[1]
	claims, err := ParseToken(tokenStr, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, GetMyRoomsResponse{
			Error: "invalid or expired token",
		})
		return
	}

	userID := int64(claims.UserID)

	rooms, err := s.roomRepo.GetRoomsByUser(userID)
	if err != nil {
		log.Println("GetRoomsByUser error:", err)
		writeJSON(w, http.StatusInternalServerError, GetMyRoomsResponse{
			Error: "db error",
		})
		return
	}

	respRooms := make([]RoomInfoResponse, 0, len(rooms))

	for _, rm := range rooms {
		roomName := rm.Name

		// ✅ OVERRIDE name cho direct room
		if rm.Type == "direct" {
			fullName, err := s.roomRepo.GetDirectPartnerFullNameByRoomID(rm.ID, userID)
			if err == nil && strings.TrimSpace(fullName) != "" {
				roomName = fullName
			} else {
				// fallback an toàn, tránh crash UI
				log.Printf(
					"[GetMyRooms] cannot get partner name for room %d: %v",
					rm.ID, err,
				)
			}
		}

		respRooms = append(respRooms, RoomInfoResponse{
			ID:        rm.ID,
			Name:      roomName,
			Type:      rm.Type,
			CreatedBy: rm.CreatedBy,
			IsActive:  rm.IsActive,
			CreatedAt: formatTime(rm.CreatedAt),
			UpdatedAt: formatTime(rm.UpdatedAt),
		})
	}

	// ✅ HTTP response
	writeJSON(w, http.StatusOK, GetMyRoomsResponse{
		Rooms: respRooms,
	})

	// ✅ WS sync (dùng data đã override name)
	go wsSendToUser(userID, wsEnvelope{
		Type: "rooms_sync",
		Data: map[string]any{
			"rooms": respRooms,
		},
	})
}

// formatTime: helper nhỏ cho đẹp, tránh nil pointer
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

// internal/httpserver/room.go

type ReplyInfoResponse struct {
	MessageID   int64  `json:"message_id"`
	Preview     string `json:"preview,omitempty"`
	SenderName  string `json:"sender_name,omitempty"`
	MessageType string `json:"message_type,omitempty"` // text|image|file|system
}

type RoomMessageResponse struct {
	ID              int64  `json:"id"`
	RoomID          int64  `json:"room_id"`
	SenderID        int64  `json:"sender_id"`
	SenderName      string `json:"sender_name"`
	SenderAvatarURL string `json:"sender_avatar_url,omitempty"`

	Content string `json:"content"`
	Type    string `json:"message_type"`
	IsTemp  int    `json:"is_temp"`

	MediaURL  string `json:"media_url,omitempty"`
	MediaMIME string `json:"media_mime,omitempty"`
	MediaSize int64  `json:"media_size,omitempty"`

	Reply     *ReplyInfoResponse         `json:"reply,omitempty"`
	Reactions []chat.ReactionSummaryItem `json:"reactions,omitempty"`

	CreatedAt string `json:"created_at"`
}

type getRoomMessagesResponse struct {
	Messages []RoomMessageResponse `json:"messages,omitempty"`
	Error    string                `json:"error,omitempty"`
}

func (s *Server) handleGetRoomMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	roomID, err := getIDFromURL(r)
	if err != nil || roomID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid room id"})
		return
	}

	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	// ==========================
	// ✅ Cursor: before_id + before_at (RFC3339)
	// ==========================
	var beforeID int64 = 0
	if v := r.URL.Query().Get("before_id"); v != "" {
		beforeID, _ = strconv.ParseInt(v, 10, 64)
	}

	var beforeAt time.Time
	if v := r.URL.Query().Get("before_at"); v != "" {
		// Expect RFC3339 string (e.g. "2025-12-18T02:11:22+07:00")
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			beforeAt = t
		}
	}

	// ==========================
	// ✅ Authz: must be member
	// ==========================
	isMember, err := s.roomRepo.IsUserInRoom(roomID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if !isMember {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you are not a member of this room"})
		return
	}

	// ==========================
	// ✅ Backward compatible:
	// If FE only sends before_id (old client), we lookup created_at for that id.
	// This avoids skipping day-separators/system messages when sorting by created_at.
	// ==========================
	if beforeID > 0 && beforeAt.IsZero() {
		// optional timeout
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// Get created_at of the cursor message (room_id + id)
		t, e := s.roomRepo.GetMessageCreatedAt(ctx, roomID, beforeID)
		if e == nil && !t.IsZero() {
			beforeAt = t
		}
		// nếu lookup fail thì vẫn chạy tiếp với beforeAt zero (repo sẽ treat as no cursor)
	}

	// ==========================
	// ✅ Get messages (cursor by created_at + id)
	// ==========================
	msgs, err := s.roomRepo.GetRoomMessages(roomID, beforeID, beforeAt, limit, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	// ==========================
	// ✅ NEW: auto mark seen tới message mới nhất user vừa load
	// (giữ logic cũ)
	// ==========================
	var newestID int64 = 0
	if beforeID == 0 {
		for _, m := range msgs {
			if m.ID > newestID {
				newestID = m.ID
			}
		}

		if newestID > 0 {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()

			_ = s.roomRepo.MarkRoomSeenUpTo(ctx, roomID, userID, newestID)

			memberIDs, err := s.roomRepo.GetRoomMemberIDs(roomID)
			if err == nil && len(memberIDs) > 0 {

				briefName := ""
				briefAvatar := ""

				if s.userRepo != nil {
					if u, e := s.userRepo.GetUserBrief(ctx, userID); e == nil && u != nil {
						briefName = u.FullName
						briefAvatar = u.AvatarURL
					}
				}

				env := wsEnvelope{
					Type: "room_seen_update",
					Data: map[string]any{
						"room_id":              roomID,
						"user_id":              userID,
						"full_name":            briefName,
						"avatar_url":           briefAvatar,
						"last_seen_message_id": newestID,
						"last_seen_at":         time.Now().Format(time.RFC3339),
					},
				}

				otherIDs := make([]int64, 0, len(memberIDs))
				for _, uid := range memberIDs {
					if uid != userID {
						otherIDs = append(otherIDs, uid)
					}
				}

				wsSendToUsers(otherIDs, env)
			}
		}
	}

	// ==========================
	// ✅ Response mapping
	// ==========================
	respMsgs := make([]RoomMessageResponse, 0, len(msgs))
	for _, m := range msgs {
		createdAtStr := ""
		if !m.CreatedAt.IsZero() {
			createdAtStr = m.CreatedAt.Format(time.RFC3339)
		}

		var reply *ReplyInfoResponse
		if m.ReplyToMessageID > 0 {
			reply = &ReplyInfoResponse{
				MessageID:   m.ReplyToMessageID,
				Preview:     m.ReplyPreview,
				SenderName:  m.ReplySenderName,
				MessageType: m.ReplyMessageType,
			}
		}

		respMsgs = append(respMsgs, RoomMessageResponse{
			ID:              m.ID,
			RoomID:          m.RoomID,
			SenderID:        m.SenderID,
			SenderName:      m.SenderName,
			SenderAvatarURL: m.SenderAvatarURL,

			Content: m.Content,
			Type:    m.Type,
			IsTemp:  m.IsTemp,

			MediaURL:  m.MediaURL,
			MediaMIME: m.MediaMIME,
			MediaSize: m.MediaSize,

			Reply:     reply,
			Reactions: m.Reactions,

			CreatedAt: createdAtStr,
		})
	}

	writeJSON(w, http.StatusOK, getRoomMessagesResponse{Messages: respMsgs})
}

// Request tạo room direct giữa current user (trong token) và 1 user khác
type CreateDirectRoomRequest struct {
	UserID int64 `json:"user_id"` // user còn lại
}

type CreateDirectRoomResponse struct {
	Room  *RoomInfoResponse `json:"room,omitempty"`
	Error string            `json:"error,omitempty"`
}

func (s *Server) handleCreateDirectRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, CreateDirectRoomResponse{
			Error: "method not allowed",
		})
		return
	}

	// 1. Lấy currentUser từ token
	currentUserID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, CreateDirectRoomResponse{
			Error: err.Error(),
		})
		return
	}

	// 2. Extract targetUserID từ URL: /rooms/direct/{id}
	// cắt prefix "/rooms/direct/"
	path := strings.TrimPrefix(r.URL.Path, "/rooms/direct/")
	path = strings.Trim(path, "/")

	targetID, err := strconv.ParseInt(path, 10, 64)
	if err != nil || targetID <= 0 {
		writeJSON(w, http.StatusBadRequest, CreateDirectRoomResponse{
			Error: "invalid target user id",
		})
		return
	}

	if targetID == currentUserID {
		writeJSON(w, http.StatusBadRequest, CreateDirectRoomResponse{
			Error: "cannot create direct room with yourself",
		})
		return
	}

	// 3. Kiểm tra đã tồn tại direct-room giữa 2 thằng chưa
	existingRoom, err := s.roomRepo.GetDirectRoomBetweenUsers(currentUserID, targetID)
	if err != nil && err != sql.ErrNoRows {
		log.Println("GetDirectRoomBetweenUsers error:", err)
		writeJSON(w, http.StatusInternalServerError, CreateDirectRoomResponse{
			Error: "db error",
		})
		return
	}

	// 3.1 Nếu đã tồn tại → trả lại luôn
	if err == nil && existingRoom != nil {
		resp := &RoomInfoResponse{
			ID:        existingRoom.ID,
			Name:      existingRoom.Name,
			Type:      existingRoom.Type,
			CreatedBy: existingRoom.CreatedBy,
			IsActive:  existingRoom.IsActive,
			CreatedAt: formatTime(existingRoom.CreatedAt),
			UpdatedAt: formatTime(existingRoom.UpdatedAt),
		}
		writeJSON(w, http.StatusOK, CreateDirectRoomResponse{Room: resp})
		return
	}

	// 4. Tạo room mới
	var a, b int64
	if currentUserID < targetID {
		a, b = currentUserID, targetID
	} else {
		a, b = targetID, currentUserID
	}
	roomName := "direct-" + strconv.FormatInt(a, 10) + "-" + strconv.FormatInt(b, 10)

	newRoom := &room.Room{
		Name:      roomName,
		Type:      "direct",
		CreatedBy: currentUserID,
		IsActive:  1,
	}

	roomID, err := s.roomRepo.CreateRoom(newRoom)
	if err != nil {
		log.Println("CreateRoom error:", err)
		writeJSON(w, http.StatusInternalServerError, CreateDirectRoomResponse{
			Error: "db error",
		})
		return
	}

	// 5. Add 2 members
	if err := s.roomRepo.AddMember(roomID, currentUserID, "member"); err != nil {
		log.Println("AddMember current user error:", err)
		writeJSON(w, http.StatusInternalServerError, CreateDirectRoomResponse{
			Error: "db error",
		})
		return
	}
	if err := s.roomRepo.AddMember(roomID, targetID, "member"); err != nil {
		log.Println("AddMember target user error:", err)
		writeJSON(w, http.StatusInternalServerError, CreateDirectRoomResponse{
			Error: "db error",
		})
		return
	}

	// 6. Lấy room lại để trả về
	createdRoom, err := s.roomRepo.GetRoomByID(roomID)
	if err != nil {
		log.Println("GetRoomByID error:", err)
		writeJSON(w, http.StatusInternalServerError, CreateDirectRoomResponse{
			Error: "db error",
		})
		return
	}

	resp := &RoomInfoResponse{
		ID:        createdRoom.ID,
		Name:      createdRoom.Name,
		Type:      createdRoom.Type,
		CreatedBy: createdRoom.CreatedBy,
		IsActive:  createdRoom.IsActive,
		CreatedAt: formatTime(createdRoom.CreatedAt),
		UpdatedAt: formatTime(createdRoom.UpdatedAt),
	}

	writeJSON(w, http.StatusCreated, CreateDirectRoomResponse{
		Room: resp,
	})
}

type GetDirectPartnerNameResponse struct {
	FullName string `json:"full_name,omitempty"`
	Error    string `json:"error,omitempty"`
}

// GET /rooms/direct-name/{room_id}
// Header: Authorization: Bearer <access_token>
func (s *Server) handleGetDirectPartnerName(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, GetDirectPartnerNameResponse{
			Error: "method not allowed",
		})
		return
	}

	// 1. Lấy current user từ token
	currentUserID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, GetDirectPartnerNameResponse{
			Error: err.Error(),
		})
		return
	}

	// 2. Parse room_id từ URL: /rooms/direct-name/{room_id}
	path := strings.TrimPrefix(r.URL.Path, "/rooms/direct-name/")
	path = strings.Trim(path, "/")

	roomID, err := strconv.ParseInt(path, 10, 64)
	if err != nil || roomID <= 0 {
		writeJSON(w, http.StatusBadRequest, GetDirectPartnerNameResponse{
			Error: "invalid room id",
		})
		return
	}

	// 3. Gọi repo: trong room direct, lấy user còn lại (user_id != currentUserID)
	partnerName, err := s.roomRepo.GetDirectPartnerFullNameByRoomID(roomID, currentUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			// room không tồn tại / không phải direct / current user không thuộc room / không tìm được partner
			writeJSON(w, http.StatusNotFound, GetDirectPartnerNameResponse{
				Error: "direct partner not found for this room",
			})
			return
		}

		log.Println("GetDirectPartnerFullNameByRoomID error:", err)
		writeJSON(w, http.StatusInternalServerError, GetDirectPartnerNameResponse{
			Error: "db error",
		})
		return
	}

	// 4. Ok, trả về tên
	writeJSON(w, http.StatusOK, GetDirectPartnerNameResponse{
		FullName: partnerName,
	})
}

type createGroupRoomRequest struct {
	Name      string  `json:"name"`
	MemberIDs []int64 `json:"member_ids"`
}

func (s *Server) handleCreateGroupRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "unauthorized",
		})
		return
	}

	var req createGroupRoomRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid JSON",
		})
		return
	}

	if strings.TrimSpace(req.Name) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "name is required",
		})
		return
	}

	room, err := s.roomRepo.CreateGroupRoom(req.Name, userID, req.MemberIDs)
	if err != nil {
		log.Println("CreateGroupRoom error:", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "db error",
		})
		return
	}

	writeJSON(w, http.StatusOK, room)
}

type addMembersRequest struct {
	RoomID  int64   `json:"room_id"`
	UserIDs []int64 `json:"user_ids"`
}

type addMembersResponse struct {
	Added   []int64 `json:"added,omitempty"`   // user_id add thành công
	Skipped []int64 `json:"skipped,omitempty"` // đã ở trong room / input lỗi
	Error   string  `json:"error,omitempty"`
}

func (s *Server) handleAddUserToRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, addMembersResponse{
			Error: "method not allowed",
		})
		return
	}

	// 1. Lấy current user từ token
	currentUserID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, addMembersResponse{
			Error: err.Error(),
		})
		return
	}

	// 2. Parse body
	var req addMembersRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, addMembersResponse{
			Error: "invalid JSON",
		})
		return
	}

	if req.RoomID <= 0 || len(req.UserIDs) == 0 {
		writeJSON(w, http.StatusBadRequest, addMembersResponse{
			Error: "room_id and user_ids are required",
		})
		return
	}

	// 3. Check current user có trong room không
	isMember, err := s.roomRepo.IsUserInRoom(req.RoomID, currentUserID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusForbidden, addMembersResponse{
				Error: "you are not a member of this room",
			})
			return
		}
		log.Println("IsUserInRoom (current user) error:", err)
		writeJSON(w, http.StatusInternalServerError, addMembersResponse{
			Error: "db error",
		})
		return
	}
	if !isMember {
		writeJSON(w, http.StatusForbidden, addMembersResponse{
			Error: "you are not a member of this room",
		})
		return
	}

	added := make([]int64, 0)
	skipped := make([]int64, 0)

	// 4. Loop qua list user_ids
	for _, uid := range req.UserIDs {
		if uid <= 0 {
			skipped = append(skipped, uid)
			continue
		}

		// tự add chính mình thì bỏ qua
		if uid == currentUserID {
			skipped = append(skipped, uid)
			continue
		}

		// Check target user đã ở trong room chưa
		targetIsMember, err := s.roomRepo.IsUserInRoom(req.RoomID, uid)
		if err != nil && err != sql.ErrNoRows {
			log.Println("IsUserInRoom (target user) error:", err)
			skipped = append(skipped, uid)
			continue
		}
		if targetIsMember {
			skipped = append(skipped, uid)
			continue
		}

		// Thêm member, role default = "member"
		if err := s.roomRepo.AddMember(req.RoomID, uid, "member"); err != nil {
			log.Println("AddMember error:", err)
			skipped = append(skipped, uid)
			continue
		}

		added = append(added, uid)
	}

	// ====== ✅ 5) Realtime emit (sau khi add xong) ======
	if len(added) > 0 {
		// 5.1) Broadcast cho toàn bộ members trong room (owner/current user cũng phải nhận)
		memberIDs, _ := s.roomRepo.GetRoomMemberIDs(req.RoomID)
		// chắc kèo include current user + new users
		memberIDs = append(memberIDs, currentUserID)
		memberIDs = append(memberIDs, added...)

		wsSendToUsers(memberIDs, wsEnvelope{
			Type:   "room.member_added",
			RoomID: req.RoomID,
			Data: map[string]any{
				"user_ids": added,
				"added_by": currentUserID,
			},
		})

		// 5.2) Gửi riêng cho user mới vào: room.joined (để FE add room vào sidebar ngay)
		// (khuyên có) — nếu mày chưa có repo GetRoomByID thì tạm bỏ block này vẫn chạy được
		if room, err := s.roomRepo.GetRoomByID(req.RoomID); err == nil && room != nil {
			for _, uid := range added {
				wsSendToUser(uid, wsEnvelope{
					Type:   "room.joined",
					RoomID: req.RoomID,
					Data: map[string]any{
						"room": room, // full room info cho sidebar
					},
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, addMembersResponse{
		Added:   added,
		Skipped: skipped,
	})
}

func (s *Server) handleMarkRoomAsRead(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// lấy userID từ token (tuỳ m implement middleware)
	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, addMembersResponse{
			Error: err.Error(),
		})
		return
	}

	// parse roomID từ path: /rooms/read/{id}
	path := strings.TrimPrefix(r.URL.Path, "/rooms/read/")
	path = strings.Trim(path, "/")

	roomID, err := strconv.ParseInt(path, 10, 64)
	if err != nil || roomID <= 0 {
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	if err := s.roomRepo.MarkRoomAsRead(roomID, userID); err != nil {
		log.Println("MarkRoomAsRead error:", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status": "ok",
	})
}

// UserInfo đơn giản cho response /rooms/members/{roomID}
type RoomMemberUser struct {
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

type GetRoomMembersResponse struct {
	Members []*room.RoomMember `json:"members"`
}

func (s *Server) handleGetRoomMembers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	// bắt buộc login
	if _, err := GetUserIDFromRequest(r, s.jwtSecret); err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "unauthorized",
		})
		return
	}

	// path kiểu: /rooms/members/{roomID}
	path := r.URL.Path
	prefix := "/rooms/members/"
	if !strings.HasPrefix(path, prefix) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid path",
		})
		return
	}

	roomIDStr := strings.TrimPrefix(path, prefix)
	if roomIDStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "missing room id",
		})
		return
	}

	roomID, err := strconv.ParseInt(roomIDStr, 10, 64)
	if err != nil || roomID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid room id",
		})
		return
	}

	members, err := s.roomRepo.GetRoomMembers(roomID)
	if err != nil {
		log.Printf("GetRoomMembers error: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "db error",
		})
		return
	}

	// ⭐ Bọc lại thành JSON object
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"members": members,
	})
}

// trong package httpserver
func (s *Server) handleDeleteUserGroup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	// ====== 1) Lấy user từ token (để kiểm tra quyền) ======
	requesterID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "unauthorized",
		})
		return
	}

	// ====== 2) Parse roomID & userID từ URL ======
	path := r.URL.Path // /rooms/3/members/10
	prefix := "/rooms/"
	if !strings.HasPrefix(path, prefix) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid path",
		})
		return
	}

	rest := strings.TrimPrefix(path, prefix) // 3/members/10
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[1] != "members" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid path format",
		})
		return
	}

	roomID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid room id",
		})
		return
	}

	targetUserID, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid user id",
		})
		return
	}

	// ====== 3) Check requester có phải owner của group không ======
	ownerID, err := s.roomRepo.GetRoomOwner(roomID)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "cannot retrieve room owner",
		})
		return
	}

	if requesterID != ownerID {
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "only owner can remove members",
		})
		return
	}

	// ====== 4) Không cho owner tự kick chính mình ======
	if targetUserID == ownerID {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "owner cannot remove himself",
		})
		return
	}

	// ====== 5) Gọi repository để xóa ======
	err = s.roomRepo.DeleteUserGroup(roomID, targetUserID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": err.Error(),
		})
		return
	}

	memberIDs, _ := s.roomRepo.GetRoomMemberIDs(roomID)
	memberIDs = append(memberIDs, requesterID)  // đảm bảo owner cũng nhận
	memberIDs = append(memberIDs, targetUserID) // đảm bảo thằng bị kick cũng nhận

	wsSendToUsers(memberIDs, wsEnvelope{
		Type:   "room.member_removed",
		RoomID: roomID,
		Data: map[string]any{
			"user_id":    targetUserID,
			"removed_by": requesterID,
		},
	})
}

// DELETE /rooms/delete/{roomID}
func (s *Server) handleDeleteRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
		})
		return
	}

	// bắt buộc login
	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "unauthorized",
		})
		return
	}

	// path kiểu: /rooms/delete/{roomID}
	path := r.URL.Path
	const prefix = "/rooms/delete/"
	if !strings.HasPrefix(path, prefix) {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid path",
		})
		return
	}

	roomIDStr := strings.TrimPrefix(path, prefix)
	if roomIDStr == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "missing room id",
		})
		return
	}

	roomID, err := strconv.ParseInt(roomIDStr, 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid room id",
		})
		return
	}

	// gọi repo xoá room (group + direct)
	err = s.roomRepo.DeleteRoom(roomID, userID)
	if err != nil {
		msg := err.Error()
		status := http.StatusInternalServerError

		if strings.Contains(msg, "not found") {
			status = http.StatusNotFound
		} else if strings.Contains(msg, "not allowed") || strings.Contains(msg, "not a member") {
			status = http.StatusForbidden
		} else if strings.Contains(msg, "unsupported room type") {
			status = http.StatusBadRequest
		}

		writeJSON(w, status, map[string]string{
			"error": msg,
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"room_id": roomID,
		"message": "room deleted",
	})
}

// DTO chuẩn FE đang dùng
type MessageDTO struct {
	ID              int64  `json:"id"`
	RoomID          int64  `json:"room_id"`
	SenderID        int64  `json:"sender_id"`
	SenderName      string `json:"sender_name"`
	SenderAvatarURL string `json:"sender_avatar_url"`
	Content         string `json:"content"`
	MessageType     string `json:"message_type"`
	CreatedAt       string `json:"created_at"`
}

// POST /rooms/upload-image/{roomID}
// multipart/form-data: file=<image>
func (s *Server) handleUploadRoomImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1) auth
	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	// 2) parse roomID from URL: /rooms/upload-image/{roomID}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) < 3 {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}
	roomIDStr := parts[len(parts)-1]
	roomID, err := strconv.ParseInt(roomIDStr, 10, 64)
	if err != nil || roomID <= 0 {
		http.Error(w, "invalid room id", http.StatusBadRequest)
		return
	}

	// 3) check member
	ok, err := s.roomRepo.IsUserInRoom(roomID, int64(userID))
	if err != nil {
		http.Error(w, "member check failed", http.StatusInternalServerError)
		return
	}
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// 4) parse multipart (limit 10MB)
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

	// 5) sniff mime
	const sniffLen = 512
	head := make([]byte, sniffLen)
	n, _ := file.Read(head)

	// reset stream (seek if possible, else reopen)
	if seeker, ok := file.(io.Seeker); ok {
		_, _ = seeker.Seek(0, io.SeekStart)
	} else {
		_ = file.Close()
		file, header, err = r.FormFile("file")
		if err != nil {
			http.Error(w, "file read error", http.StatusBadRequest)
			return
		}
		defer file.Close()
	}

	mime := http.DetectContentType(head[:n])
	if !isAllowedImageMime(mime) {
		http.Error(w, "unsupported image type", http.StatusBadRequest)
		return
	}

	// 6) ensure upload dir exists
	if err := os.MkdirAll(s.chatUploadDir, 0o755); err != nil {
		http.Error(w, "cannot create upload dir", http.StatusInternalServerError)
		return
	}

	// 7) filename
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext == "" {
		ext = mimeToExt(mime)
	}
	filename := fmt.Sprintf("r%d_u%d_%d%s", roomID, userID, time.Now().UnixNano(), ext)
	fullPath := filepath.Join(s.chatUploadDir, filename)

	out, err := os.Create(fullPath)
	if err != nil {
		http.Error(w, "cannot save file", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	if _, err = io.Copy(out, file); err != nil {
		_ = os.Remove(fullPath)
		http.Error(w, "save file error", http.StatusInternalServerError)
		return
	}

	// 8) media url (FE sẽ dùng url này để insert message)
	mediaURL := "/static/chat_uploads/" + filename

	// 9) return json
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":        true,
		"room_id":   roomID,
		"media_url": mediaURL,
		"filename":  filename,
		"mime":      mime,
		"size":      header.Size,
	})
}

func isAllowedImageMime(m string) bool {
	switch strings.ToLower(m) {
	case "image/jpeg", "image/jpg", "image/png", "image/webp", "image/gif":
		return true
	default:
		return false
	}
}

func mimeToExt(m string) string {
	switch strings.ToLower(m) {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".jpg"
	}
}
