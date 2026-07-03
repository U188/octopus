package handlers

import (
	"net/http"
	"strconv"

	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/server/middleware"
	"github.com/U188/octopus/internal/server/resp"
	"github.com/U188/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/audit").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("/logs", http.MethodGet).
				Handle(listAuditLogs),
		)
}

func listAuditLogs(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	logs, err := op.AuditList(c.Request.Context(), op.AuditListFilter{Limit: limit})
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, logs)
}
