package httpserver

import (
	"context"
	"net/http"
	"time"
)

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
