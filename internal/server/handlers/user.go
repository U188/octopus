package handlers

import (
	"net/http"
	"strconv"

	"github.com/U188/octopus/internal/apperror"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/server/auth"
	"github.com/U188/octopus/internal/server/middleware"
	"github.com/U188/octopus/internal/server/resp"
	"github.com/U188/octopus/internal/server/router"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/user").
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/login", http.MethodPost).
				Handle(login),
		)
	router.NewGroupRouter("/api/v1/user").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(
			router.NewRoute("/change-password", http.MethodPost).
				Handle(changePassword),
		).
		AddRoute(
			router.NewRoute("/change-username", http.MethodPost).
				Handle(changeUsername),
		).
		AddRoute(
			router.NewRoute("/status", http.MethodGet).
				Handle(status),
		)
}

func login(c *gin.Context) {
	var user model.UserLogin
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if allowed, retryAfter := auth.LoginAllowed(c.Request.RemoteAddr); !allowed {
		seconds := int(retryAfter.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.Itoa(seconds))
		resp.ErrorWithAppError(c, http.StatusTooManyRequests,
			apperror.New(apperror.CodeAuthLoginRateLimited, "too many login attempts").WithStatus(http.StatusTooManyRequests))
		return
	}
	if err := op.UserVerify(user.Username, user.Password); err != nil {
		auth.RecordLoginFailure(c.Request.RemoteAddr)
		resp.InvalidCredentials(c)
		return
	}
	token, expire, err := auth.GenerateJWTToken(user.Expire)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	auth.ClearLoginFailures(c.Request.RemoteAddr)
	resp.Success(c, model.UserLoginResponse{Token: token, ExpireAt: expire})
}

func changePassword(c *gin.Context) {
	var user model.UserChangePassword
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := auth.ChangePassword(user.OldPassword, user.NewPassword); err != nil {
		recordAuditFailure(c, "user.change_password", nil, err)
		resp.ErrorWithAppError(c, http.StatusInternalServerError, err)
		return
	}
	recordAuditSuccess(c, "user.change_password", nil)
	resp.Success(c, "password changed successfully")
}

func changeUsername(c *gin.Context) {
	var user model.UserChangeUsername
	if err := c.ShouldBindJSON(&user); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := auth.ChangeUsername(user.NewUsername); err != nil {
		recordAuditFailure(c, "user.change_username", map[string]any{
			"new_username": user.NewUsername,
		}, err)
		resp.ErrorWithAppError(c, http.StatusInternalServerError, err)
		return
	}
	recordAuditSuccess(c, "user.change_username", map[string]any{
		"new_username": user.NewUsername,
	})
	resp.Success(c, "username changed successfully")
}

func status(c *gin.Context) {
	resp.Success(c, "ok")
}
