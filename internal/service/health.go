package service

import (
	"context"
	"time"
)

// Health 检查数据库连接可用性。
func (s *Service) Health(ctx context.Context) bool {
	if s.db == nil {
		return false
	}
	conn, err := s.db.DB()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return conn.PingContext(ctx) == nil
}
