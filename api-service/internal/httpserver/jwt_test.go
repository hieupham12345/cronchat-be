package httpserver

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

var testSecret = []byte("test-secret-key")

func TestAccessTokenRoundTrip(t *testing.T) {
	tok, err := GenerateAccessToken(42, "alice", "admin", testSecret)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	claims, err := ParseToken(tok, testSecret)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.UserID != 42 || claims.Username != "alice" || claims.Role != "admin" {
		t.Errorf("unexpected claims: %+v", claims)
	}
	if claims.TokenType != TokenTypeAccess {
		t.Errorf("token type: got %q, want %q", claims.TokenType, TokenTypeAccess)
	}
}

func TestRefreshTokenType(t *testing.T) {
	tok, err := GenerateRefreshToken(7, "bob", testSecret)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	claims, err := ParseToken(tok, testSecret)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if claims.TokenType != TokenTypeRefresh {
		t.Errorf("token type: got %q, want %q", claims.TokenType, TokenTypeRefresh)
	}
}

func TestParseTokenWrongSecret(t *testing.T) {
	tok, _ := GenerateAccessToken(1, "x", "user", testSecret)
	if _, err := ParseToken(tok, []byte("other-secret")); err == nil {
		t.Fatal("expected error parsing with wrong secret")
	}
}

func TestParseTokenRejectsNonHMAC(t *testing.T) {
	// A token signed with "none" must be rejected by our HS256-only parser.
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{UserID: 1})
	s, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := ParseToken(s, testSecret); err == nil {
		t.Fatal("expected non-HMAC token to be rejected")
	}
}

func TestHashPasswordDeterministic(t *testing.T) {
	a := hashPassword("hunter2")
	b := hashPassword("hunter2")
	if a != b {
		t.Error("hashPassword must be deterministic")
	}
	if a == hashPassword("different") {
		t.Error("different passwords must hash differently")
	}
	// sha256 hex is 64 chars
	if len(a) != 64 {
		t.Errorf("expected 64-char hex, got %d", len(a))
	}
}
