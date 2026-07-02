package httpserver

import (
	"context"
	"cronhustler/api-service/internal/chat"
	"net/http"
	"strconv"
	"time"
)

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
