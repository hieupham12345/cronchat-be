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
