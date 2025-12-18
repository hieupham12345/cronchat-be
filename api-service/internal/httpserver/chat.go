package httpserver

import (
	"context"
	"cronhustler/api-service/internal/chat"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"
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

// ===== Reactions =====

type reactMessageRequest struct {
	MessageID int64  `json:"message_id"`
	Reaction  string `json:"reaction"` // like | love | laugh | wow | sad (hoặc emoji nếu mày cho phép)
}

type toggleReactionResponse struct {
	MessageID int64  `json:"message_id"`
	Reaction  string `json:"reaction"`
	Added     bool   `json:"added"` // true = inserted, false = removed (toggle)
}

type removeReactionRequest struct {
	MessageID int64  `json:"message_id"`
	Reaction  string `json:"reaction,omitempty"` // empty => remove all my reactions on this message
}

type reactionSummaryResponse struct {
	MessageID int64                      `json:"message_id"`
	Reactions []chat.ReactionSummaryItem `json:"reactions"`
}

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

	// 7) build model
	now := time.Now()
	msg := &chat.Message{
		RoomID:           roomID,
		SenderID:         userID,
		Content:          req.Content,
		MessageType:      msgType,
		IsTemp:           0,
		ReplyToMessageID: req.ReplyToMessageID,
		CreatedAt:        now,
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

// =======================================
// HANDLER: POST /messages/react/add (TOGGLE)
// =======================================

func (s *Server) handleToggleReaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	var req reactMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	req.Reaction = strings.TrimSpace(req.Reaction)
	if req.MessageID <= 0 || req.Reaction == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message_id and reaction are required"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	added, err := s.chatRepo.ToggleReaction(ctx, req.MessageID, userID, req.Reaction)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, toggleReactionResponse{
		MessageID: req.MessageID,
		Reaction:  req.Reaction,
		Added:     added,
	})

	// realtime: fetch room + summary rồi broadcast
	go func(messageID, actorUserID int64) {
		ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel2()

		roomID, err := s.chatRepo.GetMessageRoomID(ctx2, messageID)
		if err != nil {
			log.Println("GetMessageRoomID error:", err)
			return
		}

		items, err := s.chatRepo.GetReactionSummary(ctx2, messageID, actorUserID)
		if err != nil {
			log.Println("GetReactionSummary error:", err)
			return
		}

		memberIDs, err := s.roomRepo.GetRoomMemberIDs(roomID)
		if err != nil {
			log.Println("GetRoomMemberIDs error:", err)
			return
		}

		wsSendToUsers(memberIDs, wsEnvelope{
			Type:   "reaction_updated",
			RoomID: roomID,
			Data: map[string]any{
				"message_id": messageID,
				"reactions":  items,
			},
		})
	}(req.MessageID, userID)
}

// =======================================
// HANDLER: POST /messages/react/remove (FORCE REMOVE)
// =======================================

func (s *Server) handleRemoveReaction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	var req removeReactionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json body"})
		return
	}

	if req.MessageID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message_id is required"})
		return
	}
	req.Reaction = strings.TrimSpace(req.Reaction)

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	if req.Reaction == "" {
		if err := s.chatRepo.RemoveAllReactionsByUser(ctx, req.MessageID, userID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"message_id": req.MessageID,
			"removed":    true,
			"all":        true,
		})
		return
	}

	if err := s.chatRepo.RemoveReaction(ctx, req.MessageID, userID, req.Reaction); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"message_id": req.MessageID,
		"reaction":   req.Reaction,
		"removed":    true,
	})
}

// =======================================
// HANDLER: GET /messages/reactions/{messageID}
// =======================================

func (s *Server) handleGetReactionSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	messageID, err := getMessageIDFromReactionsPath(r.URL.Path)
	if err != nil || messageID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid message id"})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	items, err := s.chatRepo.GetReactionSummary(ctx, messageID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, reactionSummaryResponse{
		MessageID: messageID,
		Reactions: items,
	})
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

// =======================================
// HELPERS
// =======================================

// expects: /messages/reactions/{messageID}
func getMessageIDFromReactionsPath(path string) (int64, error) {
	prefix := "/messages/reactions/"
	if !strings.HasPrefix(path, prefix) {
		return 0, errors.New("invalid path")
	}
	raw := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	if raw == "" {
		return 0, errors.New("missing id")
	}
	return strconv.ParseInt(raw, 10, 64)
}

type unreadCountForRoomResponse struct {
	RoomID      int64 `json:"room_id"`
	UserID      int64 `json:"user_id"`
	UnreadCount int64 `json:"unread_count"`
}

type unreadCountsByRoomsResponse struct {
	UserID int64           `json:"user_id"`
	Counts map[int64]int64 `json:"counts"` // room_id -> unread_count
}

func (s *Server) handleGetUnreadCountsByRooms(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	counts, err := s.chatRepo.GetUnreadCountsByRooms(ctx, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, unreadCountsByRoomsResponse{
		UserID: userID,
		Counts: counts,
	})
}

func (s *Server) handleGetUnreadCountForRoom(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	userID, err := GetUserIDFromRequest(r, s.jwtSecret)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": err.Error()})
		return
	}

	roomID, err := getIDFromURL(r) // expects /rooms/unread/{roomID}
	if err != nil || roomID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid room id"})
		return
	}

	// membership
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

	cnt, err := s.chatRepo.GetUnreadCount(ctx, roomID, userID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, unreadCountForRoomResponse{
		RoomID:      roomID,
		UserID:      userID,
		UnreadCount: cnt,
	})
}
