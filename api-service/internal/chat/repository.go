package chat

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

var ErrMessageNotFound = errors.New("message not found")

type Repository struct {
	DB *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{DB: db}
}

// ========== MODELS ==========

type Message struct {
	ID          int64  `json:"id"`
	RoomID      int64  `json:"room_id"`
	SenderID    int64  `json:"sender_id"`
	Content     string `json:"content"`
	MessageType string `json:"message_type"`
	IsTemp      int    `json:"is_temp"`

	ReplyToMessageID *int64 `json:"reply_to_message_id,omitempty"`

	// ✅ cache reply content để GET nhanh + UI render
	ReplyPreview     string `json:"reply_preview,omitempty"`
	ReplySenderName  string `json:"reply_sender_name,omitempty"`
	ReplyMessageType string `json:"reply_message_type,omitempty"`

	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type Attachment struct {
	ID          int64     `json:"id"`
	MessageID   int64     `json:"message_id"`
	FileName    string    `json:"file_name"`
	FileSize    int64     `json:"file_size"`
	ContentType string    `json:"content_type"`
	FilePath    string    `json:"file_path"`
	CreatedAt   time.Time `json:"created_at"`
}

type MessageRead struct {
	ID        int64     `json:"id"`
	MessageID int64     `json:"message_id"`
	UserID    int64     `json:"user_id"`
	ReadAt    time.Time `json:"read_at"`
}

// ==============================
// Helpers
// ==============================

var ErrInvalidReplyTarget = errors.New("invalid reply target message")

// EnsureReplyTargetValid:
// - reply message phải tồn tại
// - và phải nằm cùng room

type replyInfo struct {
	Preview     string
	SenderName  string
	MessageType string
}

func buildReplyPreview(messageType string, content sql.NullString) string {
	mt := strings.TrimSpace(messageType)
	switch mt {
	case "image":
		return "📷 Image"
	case "file":
		return "📎 File"
	case "system", "text":
		// ok
	default:
		// fallback
	}

	txt := ""
	if content.Valid {
		txt = strings.TrimSpace(content.String)
	}
	if txt == "" {
		// nếu text rỗng mà không phải image/file -> vẫn trả rỗng
		return ""
	}

	// cắt 300 chars theo schema VARCHAR(300)
	rs := []rune(txt)
	if len(rs) > 300 {
		txt = string(rs[:300])
	}
	return txt
}

func pickName(fullName, username sql.NullString) string {
	if fullName.Valid && strings.TrimSpace(fullName.String) != "" {
		return strings.TrimSpace(fullName.String)
	}
	if username.Valid && strings.TrimSpace(username.String) != "" {
		return strings.TrimSpace(username.String)
	}
	return "Unknown"
}

func (r *Repository) fetchReplyInfo(ctx context.Context, roomID int64, replyToID int64) (*replyInfo, error) {
	var (
		rmContent sql.NullString
		rmType    sql.NullString
		uFullName sql.NullString
		uUsername sql.NullString
	)

	err := r.DB.QueryRowContext(ctx, `
		SELECT 
			rm.content,
			rm.message_type,
			u.full_name,
			u.username
		FROM messages rm
		LEFT JOIN users u ON rm.sender_id = u.id
		WHERE rm.id = ? AND rm.room_id = ?
		LIMIT 1
	`, replyToID, roomID).Scan(&rmContent, &rmType, &uFullName, &uUsername)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidReplyTarget
		}
		return nil, err
	}

	mt := "text"
	if rmType.Valid && strings.TrimSpace(rmType.String) != "" {
		mt = strings.TrimSpace(rmType.String)
	}

	return &replyInfo{
		Preview:     buildReplyPreview(mt, rmContent),
		SenderName:  pickName(uFullName, uUsername),
		MessageType: mt,
	}, nil
}

func fetchReplyInfoTx(ctx context.Context, tx *sql.Tx, roomID int64, replyToID int64) (*replyInfo, error) {
	var (
		rmContent sql.NullString
		rmType    sql.NullString
		uFullName sql.NullString
		uUsername sql.NullString
	)

	err := tx.QueryRowContext(ctx, `
		SELECT 
			rm.content,
			rm.message_type,
			u.full_name,
			u.username
		FROM messages rm
		LEFT JOIN users u ON rm.sender_id = u.id
		WHERE rm.id = ? AND rm.room_id = ?
		LIMIT 1
	`, replyToID, roomID).Scan(&rmContent, &rmType, &uFullName, &uUsername)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInvalidReplyTarget
		}
		return nil, err
	}

	mt := "text"
	if rmType.Valid && strings.TrimSpace(rmType.String) != "" {
		mt = strings.TrimSpace(rmType.String)
	}

	return &replyInfo{
		Preview:     buildReplyPreview(mt, rmContent),
		SenderName:  pickName(uFullName, uUsername),
		MessageType: mt,
	}, nil
}

func nullIfEmpty(s string) sql.NullString {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}

func (r *Repository) EnsureReplyTargetValid(ctx context.Context, roomID int64, replyToID int64) error {
	var existingRoomID int64
	err := r.DB.QueryRowContext(ctx,
		`SELECT room_id FROM messages WHERE id = ? LIMIT 1`,
		replyToID,
	).Scan(&existingRoomID)

	if err == sql.ErrNoRows {
		return ErrInvalidReplyTarget
	}
	if err != nil {
		return err
	}
	if existingRoomID != roomID {
		return ErrInvalidReplyTarget
	}
	return nil
}

// ==============================
// Create message (core)
// ==============================

// CreateMessage: insert 1 message (supports reply_to_message_id)
// ctx để mày dễ cancel/timeout + đồng bộ style các repo khác

func (r *Repository) CreateMessage(ctx context.Context, msg *Message, validateReply bool) (int64, error) {
	if msg == nil {
		return 0, errors.New("msg is nil")
	}

	// ✅ Optional validate + fill reply cache
	if msg.ReplyToMessageID != nil && *msg.ReplyToMessageID > 0 {
		if validateReply {
			info, err := r.fetchReplyInfo(ctx, msg.RoomID, *msg.ReplyToMessageID)
			if err != nil {
				return 0, err
			}
			msg.ReplyPreview = info.Preview
			msg.ReplySenderName = info.SenderName
			msg.ReplyMessageType = info.MessageType
		}
	}

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	// ✅ CALL proc (now supports reply fields)
	_, err = tx.ExecContext(ctx, `
	CALL sp_send_message_with_day_sep(?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		msg.RoomID,
		msg.SenderID,
		msg.Content,
		msg.MessageType,
		msg.IsTemp,
		msg.ReplyToMessageID,
		nullIfEmpty(msg.ReplyPreview),
		nullIfEmpty(msg.ReplySenderName),
		nullIfEmpty(msg.ReplyMessageType),
	)

	if err != nil {
		return 0, err
	}

	// ✅ Last insert inside the proc is the "real message"
	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT LAST_INSERT_ID()`).Scan(&id); err != nil {
		return 0, err
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}

	msg.ID = id
	return id, nil
}

// CreateMessageTx: tạo message trong transaction (để dùng kèm attachments)
func (r *Repository) CreateMessageTx(ctx context.Context, tx *sql.Tx, msg *Message, validateReply bool) (int64, error) {
	if msg == nil {
		return 0, errors.New("msg is nil")
	}
	if tx == nil {
		return 0, errors.New("tx is nil")
	}

	if msg.ReplyToMessageID != nil && *msg.ReplyToMessageID > 0 {
		if validateReply {
			info, err := fetchReplyInfoTx(ctx, tx, msg.RoomID, *msg.ReplyToMessageID)
			if err != nil {
				return 0, err
			}
			msg.ReplyPreview = info.Preview
			msg.ReplySenderName = info.SenderName
			msg.ReplyMessageType = info.MessageType
		}
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO messages (
			room_id, sender_id,
			reply_to_message_id, reply_preview, reply_sender_name, reply_message_type,
			content, message_type, is_temp
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		msg.RoomID,
		msg.SenderID,

		msg.ReplyToMessageID,
		nullIfEmpty(msg.ReplyPreview),
		nullIfEmpty(msg.ReplySenderName),
		nullIfEmpty(msg.ReplyMessageType),

		msg.Content,
		msg.MessageType,
		msg.IsTemp,
	)
	if err != nil {
		return 0, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	msg.ID = id
	return id, nil
}

// CreateMessageWithAttachments: atomic create message + attachments
func (r *Repository) CreateMessageWithAttachments(
	ctx context.Context,
	msg *Message,
	atts []Attachment,
	validateReply bool,
) (int64, error) {

	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	messageID, err := r.CreateMessageTx(ctx, tx, msg, validateReply)
	if err != nil {
		return 0, err
	}

	for i := range atts {
		atts[i].MessageID = messageID
		if _, err := r.CreateAttachmentTx(ctx, tx, &atts[i]); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return messageID, nil
}

// ==============================
// Attachments
// ==============================

func (r *Repository) CreateAttachment(ctx context.Context, att *Attachment) (int64, error) {
	if att == nil {
		return 0, errors.New("att is nil")
	}

	res, err := r.DB.ExecContext(ctx, `
		INSERT INTO attachments (message_id, file_name, file_size, content_type, file_path)
		VALUES (?, ?, ?, ?, ?)
	`,
		att.MessageID,
		att.FileName,
		att.FileSize,
		att.ContentType,
		att.FilePath,
	)
	if err != nil {
		return 0, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	att.ID = id
	return id, nil
}

func (r *Repository) CreateAttachmentTx(ctx context.Context, tx *sql.Tx, att *Attachment) (int64, error) {
	if att == nil {
		return 0, errors.New("att is nil")
	}
	if tx == nil {
		return 0, errors.New("tx is nil")
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO attachments (message_id, file_name, file_size, content_type, file_path)
		VALUES (?, ?, ?, ?, ?)
	`,
		att.MessageID,
		att.FileName,
		att.FileSize,
		att.ContentType,
		att.FilePath,
	)
	if err != nil {
		return 0, err
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	att.ID = id
	return id, nil
}
