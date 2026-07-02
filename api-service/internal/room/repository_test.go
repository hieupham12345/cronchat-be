package room

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

func TestIsUserInRoom(t *testing.T) {
	repo, mock := newMock(t)
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(int64(3), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(1))

	ok, err := repo.IsUserInRoom(3, 9)
	if err != nil {
		t.Fatalf("IsUserInRoom: %v", err)
	}
	if !ok {
		t.Error("expected user in room")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestIsUserInRoomNotMember(t *testing.T) {
	repo, mock := newMock(t)
	mock.ExpectQuery("SELECT COUNT").
		WithArgs(int64(3), int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(0))

	ok, err := repo.IsUserInRoom(3, 9)
	if err != nil {
		t.Fatalf("IsUserInRoom: %v", err)
	}
	if ok {
		t.Error("expected user NOT in room")
	}
}

func TestGetRoomMemberIDs(t *testing.T) {
	repo, mock := newMock(t)
	mock.ExpectQuery("SELECT user_id FROM room_members").
		WithArgs(int64(3)).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(1).AddRow(2).AddRow(7))

	ids, err := repo.GetRoomMemberIDs(3)
	if err != nil {
		t.Fatalf("GetRoomMemberIDs: %v", err)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[2] != 7 {
		t.Errorf("unexpected ids: %v", ids)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestMarkRoomSeenUpToInvalidNoop(t *testing.T) {
	repo, _ := newMock(t)
	// invalid input -> returns nil without touching DB (no expectations set).
	if err := repo.MarkRoomSeenUpTo(context.Background(), 0, 1, 1); err != nil {
		t.Errorf("expected nil for invalid input, got %v", err)
	}
}

func TestMarkRoomSeenUpToUpdates(t *testing.T) {
	repo, mock := newMock(t)
	mock.ExpectExec("UPDATE room_members").
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.MarkRoomSeenUpTo(context.Background(), 3, 9, 100); err != nil {
		t.Fatalf("MarkRoomSeenUpTo: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
