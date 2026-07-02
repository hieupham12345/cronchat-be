package httpserver

import (
	"context"
	"cronhustler/api-service/internal/chat"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"
)

// =======================================
// ROUTES
// =======================================

func (s *Server) mountChatRoutes(mux *http.ServeMux) {
	// messages
	mux.Handle("/rooms/send-messages/", http.HandlerFunc(s.handleSendMessage))

	// reactions
	mux.Handle("/messages/react/add", http.HandlerFunc(s.handleToggleReaction))      // POST (toggle)
	mux.Handle("/messages/react/remove", http.HandlerFunc(s.handleRemoveReaction))   // POST (force remove)
	mux.Handle("/messages/reactions/", http.HandlerFunc(s.handleGetReactionSummary)) // GET /messages/reactions/{messageID}

	// receipts (seen)
	mux.Handle("/rooms/seen", http.HandlerFunc(s.handleMarkRoomSeenUpTo))                  // POST
	mux.Handle("/rooms/last-seen/", http.HandlerFunc(s.handleGetRoomLastSeen))             // GET /rooms/last-seen/{roomID}
	mux.Handle("/messages/seen/summary/", http.HandlerFunc(s.handleGetMessageSeenSummary)) // GET /messages/seen/summary/{messageID}
	mux.Handle("/messages/seen/users/", http.HandlerFunc(s.handleListSeenUsersByMessage))  // GET /messages/seen/users/{messageID}?limit=50
	// unread
	// ✅ notifications / unread
	mux.Handle("/rooms/unread-counts", http.HandlerFunc(s.handleGetUnreadCountsByRooms)) // GET
	mux.Handle("/rooms/unread/", http.HandlerFunc(s.handleGetUnreadCountForRoom))        // GET /rooms/unread/{roomID}
}

// =======================================
// REQUEST / RESPONSE MODELS
// =======================================

type sendMessageRequest struct {
	Content          string `json:"content"`
	MessageType      string `json:"message_type"`                  // text | image | file | system
	ReplyToMessageID *int64 `json:"reply_to_message_id,omitempty"` // reply target
}

type replyInfoResponse struct {
	MessageID   int64  `json:"message_id"`
	Preview     string `json:"preview,omitempty"`
	SenderName  string `json:"sender_name,omitempty"`
	MessageType string `json:"message_type,omitempty"`
}

type sendMessageResponse struct {
	ID              int64  `json:"id"`
	RoomID          int64  `json:"room_id"`
	SenderID        int64  `json:"sender_id"`
	SenderName      string `json:"sender_name"`
	SenderAvatarURL string `json:"sender_avatar_url"`
	Content         string `json:"content"`
	MessageType     string `json:"message_type"`

	ReplyToMessageID *int64             `json:"reply_to_message_id,omitempty"`
	Reply            *replyInfoResponse `json:"reply,omitempty"`

	CreatedAt string `json:"created_at"`
}

// =======================================
// HANDLER: POST /rooms/send-messages/{roomID}
// =======================================

func (s *Server) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	// 1) only POST
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// 2) auth
	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	// 3) parse roomID
	roomID, err := getIDFromURL(r)
	if err != nil || roomID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid room id"})
		return
	}

	// 4) membership
	isMember, err := s.roomRepo.IsUserInRoom(roomID, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "you are not a member of this room"})
			return
		}
		log.Println("IsUserInRoom error:", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}
	if !isMember {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "you are not a member of this room"})
		return
	}

	// 5) parse body
	var req sendMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	// 6) validate
	req.Content = strings.TrimSpace(req.Content)
	if req.Content == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "content is required"})
		return
	}

	msgType := strings.TrimSpace(req.MessageType)
	if msgType == "" {
		msgType = "text"
	}
	switch msgType {
	case "text", "image", "file", "system":
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid message_type"})
		return
	}
	now := time.Now().UTC()

	// 7) build model
	msg := &chat.Message{
		RoomID:           roomID,
		SenderID:         userID,
		Content:          req.Content,
		MessageType:      msgType,
		IsTemp:           0,
		ReplyToMessageID: req.ReplyToMessageID,
		CreatedAt:        now, // ✅ QUAN TRỌNG

	}

	// 8) insert DB (validate reply + fill cache fields in msg)
	ctx := r.Context()
	id, err := s.chatRepo.CreateMessage(ctx, msg, true)
	if err != nil {
		if errors.Is(err, chat.ErrInvalidReplyTarget) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid reply target"})
			return
		}
		log.Println("CreateMessage error:", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "db error"})
		return
	}

	// 9) sender info for realtime
	senderName := "Unknown"
	senderAvatar := ""
	user, err := s.userRepo.GetUserByID(int(userID))
	if err != nil {
		log.Println("GetUserByID error:", err)
	} else {
		if user.Full_name.Valid && strings.TrimSpace(user.Full_name.String) != "" {
			senderName = strings.TrimSpace(user.Full_name.String)
		} else if strings.TrimSpace(user.Username) != "" {
			senderName = strings.TrimSpace(user.Username)
		}
		if user.AvatarURL.Valid {
			raw := strings.TrimSpace(user.AvatarURL.String)
			if raw != "" {
				senderAvatar = raw
			}
		}
	}

	// 10) reply object for realtime (schema giống GET)
	var reply *replyInfoResponse
	if msg.ReplyToMessageID != nil && *msg.ReplyToMessageID > 0 {
		reply = &replyInfoResponse{
			MessageID:   *msg.ReplyToMessageID,
			Preview:     msg.ReplyPreview,
			SenderName:  msg.ReplySenderName,
			MessageType: msg.ReplyMessageType,
		}
	}

	resp := sendMessageResponse{
		ID:              id,
		RoomID:          roomID,
		SenderID:        userID,
		SenderName:      senderName,
		SenderAvatarURL: senderAvatar,
		Content:         msg.Content,
		MessageType:     msg.MessageType,

		ReplyToMessageID: msg.ReplyToMessageID,
		Reply:            reply,

		CreatedAt: msg.CreatedAt.Format(time.RFC3339),
	}

	// 11) respond to sender
	writeJSON(w, http.StatusOK, resp)

	// 12) realtime push to room members (style đồng bộ)
	memberIDs, err := s.roomRepo.GetRoomMemberIDs(roomID)
	if err != nil {
		log.Println("GetRoomMemberIDs error:", err)
		return
	}

	// ✅ optional: kèm room_name / displayName qua WS
	roomLite, err := s.roomRepo.GetRoomBasic(ctx, roomID)
	if err != nil {
		log.Println("GetRoomBasic error:", err)
		roomLite = nil
	}

	// (A) message_created: append in room
	go wsSendToUsers(memberIDs, wsEnvelope{
		Type:   "message_created",
		RoomID: roomID,
		Data: map[string]any{
			"message": resp,
			"room":    roomLite, // ✅ kèm room_name
		},
	})

	// ✅ (C) unread notify: chỉ bắn cho người nhận (exclude sender)
	// DB truth: mỗi user tự tính unread_count theo last_seen_at
	recipients, err := s.chatRepo.ListRoomMemberUserIDsExcept(ctx, roomID, userID)
	if err != nil {
		log.Println("ListRoomMemberUserIDsExcept error:", err)
		return
	}

	go func(roomID int64, recips []int64) {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel2()

		for _, uid := range recips {
			cnt, err := s.chatRepo.GetUnreadCount(ctx2, roomID, uid)
			if err != nil {
				log.Println("GetUnreadCount error:", err)
				continue
			}

			wsSendToUser(uid, wsEnvelope{
				Type:   "room_unread_update",
				RoomID: roomID,
				Data: map[string]any{
					"room_id":      roomID,
					"user_id":      uid,
					"unread_count": cnt,
					"last_message": resp, // optional: FE khỏi fetch lại
					"bump":         true, // optional: move room to top
				},
			})
		}
	}(roomID, recipients)

	// // (B) room_updated: sidebar last_message + bump
	// go wsSendToUsers(memberIDs, wsEnvelope{
	// 	Type:   "room_updated",
	// 	RoomID: roomID,
	// 	Data: map[string]any{
	// 		"last_message":    resp,
	// 		"last_message_at": resp.CreatedAt,
	// 		"bump":            true,
	// 		"room":            roomLite, // ✅ kèm room_name
	// 	},
	// })

}
