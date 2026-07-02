package user

import (
	"context"
	"regexp"
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

func TestFindByUsername(t *testing.T) {
	repo, mock := newMock(t)

	cols := []string{
		"id", "username", "password", "role",
		"full_name", "email", "phone", "avatar_url",
		"is_active", "last_login", "login_ip",
		"created_ip", "created_at", "updated_at",
	}
	rows := sqlmock.NewRows(cols).AddRow(
		1, "alice", "hashed", "admin",
		"Alice", "a@b.co", "0123456789", nil,
		1, nil, nil,
		nil, "2024-01-01", "2024-01-02",
	)
	mock.ExpectQuery("SELECT \\* FROM users WHERE username = ?").
		WithArgs("alice").
		WillReturnRows(rows)

	u, err := repo.FindByUsername("alice")
	if err != nil {
		t.Fatalf("FindByUsername: %v", err)
	}
	if u.ID != 1 || u.Username != "alice" || u.Role != "admin" {
		t.Errorf("unexpected user: %+v", u)
	}
	if !u.Email.Valid || u.Email.String != "a@b.co" {
		t.Errorf("email scan wrong: %+v", u.Email)
	}
	if u.AvatarURL.Valid {
		t.Errorf("expected NULL avatar_url")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestUpdateAvatar(t *testing.T) {
	repo, mock := newMock(t)
	mock.ExpectExec("UPDATE users").
		WithArgs("/static/user_avatars/x.jpg", 7).
		WillReturnResult(sqlmock.NewResult(0, 1))

	if err := repo.UpdateAvatar(7, "/static/user_avatars/x.jpg"); err != nil {
		t.Fatalf("UpdateAvatar: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestUpdateUserDynamicNoFields(t *testing.T) {
	repo, _ := newMock(t)
	if err := repo.UpdateUserDynamic(1, map[string]interface{}{}); err == nil {
		t.Fatal("expected error for empty fields")
	}
}

func TestUpdateUserDynamicBuildsUpdate(t *testing.T) {
	repo, mock := newMock(t)
	// map ordering is non-deterministic; match loosely.
	mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UpdateUserDynamic(3, map[string]interface{}{"email": "n@e.co"})
	if err != nil {
		t.Fatalf("UpdateUserDynamic: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestSearchUsersEmptyKeyword(t *testing.T) {
	repo, _ := newMock(t)
	users, err := repo.SearchUsers("   ", 10)
	if err != nil {
		t.Fatalf("SearchUsers: %v", err)
	}
	if len(users) != 0 {
		t.Errorf("expected empty result, got %d", len(users))
	}
}

func TestGetUserBriefInvalidID(t *testing.T) {
	repo, _ := newMock(t)
	if _, err := repo.GetUserBrief(context.Background(), 0); err == nil {
		t.Fatal("expected error for userID 0")
	}
}
