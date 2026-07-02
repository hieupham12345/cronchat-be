package httpserver

import (
	"cronhustler/api-service/internal/room"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
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

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, GetMyRoomsResponse{
			Error: err.Error(),
		})
		return
	}

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
