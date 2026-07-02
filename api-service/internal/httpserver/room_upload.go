package httpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

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
