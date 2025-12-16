package user

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"strings"
)

type Repository struct {
	DB *sql.DB
}

type User struct {
	ID       int
	Username string
	Password string
	Role     string

	Full_name sql.NullString
	Email     sql.NullString
	Phone     sql.NullString
	AvatarURL sql.NullString

	Is_active  int
	Last_login sql.NullString
	Login_ip   sql.NullString

	Created_ip sql.NullString
	Created_at string
	Updated_at string
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{DB: db}
}

func (r *Repository) UpdateLoginAudit(username, ip, lastLogin string) error {
	_, err := r.DB.Exec(`
		UPDATE users
		SET 
			login_ip  = ?,
			last_login = ?,          -- 👈 cập nhật last_login
			updated_at = CURRENT_TIMESTAMP
		WHERE username = ?;
	`, ip, lastLogin, username)
	return err
}

// FindByUsername: dùng cho login
func (r *Repository) FindByUsername(username string) (*User, error) {
	row := r.DB.QueryRow(
		"SELECT * FROM users WHERE username = ?",
		username,
	)

	var u User
	err := row.Scan(
		&u.ID,
		&u.Username,
		&u.Password,
		&u.Role,
		&u.Full_name,
		&u.Email,
		&u.Phone,
		&u.AvatarURL,
		&u.Is_active,
		&u.Last_login,
		&u.Login_ip,
		&u.Created_ip,
		&u.Created_at,
		&u.Updated_at,
	)

	if err != nil {
		return nil, err
	}

	return &u, nil
}

func (r *Repository) CreateUser(u *User) (int64, error) {
	if u.Is_active == 0 {
		u.Is_active = 1
	}

	res, err := r.DB.Exec(
		`INSERT INTO users (
			username, password, role,
			full_name, email, phone, avatar_url,
			is_active, last_login, login_ip, created_ip
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		u.Username,
		u.Password,
		u.Role,
		u.Full_name,
		u.Email,
		u.Phone,
		u.AvatarURL,
		u.Is_active,
		u.Last_login,
		u.Login_ip,
		u.Created_ip,
	)
	if err != nil {
		return 0, err
	}

	return res.LastInsertId()
}

// GetUserByID: lấy thông tin 1 user theo id
func (r *Repository) GetUserByID(id int) (*User, error) {
	row := r.DB.QueryRow(
		"SELECT * FROM users WHERE id = ?",
		id,
	)

	var u User
	err := row.Scan(
		&u.ID,
		&u.Username,
		&u.Password,
		&u.Role,
		&u.Full_name,
		&u.Email,
		&u.Phone,
		&u.AvatarURL,
		&u.Is_active,
		&u.Last_login,
		&u.Login_ip,
		&u.Created_ip,
		&u.Created_at,
		&u.Updated_at,
	)
	if err != nil {
		return nil, err
	}

	return &u, nil
}

// GetAllUsers
func (r *Repository) GetAllUsers() ([]*User, error) {
	rows, err := r.DB.Query("SELECT * FROM users order by username asc LIMIT 20")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User

	for rows.Next() {
		var u User
		err := rows.Scan(
			&u.ID,
			&u.Username,
			&u.Password,
			&u.Role,
			&u.Full_name,
			&u.Email,
			&u.Phone,
			&u.AvatarURL,
			&u.Is_active,
			&u.Last_login,
			&u.Login_ip,
			&u.Created_ip,
			&u.Created_at,
			&u.Updated_at,
		)
		if err != nil {
			return nil, err
		}

		users = append(users, &u)
	}

	return users, nil
}

// GetAllUsers
func (r *Repository) GetAllUsersForListing() ([]*User, error) {
	rows, err := r.DB.Query("SELECT id, username, full_name, avatar_url FROM users LIMIT 100")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User

	for rows.Next() {
		var u User
		err := rows.Scan(
			&u.ID,
			&u.Username,
			&u.Full_name,
			&u.AvatarURL,
		)
		if err != nil {
			return nil, err
		}

		users = append(users, &u)
	}

	return users, nil
}

// UpdateUser
func (r *Repository) UpdateUserDynamic(id int64, fields map[string]interface{}) error {
	if len(fields) == 0 {
		return errors.New("no fields to update")
	}

	query := "UPDATE users SET "
	args := []interface{}{}
	i := 0

	for k, v := range fields {
		query += k + " = ?"
		if i < len(fields)-1 {
			query += ", "
		}
		args = append(args, v)
		i++
	}

	query += " WHERE id = ?"
	args = append(args, id)

	log.Printf(query) // 👈 DÒNG NÀY

	_, err := r.DB.Exec(query, args...)
	return err
}

// SearchUsers: search theo username hoặc full_name (prefix match)
func (r *Repository) SearchUsers(keyword string, limit int) ([]*User, error) {
	// Nếu keyword trống thì trả về rỗng, tránh query linh tinh
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []*User{}, nil
	}
	if limit <= 0 {
		limit = 20
	}

	like := keyword + "%"

	query := `
		SELECT 
			id,
			username,
			full_name,
			avatar_url
		FROM users
		WHERE is_active = 1
		  AND (
			   username  LIKE ?
			OR full_name LIKE ?
		  )
		ORDER BY username
		LIMIT ?;
	`

	rows, err := r.DB.Query(query, like, like, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []*User

	for rows.Next() {
		var u User
		err := rows.Scan(
			&u.ID,
			&u.Username,
			&u.Full_name,
			&u.AvatarURL,
		)
		if err != nil {
			return nil, err
		}
		users = append(users, &u)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return users, nil
}

func (r *Repository) UpdateAvatar(userID int, avatarURL string) error {
	_, err := r.DB.Exec(`
		UPDATE users
		SET 
			avatar_url = ?,
			updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, avatarURL, userID)

	return err
}

// ==========================
// UserBrief
// ==========================
type UserBrief struct {
	ID        int64  `json:"id"`
	FullName  string `json:"full_name"`
	AvatarURL string `json:"avatar_url"`
}

// ==========================
// GetUserBrief
// ==========================
func (r *Repository) GetUserBrief(ctx context.Context, userID int64) (*UserBrief, error) {
	if userID <= 0 {
		return nil, sql.ErrNoRows
	}

	var u UserBrief
	err := r.DB.QueryRowContext(ctx, `
		SELECT id, COALESCE(full_name,''), COALESCE(avatar_url,'')
		FROM users
		WHERE id = ?
		LIMIT 1
	`, userID).Scan(&u.ID, &u.FullName, &u.AvatarURL)
	if err != nil {
		return nil, err
	}
	return &u, nil
}
