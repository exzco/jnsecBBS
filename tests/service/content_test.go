package service_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/exzco/bbs/internal/config"
	"github.com/exzco/bbs/internal/service"
)

func TestContentRulesApplyWithoutHTTP(t *testing.T) {
	svc := service.New(&config.Config{}, nil)
	if _, err := svc.Reply(nil, 0, " \n\t"); err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("blank reply must be rejected before database access: %v", err)
	}
	if _, err := svc.EditPost(nil, 0, " \n\t"); err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("blank edit must be rejected before database access: %v", err)
	}
	if _, err := svc.SaveUpload(nil, "large.txt", "text/plain", 25<<20+1, strings.NewReader("")); err == nil || err.Status != http.StatusBadRequest {
		t.Fatalf("oversized upload must be rejected before storage: %v", err)
	}
	readOnly := service.New(&config.Config{ReadOnly: true}, nil)
	if _, err := readOnly.SaveUpload(nil, "file.txt", "text/plain", 1, strings.NewReader("x")); err == nil || err.Status != http.StatusForbidden {
		t.Fatalf("read-only service must reject uploads: %v", err)
	}
}
