package handler_test

import (
	"testing"

	"github.com/exzco/bbs/internal/models"
	"github.com/exzco/bbs/internal/service"
)

func TestLegacyAvatarURL(t *testing.T) {
	var svc service.Service
	for _, tc := range []struct{ stored, want string }{
		{"abc.png", "/assets/avatars/abc.png"},
		{"/avatars/abc.png", "/avatars/abc.png"},
		{"/assets/avatars/abc.png", "/assets/avatars/abc.png"},
		{"https://example.org/a.png", "https://example.org/a.png"},
		{"javascript:alert(1)", "/avatar/test.svg"},
		{"../secret", "/avatar/test.svg"},
		{"", "/avatar/test.svg"},
	} {
		if got := svc.AvatarURL(&models.User{Username: "test", AvatarURL: tc.stored}); got != tc.want {
			t.Errorf("%q: got %q want %q", tc.stored, got, tc.want)
		}
	}
}
