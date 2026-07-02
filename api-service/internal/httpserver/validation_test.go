package httpserver

import "testing"

func TestIsValidEmail(t *testing.T) {
	valid := []string{"a@b.co", "user.name+tag@example.com", "x_y@sub.domain.io"}
	invalid := []string{"", "no-at", "a@b", "a@b.c", "@b.com", "a@.com", "a b@c.com"}
	for _, e := range valid {
		if !isValidEmail(e) {
			t.Errorf("expected %q to be valid", e)
		}
	}
	for _, e := range invalid {
		if isValidEmail(e) {
			t.Errorf("expected %q to be invalid", e)
		}
	}
}

func TestIsValidPhone(t *testing.T) {
	// rule: leading 0 + exactly 10 digits total
	valid := []string{"0123456789", "0987654321"}
	invalid := []string{"", "123456789", "01234567890", "0abcdefghi", "1123456789"}
	for _, p := range valid {
		if !isValidPhone(p) {
			t.Errorf("expected %q to be valid", p)
		}
	}
	for _, p := range invalid {
		if isValidPhone(p) {
			t.Errorf("expected %q to be invalid", p)
		}
	}
}

func TestIsAllowedImageMime(t *testing.T) {
	allowed := []string{"image/jpeg", "image/png", "image/webp", "image/gif", "IMAGE/PNG"}
	denied := []string{"", "text/plain", "application/pdf", "image/svg+xml"}
	for _, m := range allowed {
		if !isAllowedImageMime(m) {
			t.Errorf("expected %q allowed", m)
		}
	}
	for _, m := range denied {
		if isAllowedImageMime(m) {
			t.Errorf("expected %q denied", m)
		}
	}
}

func TestMimeToExt(t *testing.T) {
	cases := map[string]string{
		"image/png":  ".png",
		"image/webp": ".webp",
		"image/gif":  ".gif",
		"image/jpeg": ".jpg", // default
		"unknown":    ".jpg",
	}
	for mime, want := range cases {
		if got := mimeToExt(mime); got != want {
			t.Errorf("mimeToExt(%q): got %q, want %q", mime, got, want)
		}
	}
}
