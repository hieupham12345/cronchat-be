package httpserver

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type wsEnvelope struct {
	Type   string `json:"type"`
	RoomID int64  `json:"room_id,omitempty"`
	Data   any    `json:"data,omitempty"`
	TS     int64  `json:"ts"`
}

type wsClient struct {
	conn   *websocket.Conn
	sendCh chan []byte
	userID int64
}

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

// wsByUser[userID] => set of clients
var (
	wsByUser   = make(map[int64]map[*wsClient]bool)
	wsByUserMu sync.RWMutex
)

func (s *Server) mountWsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/ws", s.handleWebSocket)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {

	log.Printf("[WS] incoming: %s\n", r.URL.Path)

	userID, err := s.VerifyWSAuth(r)
	if err != nil {
		log.Println("[WS] auth failed:", err)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("upgrade error:", err)
		return
	}

	c := &wsClient{
		conn:   conn,
		sendCh: make(chan []byte, 32),
		userID: userID,
	}

	// ✅ 2) add client
	wsByUserMu.Lock()
	if wsByUser[userID] == nil {
		wsByUser[userID] = make(map[*wsClient]bool)
	}
	wsByUser[userID][c] = true
	total := len(wsByUser[userID])
	wsByUserMu.Unlock()

	log.Printf("[WS] user=%d connected, conns=%d\n", userID, total)

	// ✅ 3) writer loop (đảm bảo 1 goroutine write duy nhất / conn)
	go func() {
		pingTicker := time.NewTicker(25 * time.Second)
		defer func() {
			pingTicker.Stop()
			_ = conn.Close()
		}()

		for {
			select {
			case msg, ok := <-c.sendCh:
				if !ok {
					return
				}
				conn.SetWriteDeadline(time.Now().Add(8 * time.Second))
				if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
					return
				}

			case <-pingTicker.C:
				conn.SetWriteDeadline(time.Now().Add(8 * time.Second))
				if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()

	// ✅ 4) reader loop để detect disconnect
	conn.SetReadLimit(1 << 20)
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	go func() {
		defer func() {
			// remove client
			wsByUserMu.Lock()
			if m := wsByUser[userID]; m != nil {
				delete(m, c)
				if len(m) == 0 {
					delete(wsByUser, userID)
				}
			}
			wsByUserMu.Unlock()

			close(c.sendCh)
			log.Printf("[WS] user=%d disconnected\n", userID)
		}()

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
}

// ===== helpers =====

func wsSendToUser(userID int64, env wsEnvelope) {
	env.TS = time.Now().UnixMilli()
	b, _ := json.Marshal(env)

	wsByUserMu.RLock()
	set := wsByUser[userID]
	if len(set) == 0 {
		wsByUserMu.RUnlock()
		return
	}
	clients := make([]*wsClient, 0, len(set))
	for c := range set {
		clients = append(clients, c)
	}
	wsByUserMu.RUnlock()

	for _, c := range clients {
		select {
		case c.sendCh <- b:
		default:
			// sendCh full -> drop connection cho sạch
			_ = c.conn.Close()
		}
	}
}

func wsSendToUsers(userIDs []int64, env wsEnvelope) {
	// tránh send trùng user
	seen := make(map[int64]struct{}, len(userIDs))
	for _, uid := range userIDs {
		if _, ok := seen[uid]; ok {
			continue
		}
		seen[uid] = struct{}{}
		wsSendToUser(uid, env)
	}
}
