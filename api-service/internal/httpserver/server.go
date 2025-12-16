package httpserver

import (
	"cronhustler/api-service/internal/chat"
	"cronhustler/api-service/internal/room"
	"cronhustler/api-service/internal/user"
	"database/sql"
	"net/http"
	"os"
)

// Server giữ state chung
type Server struct {
	mux           *http.ServeMux
	userRepo      *user.Repository
	jwtSecret     []byte
	roomRepo      *room.Repository
	chatRepo      *chat.Repository
	avatarDir     string // thư mục vật lý lưu avatar
	chatUploadDir string // thư mục vật lý lưu hình ảnh chat
	// jobRepo  *job.Repository
}

// NewServer: nhận thêm avatarDir
func NewServer(db *sql.DB, secret []byte, avatarDir string, chatUploadDir string) *Server {
	mux := http.NewServeMux()

	// đảm bảo avatarDir tồn tại phòng hờ (thường đã mkdirAll ở main rồi)
	if avatarDir == "" {
		avatarDir = "./data/user_avatars"
	}

	if chatUploadDir == "" {
		chatUploadDir = "./data/chat_uploads"
	}
	_ = os.MkdirAll(chatUploadDir, 0o755)
	_ = os.MkdirAll(avatarDir, 0o755)

	s := &Server{
		mux:           mux,
		userRepo:      user.NewRepository(db),
		jwtSecret:     secret,
		roomRepo:      room.NewRepository(db, chat.NewRepository(db)),
		chatRepo:      chat.NewRepository(db),
		avatarDir:     avatarDir,
		chatUploadDir: chatUploadDir,
	}

	// ===== MOUNT ROUTES =====

	// serve static avatar trước cũng được
	s.mux.Handle("/static/user_avatars/",
		http.StripPrefix("/static/user_avatars/",
			http.FileServer(http.Dir(s.avatarDir)),
		),
	)
	// serve static chat images
	s.mux.Handle("/static/chat_uploads/",
		http.StripPrefix("/static/chat_uploads/",
			http.FileServer(http.Dir(s.chatUploadDir)),
		),
	)

	// chia theo nhóm, mỗi nhóm định nghĩa ở file riêng
	s.mountAuthRoutes(s.mux)
	s.mountUserRoutes(s.mux)
	s.mountRoomRoutes(s.mux)
	s.mountChatRoutes(s.mux)
	s.mountWsRoutes(s.mux)
	// s.mountJobRoutes(s.mux)

	return s
}

// Routes trả về handler chính, quấn logger ở đây
func (s *Server) Routes() http.Handler {
	return LoggerMiddleware(s.mux)
}
