package chat

import (
	"database/sql"
	"strings"
	"testing"
)

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func TestBuildReplyPreview(t *testing.T) {
	if got := buildReplyPreview("image", sql.NullString{}); got != "📷 Image" {
		t.Errorf("image: got %q", got)
	}
	if got := buildReplyPreview("file", sql.NullString{}); got != "📎 File" {
		t.Errorf("file: got %q", got)
	}
	if got := buildReplyPreview("text", ns("  hello  ")); got != "hello" {
		t.Errorf("text trims: got %q", got)
	}
	if got := buildReplyPreview("text", ns("")); got != "" {
		t.Errorf("empty text: got %q", got)
	}
	if got := buildReplyPreview("text", sql.NullString{Valid: false}); got != "" {
		t.Errorf("null content: got %q", got)
	}
}

func TestBuildReplyPreviewTruncatesTo300Runes(t *testing.T) {
	long := strings.Repeat("あ", 500) // multi-byte runes
	got := buildReplyPreview("text", ns(long))
	if n := len([]rune(got)); n != 300 {
		t.Errorf("expected 300 runes, got %d", n)
	}
}

func TestPickName(t *testing.T) {
	if got := pickName(ns("Full Name"), ns("uname")); got != "Full Name" {
		t.Errorf("prefers full name: got %q", got)
	}
	if got := pickName(sql.NullString{Valid: false}, ns("uname")); got != "uname" {
		t.Errorf("falls back to username: got %q", got)
	}
	if got := pickName(ns("   "), ns("uname")); got != "uname" {
		t.Errorf("blank full name falls back: got %q", got)
	}
	if got := pickName(sql.NullString{}, sql.NullString{}); got != "Unknown" {
		t.Errorf("both empty: got %q", got)
	}
}

func TestNullIfEmpty(t *testing.T) {
	if got := nullIfEmpty("  "); got.Valid {
		t.Error("whitespace-only must be NULL")
	}
	got := nullIfEmpty("  x  ")
	if !got.Valid || got.String != "x" {
		t.Errorf("expected trimmed valid 'x', got %+v", got)
	}
}

func TestBuildInt64InClause(t *testing.T) {
	ph, args := buildInt64InClause([]int64{10, 20, 30})
	if ph != "?,?,?" {
		t.Errorf("placeholders: got %q", ph)
	}
	if len(args) != 3 || args[0] != int64(10) || args[2] != int64(30) {
		t.Errorf("args: got %v", args)
	}

	ph, args = buildInt64InClause(nil)
	if ph != "" || len(args) != 0 {
		t.Errorf("empty input: got ph=%q args=%v", ph, args)
	}
}
