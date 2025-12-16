package httpserver

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Phân loại token
type TokenType string

const (
	TokenTypeAccess  TokenType = "access"
	TokenTypeRefresh TokenType = "refresh"
)

// TTL cho từng loại token
const (
	AccessTokenTTL  = 10 * time.Minute   // access token sống 10 phút
	RefreshTokenTTL = 7 * 24 * time.Hour // refresh token sống 7 ngày (tùy chỉnh)
)

// Claims custom, muốn gì thêm vào đây
type Claims struct {
	UserID    int       `json:"user_id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	TokenType TokenType `json:"token_type"` // access | refresh
	jwt.RegisteredClaims
}

// GenerateAccessToken tạo JWT access token
func GenerateAccessToken(userID int, username string, role string, secret []byte) (string, error) {
	now := time.Now()

	claims := Claims{
		UserID:    userID,
		Username:  username,
		Role:      role,
		TokenType: TokenTypeAccess,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(AccessTokenTTL)),
			Issuer:    "cronhustler-api",
			Subject:   username,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// GenerateRefreshToken tạo JWT refresh token
func GenerateRefreshToken(userID int, username string, secret []byte) (string, error) {
	now := time.Now()

	claims := Claims{
		UserID:    userID,
		Username:  username,
		TokenType: TokenTypeRefresh,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(RefreshTokenTTL)),
			Issuer:    "cronhustler-api",
			Subject:   username,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

// ParseToken verify + parse JWT
func ParseToken(tokenStr string, secret []byte) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		// Chắc cú: chỉ chấp nhận HS256
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrTokenSignatureInvalid
		}
		return secret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, jwt.ErrTokenInvalidClaims
	}

	return claims, nil
}
