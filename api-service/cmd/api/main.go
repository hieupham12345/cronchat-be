package main

import (
	"cronhustler/api-service/internal/httpserver"
	"cronhustler/db"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
)

func main() {
	// ============================
	// 1) Load ENV
	// ============================
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️  Không tìm thấy file .env, dùng ENV hệ thống")
	}

	// ============================
	// 2) Build MySQL DSN
	// ============================
	mysqlUser := os.Getenv("MYSQL_USER")
	mysqlPass := os.Getenv("MYSQL_PASSWORD")
	mysqlHost := os.Getenv("MYSQL_HOST")
	mysqlPort := os.Getenv("MYSQL_PORT")
	mysqlDB := os.Getenv("MYSQL_DATABASE")

	if mysqlUser == "" || mysqlDB == "" {
		log.Fatal("❌ Thiếu MYSQL_USER hoặc MYSQL_DATABASE trong ENV")
	}

	if mysqlHost == "" {
		mysqlHost = "127.0.0.1"
	}
	if mysqlPort == "" {
		mysqlPort = "3306"
	}

	dsn := mysqlUser + ":" + mysqlPass +
		"@tcp(" + mysqlHost + ":" + mysqlPort + ")/" +
		mysqlDB + "?parseTime=true&charset=utf8mb4&loc=Local"

	// ============================
	// 3) Kết nối MySQL
	// ============================
	database, err := db.OpenMySQL(dsn)
	if err != nil {
		log.Fatalf("❌ Không kết nối được MySQL: %v", err)
	}
	defer database.Close()

	for i := 1; i <= 3; i++ {
		if err := database.Ping(); err == nil {
			break
		}
		log.Printf("⏳ Ping MySQL lần %d thất bại: %v", i, err)
		time.Sleep(time.Second)
		if i == 3 {
			log.Fatal("❌ MySQL không sẵn sàng")
		}
	}

	log.Println("✅ MySQL connected")

	// ============================
	// 4) JWT Secret
	// ============================
	secret := []byte(os.Getenv("GO_SECRET_KEY"))
	if len(secret) == 0 {
		log.Fatal("❌ GO_SECRET_KEY chưa được cấu hình")
	}

	// ============================
	// 5) Server address
	// ============================
	addr := os.Getenv("BASE_URL")
	if addr == "" {
		addr = ":5555"
	}

	// ============================
	// 6) Avatar directory
	// ============================
	avatarDir := os.Getenv("AVATAR_DIR")
	if avatarDir == "" {
		avatarDir = "./data/user_avatars"
	}
	mustCreateDir("Avatar", avatarDir)

	// ============================
	// 7) Chat upload directory (NEW)
	// ============================
	chatUploadDir := os.Getenv("CHAT_UPLOAD_DIR")
	if chatUploadDir == "" {
		chatUploadDir = "./data/chat_uploads"
	}
	mustCreateDir("Chat upload", chatUploadDir)

	// ============================
	// 8) Create server
	// ============================
	srv := httpserver.NewServer(
		database,
		secret,
		avatarDir,
		chatUploadDir, // 👈 NEW
	)

	log.Printf("🖼  Avatar dir      : %s", avatarDir)
	log.Printf("🖼  Chat upload dir : %s", chatUploadDir)

	// ============================
	// 9) Routes + CORS
	// ============================
	handler := httpserver.WithCORS(srv.Routes())

	// ============================
	// 10) Run server
	// ============================
	log.Printf("🚀 Server running on http://%s", addr)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatal(err)
	}
}

// helper tạo thư mục
func mustCreateDir(name, path string) {
	if err := os.MkdirAll(path, 0o755); err != nil {
		log.Fatalf("❌ Không tạo được thư mục %s (%s): %v", name, path, err)
	}
}
