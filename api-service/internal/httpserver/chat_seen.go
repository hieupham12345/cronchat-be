package httpserver

import (
	"context"
	"cronhustler/api-service/internal/chat"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ===== Receipts (Seen) =====

type markSeenRequest struct {
	RoomID      int64 `json:"room_id"`
	UpToMessage int64 `json:"up_to_message_id"`
}

type markSeenResponse struct {
	RoomID          int64  `json:"room_id"`
	UpToMessageID   int64  `json:"up_to_message_id"`
	Affected        int64  `json:"affected"`
	LastSeenMessage int64  `json:"last_seen_message_id"`
	LastSeenAt      string `json:"last_seen_at,omitempty"`

	Room roomLiteResponse `json:"room"` // ✅ value, không phải pointer
}

type roomLiteResponse struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Name      string `json:"name"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type roomLastSeenResponse struct {
	RoomID            int64  `json:"room_id"`
	UserID            int64  `json:"user_id"`
	LastSeenMessageID int64  `json:"last_seen_message_id"`
	LastSeenAt        string `json:"last_seen_at,omitempty"`
}

type messageSeenSummaryResponse struct {
	MessageID int64 `json:"message_id"`
	SeenCount int64 `json:"seen_count"`
	SeenByMe  bool  `json:"seen_by_me"`
}

type listSeenUsersResponse struct {
	MessageID int64           `json:"message_id"`
	Users     []chat.SeenUser `json:"users"`
}

// =======================================
// HANDLER: POST /rooms/seen
// =======================================

func (s *Server) handleMarkRoomSeenUpTo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	var req markSeenRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}
	if req.RoomID <= 0 || req.UpToMessage <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "room_id and up_to_message_id are required"})
		return
	}

	// membership
	isMember, err := s.roomRepo.IsUserInRoom(req.RoomID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !isMember {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a room member"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	affected, err := s.chatRepo.MarkRoomSeenUpTo(ctx, req.RoomID, userID, req.UpToMessage)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// ✅ Đồng bộ bookkeeping unread: unread count đọc room_members.last_seen_at,
	// nên phải update luôn ở đây (nếu không unread sẽ không giảm sau khi seen).
	if err := s.roomRepo.MarkRoomSeenUpTo(ctx, req.RoomID, userID, req.UpToMessage); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	lastMsgID, lastAt, err := s.chatRepo.GetRoomLastSeenMessageID(ctx, req.RoomID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// ✅ Lấy info room để trả kèm response
	room, err := s.roomRepo.GetRoomByIDLite(ctx, req.RoomID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	// ✅ Decorate name theo rule direct/group (KHÔNG update DB)
	displayName := room.Name
	if strings.EqualFold(room.Type, "direct") {
		if partnerName, err := s.roomRepo.GetDirectPartnerFullNameByRoomID(req.RoomID, userID); err == nil && strings.TrimSpace(partnerName) != "" {
			displayName = partnerName
		}
	}

	lastSeenAtStr := ""
	if lastAt != nil {
		lastSeenAtStr = lastAt.Format(time.RFC3339)
	}

	resp := markSeenResponse{
		RoomID:          req.RoomID,
		UpToMessageID:   req.UpToMessage,
		Affected:        affected,
		LastSeenMessage: lastMsgID,
		LastSeenAt:      lastSeenAtStr,
		Room: roomLiteResponse{
			ID:        room.ID,
			Type:      room.Type,
			Name:      displayName,
			UpdatedAt: room.UpdatedAt.Format(time.RFC3339),
		},
	}

	// respond first
	writeJSON(w, http.StatusOK, resp)

	// realtime (style đồng bộ)
	memberIDs, err := s.roomRepo.GetRoomMemberIDs(req.RoomID)
	if err != nil {
		log.Println("GetRoomMemberIDs error:", err)
		return
	}

	// (A) room_seen_update: update state seen trong room
	go wsSendToUsers(memberIDs, wsEnvelope{
		Type:   "room_seen_update",
		RoomID: req.RoomID,
		Data: map[string]any{
			"user_id":              userID,
			"last_seen_message_id": lastMsgID,
			"last_seen_at":         lastSeenAtStr,
			"up_to_message_id":     req.UpToMessage,
			// ✅ kèm room để FE tiện sync state nếu muốn
			"room": map[string]any{
				"id":         room.ID,
				"type":       room.Type,
				"name":       displayName,
				"updated_at": room.UpdatedAt,
			},
		},
	})

	// // (B) room_updated: nếu sidebar mày gom về room_updated thì nhét seen_update vào đây
	// go wsSendToUsers(memberIDs, wsEnvelope{
	// 	Type:   "room_updated",
	// 	RoomID: req.RoomID,
	// 	Data: map[string]any{
	// 		"seen_update": map[string]any{
	// 			"user_id":              userID,
	// 			"last_seen_message_id": lastMsgID,
	// 			"last_seen_at":         lastSeenAtStr,
	// 		},
	// 		// ✅ kèm room (name đã decorate theo direct/group)
	// 		"room": map[string]any{
	// 			"id":         room.ID,
	// 			"type":       room.Type,
	// 			"name":       displayName,
	// 			"updated_at": room.UpdatedAt,
	// 		},
	// 	},
	// })
}

// =======================================
// HANDLER: GET /rooms/last-seen/{roomID}
// =======================================

func (s *Server) handleGetRoomLastSeen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	roomID, err := getIDFromURL(r) // expects /rooms/last-seen/{roomID}
	if err != nil || roomID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid room id"})
		return
	}

	isMember, err := s.roomRepo.IsUserInRoom(roomID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !isMember {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a room member"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	lastMsgID, lastAt, err := s.chatRepo.GetRoomLastSeenMessageID(ctx, roomID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	resp := roomLastSeenResponse{
		RoomID:            roomID,
		UserID:            userID,
		LastSeenMessageID: lastMsgID,
	}
	if lastAt != nil {
		resp.LastSeenAt = lastAt.Format(time.RFC3339)
	}

	writeJSON(w, http.StatusOK, resp)
}

// =======================================
// HANDLER: GET /messages/seen/summary/{messageID}
// =======================================

func (s *Server) handleGetMessageSeenSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	messageID, err := getIDFromURL(r) // expects /messages/seen/summary/{messageID}
	if err != nil || messageID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid message id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	roomID, senderID, err := s.chatRepo.GetMessageRoomAndSender(ctx, messageID)
	if err != nil {
		if errors.Is(err, chat.ErrMessageNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "message not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	isMember, err := s.roomRepo.IsUserInRoom(roomID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !isMember {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a room member"})
		return
	}

	sum, err := s.chatRepo.GetMessageSeenSummary(ctx, messageID, userID, senderID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, messageSeenSummaryResponse{
		MessageID: sum.MessageID,
		SeenCount: sum.SeenCount,
		SeenByMe:  sum.SeenByMe,
	})
}

// =======================================
// HANDLER: GET /messages/seen/users/{messageID}?limit=50
// =======================================

func (s *Server) handleListSeenUsersByMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	messageID, err := getIDFromURL(r) // expects /messages/seen/users/{messageID}
	if err != nil || messageID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid message id"})
		return
	}

	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	roomID, _, err := s.chatRepo.GetMessageRoomAndSender(ctx, messageID)
	if err != nil {
		if errors.Is(err, chat.ErrMessageNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "message not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	isMember, err := s.roomRepo.IsUserInRoom(roomID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if !isMember {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "not a room member"})
		return
	}

	users, err := s.chatRepo.ListSeenUsersByMessage(ctx, messageID, userID, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, listSeenUsersResponse{
		MessageID: messageID,
		Users:     users,
	})
}
