// Package mail 极简 SMTP 发信客户端（零第三方依赖）：
// 465 端口走隐式 TLS，其余端口服务器支持时升级 STARTTLS；配置了用户名时启用 PLAIN 认证。
package mail

import (
	"crypto/tls"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

// Sender SMTP 发信配置与客户端。
type Sender struct {
	Host     string
	Port     int
	Username string
	Password string
	From     string
}

// Send 发送一封 UTF-8 纯文本邮件。
func (s *Sender) Send(to, subject, body string) error {
	conn, err := s.dial()
	if err != nil {
		return err
	}
	defer conn.Close()
	c, err := smtp.NewClient(conn, s.Host)
	if err != nil {
		return err
	}
	defer c.Close()

	if s.Port != 465 {
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(&tls.Config{ServerName: s.Host}); err != nil {
				return err
			}
		}
	}
	if s.Username != "" {
		if ok, _ := c.Extension("AUTH"); ok {
			if err := c.Auth(smtp.PlainAuth("", s.Username, s.Password, s.Host)); err != nil {
				return err
			}
		}
	}
	if err := c.Mail(s.From); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(s.buildMessage(to, subject, body)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// dial 465 端口隐式 TLS，其余端口明文连接（由 Send 协商 STARTTLS）。
func (s *Sender) dial() (net.Conn, error) {
	addr := net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
	if s.Port == 465 {
		return tls.Dial("tcp", addr, &tls.Config{ServerName: s.Host})
	}
	return net.Dial("tcp", addr)
}

// buildMessage 组装 RFC 822 报文：主题按 RFC 2047 编码，正文统一 CRLF 行尾
// （点 stuffing 由 textproto.DataWriter 处理）。
func (s *Sender) buildMessage(to, subject, body string) []byte {
	crlf := strings.ReplaceAll(strings.ReplaceAll(body, "\r\n", "\n"), "\n", "\r\n")
	message := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s",
		s.From, to, mime.QEncoding.Encode("UTF-8", subject), time.Now().Format(time.RFC1123Z), crlf)
	return []byte(message)
}
