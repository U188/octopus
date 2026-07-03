package handlers

import (
	"net/http"
	"time"

	"github.com/U188/octopus/internal/conf"
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

func updateFunc(c *gin.Context) {
	err := update.UpdateCore()
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, "update success")
}

func restartFunc(c *gin.Context) {
	if err := update.RestartCore(500 * time.Millisecond); err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, "restart scheduled")
}
