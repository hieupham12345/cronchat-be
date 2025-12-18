package room

import (
	"context"
	"cronhustler/api-service/internal/chat"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type Repository struct {
	DB       *sql.DB
	chatRepo *chat.Repository
}

func NewRepository(db *sql.DB, chatRepo *chat.Repository) *Repository {
	return &Repository{
		DB:       db,
		chatRepo: chatRepo,
	}
}

type Room struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"` // direct | group
	CreatedBy   int64     `json:"created_by"`
	IsActive    int       `json:"is_active"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	UnreadCount int64     `json:"unread_count"` // NEW

}

type RoomMember struct {
	ID         int64      `json:"id"`
	FullName   string     `json:"full_name"`
	RoomID     int64      `json:"room_id"`
	UserID     int64      `json:"user_id"`
	MemberRole string     `json:"member_role"`
	JoinedAt   time.Time  `json:"joined_at"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	AvatarURL  string     `json:"avatar_url"` // 👈 thêm field này

}

func (r *Repository) CreateRoom(room *Room) (int64, error) {
	res, err := r.DB.Exec(`
        INSERT INTO rooms (name, type, created_by, is_active)
        VALUES (?, ?, ?, ?)
    `, room.Name, room.Type, room.CreatedBy, room.IsActive)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (r *Repository) AddMember(roomID, userID int64, role string) error {
	_, err := r.DB.Exec(`
        INSERT INTO room_members (room_id, user_id, member_role)
        VALUES (?, ?, ?)
    `, roomID, userID, role)
	return err
}

func (r *Repository) GetRoomByID(id int64) (*Room, error) {
	row := r.DB.QueryRow(`
        SELECT id, name, type, created_by, is_active, created_at, updated_at
        FROM rooms WHERE id = ?
    `, id)

	var rm Room
	err := row.Scan(
		&rm.ID, &rm.Name, &rm.Type, &rm.CreatedBy,
		&rm.IsActive, &rm.CreatedAt, &rm.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &rm, nil
}

func (r *Repository) GetRoomMembers(roomID int64) ([]*RoomMember, error) {
	rows, err := r.DB.Query(`
        SELECT 
            r.id,
            u.full_name,
            r.room_id,
            r.user_id,
            r.member_role,
            r.joined_at,
            r.last_seen_at,
            u.avatar_url
        FROM room_members r 
        JOIN users u ON r.user_id = u.id
        WHERE r.room_id = ?
        ORDER BY r.joined_at ASC
    `, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	members := []*RoomMember{}

	for rows.Next() {
		var m RoomMember
		var avatarURL sql.NullString // 👈 nhận NULL được

		err := rows.Scan(
			&m.ID,
			&m.FullName,
			&m.RoomID,
			&m.UserID,
			&m.MemberRole,
			&m.JoinedAt,
			&m.LastSeenAt,
			&avatarURL, // 👈 scan vào đây, KHÔNG scan thẳng m.AvatarURL
		)
		if err != nil {
			return nil, err
		}

		if avatarURL.Valid {
			m.AvatarURL = avatarURL.String
		} else {
			m.AvatarURL = "" // hoặc để default, FE tự handle
		}

		members = append(members, &m)
	}

	return members, nil
}

func (r *Repository) GetRoomsByUser(userID int64) ([]*Room, error) {
	rows, err := r.DB.Query(`
		SELECT
			r.id,
			r.name,
			r.type,
			r.created_by,
			r.is_active,
			r.created_at,
			r.updated_at,
			COALESCE((
				SELECT COUNT(*)
				FROM messages m
				WHERE
					m.room_id   = r.id
					AND m.is_temp = 0
					AND m.sender_id <> rm.user_id
					AND (
						rm.last_seen_at IS NULL
						OR m.created_at > rm.last_seen_at
					)
			), 0) AS unread_count
		FROM rooms r
		JOIN room_members rm ON rm.room_id = r.id
		WHERE
			rm.user_id = ?
			AND (
				r.type = 'group'
				OR EXISTS (
					SELECT 1
					FROM messages m2
					WHERE m2.room_id = r.id
				)
			)
		ORDER BY r.updated_at DESC;
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rooms := []*Room{}

	for rows.Next() {
		var rm Room
		err := rows.Scan(
			&rm.ID,
			&rm.Name,
			&rm.Type,
			&rm.CreatedBy,
			&rm.IsActive,
			&rm.CreatedAt,
			&rm.UpdatedAt,
			&rm.UnreadCount,
		)
		if err != nil {
			return nil, err
		}
		rooms = append(rooms, &rm)
	}

	return rooms, nil
}

func (r *Repository) GetDirectRoomBetweenUsers(a, b int64) (*Room, error) {
	row := r.DB.QueryRow(`
        SELECT r.id, r.name, r.type, r.created_by, r.is_active, r.created_at, r.updated_at
        FROM rooms r
        JOIN room_members m1 ON m1.room_id = r.id
        JOIN room_members m2 ON m2.room_id = r.id
        WHERE r.type = 'direct'
          AND m1.user_id = ?
          AND m2.user_id = ?
        LIMIT 1
    `, a, b)

	var rm Room
	err := row.Scan(
		&rm.ID, &rm.Name, &rm.Type, &rm.CreatedBy,
		&rm.IsActive, &rm.CreatedAt, &rm.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	return &rm, nil
}

type RoomWithMembers struct {
	Room    *Room         `json:"room"`
	Members []*RoomMember `json:"members"`
}

type Message struct {
	// ===== Core =====
	ID       int64 `json:"id"`
	RoomID   int64 `json:"room_id"`
	SenderID int64 `json:"sender_id"`

	SenderName      string `json:"full_name"`
	SenderAvatarURL string `json:"sender_avatar_url,omitempty"`

	Content string `json:"content"`      // text OR image/file url (fallback)
	Type    string `json:"message_type"` // text | image | file | system
	IsTemp  int    `json:"is_temp"`

	CreatedAt time.Time `json:"created_at"`

	// ===== Media (NEW) =====
	MediaURL  string `json:"media_url,omitempty"`
	MediaMIME string `json:"media_mime,omitempty"`
	MediaSize int64  `json:"media_size,omitempty"`

	// ===== Reply (NEW – denormalized) =====
	ReplyToMessageID int64  `json:"reply_to_message_id,omitempty"`
	ReplyPreview     string `json:"reply_preview,omitempty"`
	ReplySenderName  string `json:"reply_sender_name,omitempty"`
	ReplyMessageType string `json:"reply_message_type,omitempty"`

	Reactions []chat.ReactionSummaryItem `json:"reactions,omitempty"`
}

// internal/room/repository.go
func (r *Repository) IsUserInRoom(roomID, userID int64) (bool, error) {
	var count int
	err := r.DB.QueryRow(`
        SELECT COUNT(*) 
        FROM room_members
        WHERE room_id = ? AND user_id = ?
    `, roomID, userID).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (r *Repository) GetMessageCreatedAt(
	ctx context.Context,
	roomID int64,
	messageID int64,
) (time.Time, error) {

	var t time.Time
	err := r.DB.QueryRowContext(ctx, `
		SELECT created_at
		FROM messages
		WHERE room_id = ? AND id = ?
		LIMIT 1
	`, roomID, messageID).Scan(&t)

	return t, err
}

func (r *Repository) GetRoomMessages(roomID int64, beforeID int64, beforeAt time.Time, limit int, userID int64) ([]*Message, error) {
	cursorEnabled := 0
	var beforeAtVal any = nil

	if beforeID > 0 && !beforeAt.IsZero() {
		cursorEnabled = 1
		beforeAtVal = beforeAt
	}

	rows, err := r.DB.Query(`
		SELECT *
		FROM (
		  SELECT
		    m.id, m.room_id, m.sender_id,
		    m.reply_to_message_id, m.reply_preview, m.reply_sender_name, m.reply_message_type,
		    m.content, m.message_type, m.is_temp,
		    m.media_url, m.media_mime, m.media_size,
		    m.created_at,
		    u.full_name, u.username, u.avatar_url
		  FROM messages m
		  LEFT JOIN users u ON m.sender_id = u.id
		  WHERE m.room_id = ?
		    AND (
		      ? = 0
		      OR m.created_at < ?
		      OR (m.created_at = ? AND m.id < ?)
		    )
		  ORDER BY m.created_at DESC, m.id DESC
		  LIMIT ?
		) t
		ORDER BY t.created_at ASC, t.id ASC
	`, roomID, cursorEnabled, beforeAtVal, beforeAtVal, beforeID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []*Message
	var messageIDs []int64

	for rows.Next() {
		var m Message

		// user join nullable
		var fullName, username, avatarURL sql.NullString

		// reply nullable
		var replyToID sql.NullInt64
		var replyPreview, replySenderName, replyMessageType sql.NullString

		// media nullable
		var mediaURL sql.NullString
		var mediaMIME sql.NullString
		var mediaSize sql.NullInt64

		err := rows.Scan(
			&m.ID,
			&m.RoomID,
			&m.SenderID,

			&replyToID,
			&replyPreview,
			&replySenderName,
			&replyMessageType,

			&m.Content,
			&m.Type,
			&m.IsTemp,

			&mediaURL,
			&mediaMIME,
			&mediaSize,

			&m.CreatedAt,

			&fullName,
			&username,
			&avatarURL,
		)
		if err != nil {
			return nil, err
		}

		// SenderName
		if fullName.Valid && fullName.String != "" {
			m.SenderName = fullName.String
		} else if username.Valid && username.String != "" {
			m.SenderName = username.String
		} else {
			m.SenderName = "Unknown"
		}

		// SenderAvatarURL
		if avatarURL.Valid {
			m.SenderAvatarURL = avatarURL.String
		}

		// Media
		if mediaURL.Valid {
			m.MediaURL = mediaURL.String
		}
		if mediaMIME.Valid {
			m.MediaMIME = mediaMIME.String
		}
		if mediaSize.Valid {
			m.MediaSize = mediaSize.Int64
		}

		// Reply
		if replyToID.Valid {
			m.ReplyToMessageID = replyToID.Int64
		}
		if replyPreview.Valid {
			m.ReplyPreview = replyPreview.String
		}
		if replySenderName.Valid {
			m.ReplySenderName = replySenderName.String
		}
		if replyMessageType.Valid {
			m.ReplyMessageType = replyMessageType.String
		}

		msgs = append(msgs, &m)
		messageIDs = append(messageIDs, m.ID)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	// ✅ Attach reactions batch (không có message => skip)
	if len(msgs) > 0 {
		reactionMap, err := r.chatRepo.GetReactionSummaryBatch(context.Background(), messageIDs, userID)
		// NOTE: nếu mày đã dùng ctx ở handler thì nên truyền ctx vào hàm này luôn (xịn nhất).
		// Ở đây tạm để context.Background() nếu signature hiện tại không có ctx.
		if err != nil {
			return nil, err
		}

		for _, m := range msgs {
			if rs, ok := reactionMap[m.ID]; ok {
				m.Reactions = rs
			} else {
				m.Reactions = nil
			}
		}
	}

	return msgs, nil
}

func (r *Repository) GetDirectPartnerFullNameByRoomID(roomID, currentUserID int64) (string, error) {
	const query = `
		SELECT u.full_name
		FROM rooms ro
		JOIN room_members rm_self
			ON rm_self.room_id = ro.id
			AND rm_self.user_id = ?
		JOIN room_members rm_partner
			ON rm_partner.room_id = ro.id
			AND rm_partner.user_id != ?
		JOIN users u
			ON u.id = rm_partner.user_id
		WHERE ro.id = ?
		  AND ro.type = 'direct'
		LIMIT 1;
	`

	var fullName string
	err := r.DB.QueryRow(query, currentUserID, currentUserID, roomID).Scan(&fullName)
	if err != nil {
		return "", err // có thể là sql.ErrNoRows
	}
	return fullName, nil
}

func (r *Repository) CreateGroupRoom(name string, createdBy int64, memberIDs []int64) (*Room, error) {
	tx, err := r.DB.Begin()
	if err != nil {
		return nil, err
	}

	// rollback helper
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()

	// 1. Tạo room type = 'group'
	res, err := tx.Exec(`
        INSERT INTO rooms (name, type, created_by, is_active)
        VALUES (?, 'group', ?, 1)
    `, name, createdBy)
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	roomID, err := res.LastInsertId()
	if err != nil {
		tx.Rollback()
		return nil, err
	}

	// 2. Chuẩn hoá list member: đảm bảo có createdBy, remove trùng
	uniqueMembers := make(map[int64]struct{})

	// luôn đảm bảo thằng tạo nhóm là member
	uniqueMembers[createdBy] = struct{}{}

	for _, uid := range memberIDs {
		if uid <= 0 {
			continue
		}
		uniqueMembers[uid] = struct{}{}
	}

	// 3. Insert vào room_members
	for uid := range uniqueMembers {
		role := "member"
		if uid == createdBy {
			role = "owner" // hoặc 'admin' tuỳ convention của m
		}

		_, err := tx.Exec(`
            INSERT INTO room_members (room_id, user_id, member_role)
            VALUES (?, ?, ?)
        `, roomID, uid, role)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
	}

	// 4. Commit transaction
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// 5. Trả về struct Room tối thiểu
	room := &Room{
		ID:        roomID,
		Name:      name,
		Type:      "group",
		CreatedBy: createdBy,
		IsActive:  1,
	}

	return room, nil
}

func (r *Repository) MarkRoomAsRead(roomID, userID int64, t time.Time) error {
	_, err := r.DB.Exec(`
        UPDATE room_members
        SET last_seen_at = ?
        WHERE room_id = ? AND user_id = ?
    `, t, roomID, userID)
	return err
}

func (r *Repository) DeleteUserGroup(roomID int64, userID int64) error {
	// chỉ xóa nếu tồn tại trong room_members
	_, err := r.DB.Exec(`
        DELETE FROM room_members
        WHERE room_id = ? AND user_id = ?
    `, roomID, userID)

	return err
}

func (r *Repository) GetRoomOwner(roomID int64) (int64, error) {
	var ownerID int64
	err := r.DB.QueryRow(`
        SELECT user_id
        FROM room_members
        WHERE room_id = ? AND member_role = 'owner'
        LIMIT 1
    `, roomID).Scan(&ownerID)
	if err != nil {
		return 0, err
	}
	return ownerID, nil
}

func (r *Repository) DeleteRoom(roomID, userID int64) error {
	// ========== 1) Lấy thông tin room ==========
	var (
		roomType  string
		createdBy int64
	)

	err := r.DB.QueryRow(`
		SELECT type, created_by
		FROM rooms
		WHERE id = ?
	`, roomID).Scan(&roomType, &createdBy)

	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("room not found")
		}
		return fmt.Errorf("query room: %w", err)
	}

	// ========== 2) Check quyền theo type ==========
	switch roomType {
	case "group":
		// group: chỉ cho created_by hoặc owner xoá
		var memberRole string
		err = r.DB.QueryRow(`
			SELECT member_role
			FROM room_members
			WHERE room_id = ? AND user_id = ?
		`, roomID, userID).Scan(&memberRole)

		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("you are not a member of this room")
			}
			return fmt.Errorf("query room member: %w", err)
		}

		if userID != createdBy && memberRole != "owner" {
			return fmt.Errorf("you are not allowed to delete this room")
		}

	case "direct":
		// direct: chỉ cần là member là có quyền xoá (xoá cho cả 2 luôn)
		var dummy int
		err = r.DB.QueryRow(`
			SELECT 1
			FROM room_members
			WHERE room_id = ? AND user_id = ?
		`, roomID, userID).Scan(&dummy)

		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("you are not a member of this room")
			}
			return fmt.Errorf("query room member: %w", err)
		}

	default:
		// phòng lạ lạ
		return fmt.Errorf("unsupported room type")
	}

	// ========== 3) Xóa room trong transaction ==========
	tx, err := r.DB.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// DELETE rooms -> nhờ FK ON DELETE CASCADE:
	// - room_members
	// - messages
	// - message_reads
	// - attachments
	if _, err := tx.Exec(`DELETE FROM rooms WHERE id = ?`, roomID); err != nil {
		return fmt.Errorf("delete room: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit delete room: %w", err)
	}

	return nil
}

func (r *Repository) CreateImageMessage(
	roomID int64,
	senderID int64,
	imageURL string,
) (*Message, error) {

	now := time.Now()

	res, err := r.DB.Exec(`
		INSERT INTO messages (room_id, sender_id, content, message_type, created_at)
		VALUES (?, ?, ?, 'image', ?)
	`, roomID, senderID, imageURL, now)
	if err != nil {
		return nil, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}

	// LẤY INFO USER (để trả đúng format message hiện tại)
	var (
		fullName  string
		avatarURL string
	)

	_ = r.DB.QueryRow(`
		SELECT full_name, avatar_url
		FROM users
		WHERE id = ?
	`, senderID).Scan(&fullName, &avatarURL)

	return &Message{
		ID:              id,
		RoomID:          roomID,
		SenderID:        senderID,
		SenderName:      fullName,
		Content:         imageURL,
		Type:            "image",
		IsTemp:          0,
		CreatedAt:       now,
		SenderAvatarURL: avatarURL,
	}, nil
}

// GetRoomMemberIDs returns all user_ids in room
func (r *Repository) GetRoomMemberIDs(roomID int64) ([]int64, error) {
	rows, err := r.DB.Query(`SELECT user_id FROM room_members WHERE room_id = ?`, roomID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var uid int64
		if err := rows.Scan(&uid); err != nil {
			return nil, err
		}
		ids = append(ids, uid)
	}
	return ids, rows.Err()
}

// ==========================
// MarkRoomSeenUpTo
// - update last_seen_message_id và last_seen_at cho user trong room
// - dùng GREATEST để không bị "seen lùi" khi request đến trễ
// ==========================
func (r *Repository) MarkRoomSeenUpTo(ctx context.Context, roomID, userID, lastSeenMessageID int64) error {
	if roomID <= 0 || userID <= 0 || lastSeenMessageID <= 0 {
		return nil
	}

	_, err := r.DB.ExecContext(ctx, `
		UPDATE room_members
		SET
			last_seen_message_id = GREATEST(COALESCE(last_seen_message_id, 0), ?),
			last_seen_at = ?
		WHERE room_id = ? AND user_id = ?
	`, lastSeenMessageID, time.Now(), roomID, userID)

	return err
}

type RoomLite struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (r *Repository) GetRoomByIDLite(ctx context.Context, roomID int64) (*RoomLite, error) {
	const q = `
		SELECT id, name, type, updated_at
		FROM rooms
		WHERE id = ?
		LIMIT 1;
	`
	var x RoomLite
	err := r.DB.QueryRowContext(ctx, q, roomID).Scan(&x.ID, &x.Name, &x.Type, &x.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, err
		}
		return nil, err
	}
	return &x, nil
}

type roomLiteResponse struct {
	ID          int64  `json:"id"`
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`        // raw name (group)
	DisplayName string `json:"displayName,omitempty"` // tên hiển thị FE dùng
}

func (r *Repository) GetRoomBasic(ctx context.Context, roomID int64) (*roomLiteResponse, error) {
	// giả sử rooms có columns: id, type, name
	var typ, name string
	err := r.DB.QueryRowContext(ctx, `SELECT type, name FROM rooms WHERE id=?`, roomID).Scan(&typ, &name)
	if err != nil {
		return nil, err
	}

	display := name
	if strings.TrimSpace(display) == "" {
		display = "Room"
	}
	return &roomLiteResponse{
		ID:          roomID,
		Type:        typ,
		Name:        name,
		DisplayName: display,
	}, nil
}
