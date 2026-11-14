package web

import (
	"context"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

func (s *Server) health(c echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	if s.db == nil {
		return c.JSON(http.StatusServiceUnavailable, echo.Map{"status": "unavailable"})
	}
	connection, err := s.db.DB()
	if err == nil {
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		err = connection.PingContext(ctx)
	}
	if err != nil {
		return c.JSON(http.StatusServiceUnavailable, echo.Map{"status": "unavailable"})
	}
	return c.JSON(http.StatusOK, echo.Map{"status": "ok"})
}
