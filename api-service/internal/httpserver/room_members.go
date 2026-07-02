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
