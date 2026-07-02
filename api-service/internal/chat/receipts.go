package chat

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

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
