package chat

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func newMock(t *testing.T) (*Repository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &Repository{DB: db}, mock
}

func TestGetReactionSummary(t *testing.T) {
	repo, mock := newMock(t)
	rows := sqlmock.NewRows([]string{"reaction", "cnt", "reacted_by_me"}).
		AddRow("👍", 3, 1).
		AddRow("❤️", 1, 0)
	mock.ExpectQuery("FROM message_reactions").
		WithArgs(int64(99), int64(5)).
		WillReturnRows(rows)

	items, err := repo.GetReactionSummary(context.Background(), 5, 99)
	if err != nil {
		t.Fatalf("GetReactionSummary: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Reaction != "👍" || items[0].Count != 3 || !items[0].ReactedByMe {
		t.Errorf("row0 wrong: %+v", items[0])
	}
	if items[1].ReactedByMe {
		t.Errorf("row1 reacted_by_me should be false: %+v", items[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestGetReactionSummaryInvalidID(t *testing.T) {
	repo, _ := newMock(t)
	if _, err := repo.GetReactionSummary(context.Background(), 0, 1); err == nil {
		t.Fatal("expected error for messageID 0")
	}
}

func TestToggleReactionInsertPath(t *testing.T) {
	repo, mock := newMock(t)
	// INSERT IGNORE affects 1 row => newly added => added=true, no DELETE.
	mock.ExpectExec("INSERT IGNORE INTO message_reactions").
		WithArgs(int64(5), int64(9), "👍").
		WillReturnResult(sqlmock.NewResult(1, 1))

	added, err := repo.ToggleReaction(context.Background(), 5, 9, "👍")
	if err != nil {
		t.Fatalf("ToggleReaction: %v", err)
	}
	if !added {
		t.Error("expected added=true on fresh insert")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestToggleReactionRemovePath(t *testing.T) {
	repo, mock := newMock(t)
	// INSERT IGNORE affects 0 rows (already exists) => DELETE => added=false.
	mock.ExpectExec("INSERT IGNORE INTO message_reactions").
		WithArgs(int64(5), int64(9), "👍").
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec("DELETE FROM message_reactions").
		WithArgs(int64(5), int64(9), "👍").
		WillReturnResult(sqlmock.NewResult(0, 1))

	added, err := repo.ToggleReaction(context.Background(), 5, 9, "👍")
	if err != nil {
		t.Fatalf("ToggleReaction: %v", err)
	}
	if added {
		t.Error("expected added=false on toggle-off")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestGetUnreadCountNeverSeen(t *testing.T) {
	repo, mock := newMock(t)
	// last_seen_at NULL -> treated as epoch; then COUNT(*) of unread.
	mock.ExpectQuery("SELECT last_seen_at").
		WithArgs(int64(3), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"last_seen_at"}).AddRow(nil))
	mock.ExpectQuery("SELECT COUNT").
		WillReturnRows(sqlmock.NewRows([]string{"cnt"}).AddRow(4))

	cnt, err := repo.GetUnreadCount(context.Background(), 3, 9)
	if err != nil {
		t.Fatalf("GetUnreadCount: %v", err)
	}
	if cnt != 4 {
		t.Errorf("expected 4, got %d", cnt)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestMarkRoomSeenUpToInvalidInput(t *testing.T) {
	repo, _ := newMock(t)
	if _, err := repo.MarkRoomSeenUpTo(context.Background(), 0, 1, 1); err == nil {
		t.Fatal("expected error for invalid roomID")
	}
}
