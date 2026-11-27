package auth_test

import (
	"testing"

	"github.com/exzco/bbs/internal/auth"
)

func TestCheckPasswordGoHash(t *testing.T) {
	h, err := auth.HashPassword("s3cret-password")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(h, "s3cret-password") {
		t.Fatal("valid password rejected")
	}
	if auth.CheckPassword(h, "wrong") {
		t.Fatal("wrong password accepted")
	}
}

// TestCheckPasswordPHPHash 验证从 Flarum 导入的 PHP bcrypt 哈希（$2y$ 前缀）可直接登录。
func TestCheckPasswordPHPHash(t *testing.T) {
	// 先生成 $2a$ 哈希再替换为 PHP 的 $2y$ 前缀
	h, _ := auth.HashPassword("secret123")
	y := "$2y$" + h[4:]
	if !auth.CheckPassword(y, "secret123") {
		t.Fatal("$2y$ prefixed hash rejected")
	}
	if auth.CheckPassword(y, "wrong") {
		t.Fatal("wrong password accepted for $2y$ hash")
	}
}
