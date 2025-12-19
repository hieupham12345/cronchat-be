package chat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
		CALL sp_send_message_with_day_sep(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		msg.RoomID,
		msg.SenderID,

		msg.Content,
		msg.MessageType,
		msg.IsTemp,

		// ✅ reply fields
		msg.ReplyToMessageID,          // pointer => nil ok
		nullIfEmpty(msg.ReplyPreview), // nil if empty => DB NULL
		nullIfEmpty(msg.ReplySenderName),
		nullIfEmpty(msg.ReplyMessageType),

		// ✅ created_at (match response)
		msg.CreatedAt, // if zero-time -> you can pass nil, but better ensure handler sets it
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

// ==========================
// Reactions
// ==========================

// =========================
// MODELS
// =========================

type ReactionSummaryItem struct {
	Reaction    string `json:"reaction"`
	Count       int    `json:"count"`
	ReactedByMe bool   `json:"reacted_by_me"`
}

type ReactionUserItem struct {
	UserID    int64     `json:"user_id"`
	FullName  string    `json:"full_name"`
	AvatarURL *string   `json:"avatar_url,omitempty"`
	Reaction  string    `json:"reaction"`
	CreatedAt time.Time `json:"created_at"`
}

// =========================
// TOGGLE / REMOVE
// =========================

// ToggleReaction: nếu chưa có -> insert (added=true)
// nếu đã có -> delete (added=false)
func (r *Repository) ToggleReaction(ctx context.Context, messageID, userID int64, reaction string) (added bool, err error) {
	reaction = strings.TrimSpace(reaction)
	if messageID <= 0 || userID <= 0 || reaction == "" {
		return false, errors.New("invalid input")
	}

	// INSERT IGNORE để tránh duplicate theo unique(message_id,user_id,reaction)
	res, err := r.DB.ExecContext(ctx, `
		INSERT IGNORE INTO message_reactions (message_id, user_id, reaction)
		VALUES (?, ?, ?)
	`, messageID, userID, reaction)
	if err != nil {
		return false, err
	}

	ra, err := res.RowsAffected()
	if err != nil {
		return false, err
	}

	// Insert thành công => added
	if ra == 1 {
		return true, nil
	}

	// Đã tồn tại => xóa để toggle off
	_, err = r.DB.ExecContext(ctx, `
		DELETE FROM message_reactions
		WHERE message_id = ? AND user_id = ? AND reaction = ?
	`, messageID, userID, reaction)
	if err != nil {
		return false, err
	}

	return false, nil
}

// RemoveReaction: remove “cứng” (không toggle)
func (r *Repository) RemoveReaction(ctx context.Context, messageID, userID int64, reaction string) error {
	reaction = strings.TrimSpace(reaction)
	if messageID <= 0 || userID <= 0 || reaction == "" {
		return errors.New("invalid input")
	}

	_, err := r.DB.ExecContext(ctx, `
		DELETE FROM message_reactions
		WHERE message_id = ? AND user_id = ? AND reaction = ?
	`, messageID, userID, reaction)
	return err
}

// =========================
// SUMMARY (SINGLE)
// =========================

// GetReactionSummary: trả về list {reaction,count,reacted_by_me} cho 1 message
func (r *Repository) GetReactionSummary(ctx context.Context, messageID, viewerUserID int64) ([]ReactionSummaryItem, error) {
	if messageID <= 0 {
		return nil, errors.New("invalid message id")
	}

	rows, err := r.DB.QueryContext(ctx, `
		SELECT
			reaction,
			COUNT(*) AS cnt,
			(SUM(user_id = ?) > 0) AS reacted_by_me
		FROM message_reactions
		WHERE message_id = ?
		GROUP BY reaction
		ORDER BY cnt DESC, reaction ASC
	`, viewerUserID, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ReactionSummaryItem
	for rows.Next() {
		var it ReactionSummaryItem
		var reactedByMeBoolInt int // MySQL trả 0/1
		if err := rows.Scan(&it.Reaction, &it.Count, &reactedByMeBoolInt); err != nil {
			return nil, err
		}
		it.ReactedByMe = reactedByMeBoolInt == 1
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// =========================
// SUMMARY (BATCH) - DÙNG KHI GET ROOM MESSAGES
// =========================

// GetReactionSummaryBatch: map[messageID][]ReactionSummaryItem
// messageIDs là list message đang load (vd 20-50 cái)
func (r *Repository) GetReactionSummaryBatch(ctx context.Context, messageIDs []int64, viewerUserID int64) (map[int64][]ReactionSummaryItem, error) {
	result := make(map[int64][]ReactionSummaryItem)
	if len(messageIDs) == 0 {
		return result, nil
	}

	inClause, args := buildInt64InClause(messageIDs)
	// args: messageIDs..., mình cần viewerUserID đứng đầu vì query dùng trước
	queryArgs := make([]any, 0, 1+len(args))
	queryArgs = append(queryArgs, viewerUserID)
	queryArgs = append(queryArgs, args...)

	q := fmt.Sprintf(`
		SELECT
			message_id,
			reaction,
			COUNT(*) AS cnt,
			(SUM(user_id = ?) > 0) AS reacted_by_me
		FROM message_reactions
		WHERE message_id IN (%s)
		GROUP BY message_id, reaction
		ORDER BY message_id ASC, cnt DESC, reaction ASC
	`, inClause)

	rows, err := r.DB.QueryContext(ctx, q, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var messageID int64
		var it ReactionSummaryItem
		var reactedByMeBoolInt int
		if err := rows.Scan(&messageID, &it.Reaction, &it.Count, &reactedByMeBoolInt); err != nil {
			return nil, err
		}
		it.ReactedByMe = reactedByMeBoolInt == 1
		result[messageID] = append(result[messageID], it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// =========================
// LIST USERS REACTED (DETAIL VIEW)
// =========================

func (r *Repository) ListReactionsByMessage(ctx context.Context, messageID int64) ([]ReactionUserItem, error) {
	if messageID <= 0 {
		return nil, errors.New("invalid message id")
	}

	rows, err := r.DB.QueryContext(ctx, `
		SELECT
			mr.user_id,
			COALESCE(u.full_name, u.username) AS full_name,
			u.avatar_url,
			mr.reaction,
			mr.created_at
		FROM message_reactions mr
		JOIN users u ON u.id = mr.user_id
		WHERE mr.message_id = ?
		ORDER BY mr.created_at ASC, mr.id ASC
	`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ReactionUserItem
	for rows.Next() {
		var it ReactionUserItem
		if err := rows.Scan(&it.UserID, &it.FullName, &it.AvatarURL, &it.Reaction, &it.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// =========================
// HELPERS
// =========================

func buildInt64InClause(ids []int64) (placeholders string, args []any) {
	// (?, ?, ?, ...)
	sb := strings.Builder{}
	args = make([]any, 0, len(ids))

	for i, id := range ids {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("?")
		args = append(args, id)
	}
	return sb.String(), args
}

// RemoveAllReactionsByUser: remove tất cả reaction của user trên message
func (r *Repository) RemoveAllReactionsByUser(ctx context.Context, messageID, userID int64) error {
	if messageID <= 0 || userID <= 0 {
		return errors.New("invalid input")
	}
	_, err := r.DB.ExecContext(ctx, `
		DELETE FROM message_reactions
		WHERE message_id = ? AND user_id = ?
	`, messageID, userID)
	return err
}

func (r *Repository) GetMessageRoomID(ctx context.Context, messageID int64) (int64, error) {
	if messageID <= 0 {
		return 0, errors.New("invalid message id")
	}

	var roomID int64
	err := r.DB.QueryRowContext(ctx, `
		SELECT room_id
		FROM messages
		WHERE id = ?
	`, messageID).Scan(&roomID)
	if err != nil {
		return 0, err
	}
	return roomID, nil
}

// ========== RECEIPTS TYPES ==========

type ReceiptStatus string

const (
	ReceiptDelivered ReceiptStatus = "delivered"
	ReceiptSeen      ReceiptStatus = "seen"
)

// Trả về user đã seen (cho UI "Seen by ...")
type SeenUser struct {
	UserID    int64     `json:"user_id"`
	FullName  string    `json:"full_name"`
	AvatarURL string    `json:"avatar_url,omitempty"`
	SeenAt    time.Time `json:"seen_at"`
}

// Tóm tắt theo message (count + mình đã seen chưa)
type MessageSeenSummary struct {
	MessageID  int64 `json:"message_id"`
	SeenCount  int64 `json:"seen_count"`
	SeenByMe   bool  `json:"seen_by_me"`
	TotalUsers int64 `json:"total_users,omitempty"` // optional nếu mày muốn hiển thị x/y
}

// ========== UPSERT HELPERS ==========

func (r *Repository) SetDelivered(ctx context.Context, roomID, messageID, userID int64) error {
	if roomID <= 0 || messageID <= 0 || userID <= 0 {
		return errors.New("invalid input")
	}

	// ✅ nếu đã seen rồi thì KHÔNG downgrade về delivered
	_, err := r.DB.ExecContext(ctx, `
		INSERT INTO message_receipts (room_id, message_id, user_id, status, seen_at)
		VALUES (?, ?, ?, 'delivered', NOW())
		ON DUPLICATE KEY UPDATE
			status = IF(status = 'seen', 'seen', 'delivered'),
			seen_at = IF(status = 'seen', seen_at, GREATEST(seen_at, VALUES(seen_at)))
	`, roomID, messageID, userID)

	return err
}

func (r *Repository) SetSeen(ctx context.Context, roomID, messageID, userID int64) error {
	if roomID <= 0 || messageID <= 0 || userID <= 0 {
		return errors.New("invalid input")
	}

	_, err := r.DB.ExecContext(ctx, `
		INSERT INTO message_receipts (room_id, message_id, user_id, status, seen_at)
		VALUES (?, ?, ?, 'seen', NOW())
		ON DUPLICATE KEY UPDATE
			status = 'seen',
			seen_at = GREATEST(seen_at, VALUES(seen_at))
	`, roomID, messageID, userID)

	return err
}

// ========== BULK MARK SEEN (UP TO MESSAGE) ==========
// MarkRoomSeenUpTo: set seen cho tất cả messages trong room có id <= upToMessageID
// - default: skip message do chính user gửi (thường UI không cần receipt cho msg của mình)
func (r *Repository) MarkRoomSeenUpTo(ctx context.Context, roomID, userID, upToMessageID int64) (affected int64, err error) {
	if roomID <= 0 || userID <= 0 || upToMessageID <= 0 {
		return 0, errors.New("invalid input")
	}

	res, err := r.DB.ExecContext(ctx, `
		INSERT INTO message_receipts (room_id, message_id, user_id, status, seen_at)
		SELECT m.room_id, m.id, ?, 'seen', NOW()
		FROM messages m
		WHERE m.room_id = ?
		  AND m.id <= ?
		  AND m.sender_id <> ?
		ON DUPLICATE KEY UPDATE
			status = 'seen',
			seen_at = GREATEST(seen_at, VALUES(seen_at))
	`, userID, roomID, upToMessageID, userID)
	if err != nil {
		return 0, err
	}

	ra, _ := res.RowsAffected()
	return ra, nil
}

// ========== QUERIES ==========

// GetReceiptStatus: lấy status hiện tại của user trên 1 message (không có row -> delivered/seen tuỳ logic của mày)
func (r *Repository) GetReceiptStatus(ctx context.Context, messageID, userID int64) (ReceiptStatus, *time.Time, error) {
	if messageID <= 0 || userID <= 0 {
		return "", nil, errors.New("invalid input")
	}

	var st string
	var t time.Time
	err := r.DB.QueryRowContext(ctx, `
		SELECT status, seen_at
		FROM message_receipts
		WHERE message_id = ? AND user_id = ?
		LIMIT 1
	`, messageID, userID).Scan(&st, &t)

	if err == sql.ErrNoRows {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	tt := t
	return ReceiptStatus(st), &tt, nil
}

// CountSeenByMessage: đếm số người seen 1 message (thường exclude sender)
func (r *Repository) CountSeenByMessage(ctx context.Context, messageID int64, excludeUserID int64) (int64, error) {
	if messageID <= 0 {
		return 0, errors.New("invalid input")
	}

	var c int64
	if excludeUserID > 0 {
		err := r.DB.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM message_receipts
			WHERE message_id = ?
			  AND status = 'seen'
			  AND user_id <> ?
		`, messageID, excludeUserID).Scan(&c)
		return c, err
	}

	err := r.DB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM message_receipts
		WHERE message_id = ?
		  AND status = 'seen'
	`, messageID).Scan(&c)
	return c, err
}

// HasSeenMessage: user đã seen message chưa
func (r *Repository) HasSeenMessage(ctx context.Context, messageID, userID int64) (bool, error) {
	if messageID <= 0 || userID <= 0 {
		return false, errors.New("invalid input")
	}

	var ok int
	err := r.DB.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1
			FROM message_receipts
			WHERE message_id = ? AND user_id = ? AND status = 'seen'
			LIMIT 1
		)
	`, messageID, userID).Scan(&ok)
	return ok == 1, err
}

// ListSeenUsersByMessage: list người đã seen message (kèm full_name/avatar_url)
func (r *Repository) ListSeenUsersByMessage(ctx context.Context, messageID int64, excludeUserID int64, limit int) ([]SeenUser, error) {
	if messageID <= 0 {
		return nil, errors.New("invalid input")
	}
	if limit <= 0 {
		limit = 50
	}

	rows, err := r.DB.QueryContext(ctx, `
		SELECT r.user_id,
		       COALESCE(u.full_name, u.username) AS full_name,
		       COALESCE(u.avatar_url, '') AS avatar_url,
		       r.seen_at
		FROM message_receipts r
		JOIN users u ON u.id = r.user_id
		WHERE r.message_id = ?
		  AND r.status = 'seen'
		  AND (? = 0 OR r.user_id <> ?)
		ORDER BY r.seen_at DESC
		LIMIT ?
	`, messageID, excludeUserID, excludeUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]SeenUser, 0, 16)
	for rows.Next() {
		var it SeenUser
		if err := rows.Scan(&it.UserID, &it.FullName, &it.AvatarURL, &it.SeenAt); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetRoomLastSeenMessageID: last message_id mà user đã seen trong room (dựa trên receipts)
func (r *Repository) GetRoomLastSeenMessageID(ctx context.Context, roomID, userID int64) (int64, *time.Time, error) {
	if roomID <= 0 || userID <= 0 {
		return 0, nil, errors.New("invalid input")
	}

	var lastID sql.NullInt64
	var lastAt sql.NullTime
	err := r.DB.QueryRowContext(ctx, `
		SELECT MAX(message_id) AS last_message_id,
		       MAX(seen_at)    AS last_seen_at
		FROM message_receipts
		WHERE room_id = ?
		  AND user_id = ?
		  AND status = 'seen'
	`, roomID, userID).Scan(&lastID, &lastAt)

	if err != nil {
		return 0, nil, err
	}
	if !lastID.Valid {
		return 0, nil, nil
	}
	var t *time.Time
	if lastAt.Valid {
		tt := lastAt.Time
		t = &tt
	}
	return lastID.Int64, t, nil
}

// GetMessageSeenSummary: tiện cho API response (count + seen_by_me)
func (r *Repository) GetMessageSeenSummary(ctx context.Context, messageID, meUserID int64, excludeUserID int64) (MessageSeenSummary, error) {
	if messageID <= 0 || meUserID <= 0 {
		return MessageSeenSummary{}, errors.New("invalid input")
	}

	var seenCount int64
	var seenByMe int64

	// count seen (exclude sender nếu truyền excludeUserID)
	if excludeUserID > 0 {
		if err := r.DB.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM message_receipts
			WHERE message_id = ? AND status = 'seen' AND user_id <> ?
		`, messageID, excludeUserID).Scan(&seenCount); err != nil {
			return MessageSeenSummary{}, err
		}
	} else {
		if err := r.DB.QueryRowContext(ctx, `
			SELECT COUNT(*)
			FROM message_receipts
			WHERE message_id = ? AND status = 'seen'
		`, messageID).Scan(&seenCount); err != nil {
			return MessageSeenSummary{}, err
		}
	}

	if err := r.DB.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM message_receipts
			WHERE message_id = ? AND user_id = ? AND status = 'seen'
			LIMIT 1
		)
	`, messageID, meUserID).Scan(&seenByMe); err != nil {
		return MessageSeenSummary{}, err
	}

	return MessageSeenSummary{
		MessageID: messageID,
		SeenCount: seenCount,
		SeenByMe:  seenByMe == 1,
	}, nil
}

// internal/chat/repository_receipts.go (hoặc repository_messages.go)
func (r *Repository) GetMessageRoomAndSender(ctx context.Context, messageID int64) (roomID int64, senderID int64, err error) {
	err = r.DB.QueryRowContext(ctx, `SELECT room_id, sender_id FROM messages WHERE id=? LIMIT 1`, messageID).
		Scan(&roomID, &senderID)
	if err == sql.ErrNoRows {
		return 0, 0, ErrMessageNotFound
	}
	return
}

// ===============================
// 1) Recipients for notification
// ===============================

// List member user_ids in a room, excluding sender (for WS notify, badge unread...)
func (r *Repository) ListRoomMemberUserIDsExcept(ctx context.Context, roomID, excludeUserID int64) ([]int64, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT user_id
		FROM room_members
		WHERE room_id = ? AND user_id <> ?
	`, roomID, excludeUserID)
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

// ===============================
// 2) Unread count (DB truth)
// ===============================

// Unread of 1 room for 1 user
// rule: messages.created_at > rm.last_seen_at AND sender_id != user AND message_type != 'system'
func (r *Repository) GetUnreadCount(ctx context.Context, roomID, userID int64) (int64, error) {
	var lastSeen sql.NullTime
	err := r.DB.QueryRowContext(ctx, `
		SELECT last_seen_at
		FROM room_members
		WHERE room_id = ? AND user_id = ?
	`, roomID, userID).Scan(&lastSeen)
	if err != nil {
		return 0, err
	}

	// If never seen -> treat as "very old" => count all non-system messages not from me
	seenAt := time.Unix(0, 0)
	if lastSeen.Valid {
		seenAt = lastSeen.Time
	}

	var cnt int64
	err = r.DB.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM messages
		WHERE room_id = ?
		  AND message_type <> 'system'
		  AND sender_id <> ?
		  AND created_at > ?
	`, roomID, userID, seenAt).Scan(&cnt)
	return cnt, err
}

// Unread counts for sidebar: return map room_id -> unread_count
func (r *Repository) GetUnreadCountsByRooms(ctx context.Context, userID int64) (map[int64]int64, error) {
	rows, err := r.DB.QueryContext(ctx, `
		SELECT
			rm.room_id,
			COUNT(m.id) AS unread_count
		FROM room_members rm
		LEFT JOIN messages m
		  ON m.room_id = rm.room_id
		 AND m.message_type <> 'system'
		 AND m.sender_id <> rm.user_id
		 AND m.created_at > COALESCE(rm.last_seen_at, '1970-01-01 00:00:00')
		WHERE rm.user_id = ?
		GROUP BY rm.room_id
		HAVING COUNT(m.id) > 0

	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]int64)
	for rows.Next() {
		var roomID, cnt int64
		if err := rows.Scan(&roomID, &cnt); err != nil {
			return nil, err
		}
		out[roomID] = cnt
	}
	return out, rows.Err()
}
