package handlers

import (
	"net/http"
	"time"

	"github.com/U188/octopus/internal/conf"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/server/middleware"
	"github.com/U188/octopus/internal/server/resp"
	"github.com/U188/octopus/internal/server/router"
	"github.com/U188/octopus/internal/update"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/update").
		Use(middleware.Auth()).
		AddRoute(
			router.NewRoute("", http.MethodGet).
				Handle(latest),
		).
		AddRoute(
			router.NewRoute("/now-version", http.MethodGet).
				Handle(getNowVersion),
		).
		AddRoute(
			router.NewRoute("/info", http.MethodGet).
				Handle(getVersionInfo),
		).
		AddRoute(
			router.NewRoute("/status", http.MethodGet).
				Handle(getUpdateStatus),
		).
		AddRoute(
			router.NewRoute("", http.MethodPost).
				Handle(updateFunc),
		).
		AddRoute(
			router.NewRoute("/restart", http.MethodPost).
				Handle(restartFunc),
		)
}

func latest(c *gin.Context) {
	latestInfo, err := update.GetLatestInfo()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, *latestInfo)
}

func getNowVersion(c *gin.Context) {
	resp.Success(c, conf.Version)
}

func getVersionInfo(c *gin.Context) {
	resp.Success(c, gin.H{
		"version":    conf.Version,
		"commit":     conf.Commit,
		"build_time": conf.BuildTime,
		"author":     conf.Author,
		"repo":       conf.Repo,
	})
}

func getUpdateStatus(c *gin.Context) {
	resp.Success(c, update.CurrentStatus())
}

func updateFunc(c *gin.Context) {
	actor := "admin"
	if user := op.UserGet(); user.Username != "" {
		actor = user.Username
	}
	// Kick off the update asynchronously and return immediately. The download +
	// install can take ~30s+, and blocking the request for that long made the
	// browser/reverse-proxy time out and report "update failed" even when the
	// server updated successfully. The UI now polls /status and /info instead.
	status := update.StartUpdate(update.AuditMeta{
		Actor:       actor,
		IP:          c.ClientIP(),
		UserAgent:   c.Request.UserAgent(),
		Method:      c.Request.Method,
		Path:        c.FullPath(),
		FromVersion: conf.Version,
		Commit:      conf.Commit,
	})
	resp.Success(c, status)
}

func restartFunc(c *gin.Context) {
	if err := update.RestartCore(500 * time.Millisecond); err != nil {
		recordAudit(c, "system.restart", op.AuditStatusFailed, map[string]any{
			"version": conf.Version,
			"commit":  conf.Commit,
		}, err)
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	recordAudit(c, "system.restart", op.AuditStatusSuccess, map[string]any{
		"version": conf.Version,
		"commit":  conf.Commit,
	}, nil)
	resp.Success(c, "restart scheduled")
}
