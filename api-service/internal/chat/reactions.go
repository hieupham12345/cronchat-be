package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

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
