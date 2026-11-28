package auth_test

import (
	"testing"

	"github.com/exzco/bbs/internal/models"
)

func TestCheckPasswordGoHash(t *testing.T) {
	var u models.User
	if err := u.SetPassword("s3cret-password"); err != nil {
		t.Fatal(err)
	}
	if !u.CheckPassword("s3cret-password") {
		t.Fatal("valid password rejected")
	}
	if u.CheckPassword("wrong") {
		t.Fatal("wrong password accepted")
	}
}

// TestCheckPasswordPHPHash 验证从 Flarum 导入的 PHP bcrypt 哈希（$2y$ 前缀）可直接登录。
func TestCheckPasswordPHPHash(t *testing.T) {
	// 先生成 $2a$ 哈希再替换为 PHP 的 $2y$ 前缀
	var u models.User
	if err := u.SetPassword("secret123"); err != nil {
		t.Fatal(err)
	}
	u.Password = "$2y$" + u.Password[4:]
	if !u.CheckPassword("secret123") {
		t.Fatal("$2y$ prefixed hash rejected")
	}
	if u.CheckPassword("wrong") {
		t.Fatal("wrong password accepted for $2y$ hash")
	}
}
