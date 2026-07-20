package server

import (
	"fmt"
	"net/http"
	"time"

	"github.com/U188/octopus/internal/conf"
	"github.com/U188/octopus/internal/relay/bodycache"
	_ "github.com/U188/octopus/internal/server/handlers"
	"github.com/U188/octopus/internal/server/middleware"
	"github.com/U188/octopus/internal/server/resp"
	"github.com/U188/octopus/internal/server/router"
	"github.com/U188/octopus/internal/utils/log"
	"github.com/U188/octopus/internal/utils/safe"
	"github.com/U188/octopus/static"
	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

var httpSrv http.Server

func Start() error {
	if conf.IsDebug() {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	// 启动时清理 Images 请求体临时文件（失败仅告警，不阻断启动）
	tmpDir := bodycache.TmpDirFromEnv()
	olderThan := bodycache.TmpCleanupOlderThanFromEnv()
	if err := bodycache.CleanupOldTmpFiles(tmpDir, bodycache.TmpFilePrefix, olderThan); err != nil {
		log.Warnf("cleanup images tmp files failed: dir=%s prefix=%s olderThan=%s err=%v", tmpDir, bodycache.TmpFilePrefix, olderThan, err)
	}

	r := gin.New()
	r.Use(middleware.SecurityHeaders())
	r.Use(gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		log.Errorf("http panic recovered: %v", recovered)
		resp.Error(c, http.StatusInternalServerError, resp.ErrInternalServer)
		c.Abort()
	}))

	r.Use(gzip.Gzip(gzip.DefaultCompression,
		gzip.WithExcludedPaths([]string{"/v1/"}),
		gzip.WithExcludedPathsRegexs([]string{`/api/v1/log/stream`}),
	))

	r.Use(middleware.Logger(middleware.LoggerConfig{
		Enabled:       conf.AppConfig.Log.Access.Enabled || conf.IsDebug(),
		SlowThreshold: time.Duration(conf.AppConfig.Log.Access.SlowThresholdMS) * time.Millisecond,
	}))
	r.Use(middleware.RequestDebug(middleware.RequestDebugConfig{
		Enabled:        conf.AppConfig.Log.RequestDebug.Enabled,
		IncludeHeaders: conf.AppConfig.Log.RequestDebug.IncludeHeaders,
		IncludeBody:    conf.AppConfig.Log.RequestDebug.IncludeBody,
		MaxBodyBytes:   conf.AppConfig.Log.RequestDebug.MaxBodyBytes,
	}))
	r.Use(middleware.Cors())
	r.Use(middleware.StaticEmbed("/", static.StaticFS))

	if err := router.RegisterAll(r); err != nil {
		return fmt.Errorf("failed to register routes: %w", err)
	}

	httpSrv.Addr = fmt.Sprintf("%s:%d", conf.AppConfig.Server.Host, conf.AppConfig.Server.Port)
	httpSrv.Handler = r
	httpSrv.ReadHeaderTimeout = 10 * time.Second
	httpSrv.IdleTimeout = 2 * time.Minute
	httpSrv.MaxHeaderBytes = 1 << 20
	safe.Go("http-listen", func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Errorf("http server listen and serve error: %v", err)
		}
	})
	return nil
}

func Close() error {
	return httpSrv.Close()
}
