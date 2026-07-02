package chat

import (
	"context"
	"database/sql"
	"time"
)

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
