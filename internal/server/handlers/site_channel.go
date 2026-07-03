package handlers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/U188/octopus/internal/apperror"
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/server/middleware"
	"github.com/U188/octopus/internal/server/resp"
	"github.com/U188/octopus/internal/server/router"
	sitesvc "github.com/U188/octopus/internal/site"
	"github.com/gin-gonic/gin"
)

func init() {
	router.NewGroupRouter("/api/v1/site-channel").
		Use(middleware.Auth()).
		AddRoute(router.NewRoute("/list", http.MethodGet).Handle(listSiteChannel)).
		AddRoute(router.NewRoute("/:siteId", http.MethodGet).Handle(getSiteChannel)).
		AddRoute(router.NewRoute("/:siteId/account/:accountId", http.MethodGet).Handle(getSiteChannelAccount)).
		AddRoute(router.NewRoute("/:siteId/account/:accountId/model-history", http.MethodGet).Handle(getSiteChannelModelHistory))

	router.NewGroupRouter("/api/v1/site-channel").
		Use(middleware.Auth()).
		Use(middleware.RequireJSON()).
		AddRoute(router.NewRoute("/:siteId/account/:accountId/keys", http.MethodPost).Handle(createSiteChannelKey)).
		AddRoute(router.NewRoute("/:siteId/account/:accountId/source-keys", http.MethodPut).Handle(updateSiteSourceKeys)).
		AddRoute(router.NewRoute("/:siteId/account/:accountId/group-projection", http.MethodPut).Handle(updateSiteGroupProjection)).
		AddRoute(router.NewRoute("/:siteId/account/:accountId/model-routes", http.MethodPut).Handle(updateSiteChannelModelRoutes)).
		AddRoute(router.NewRoute("/:siteId/account/:accountId/model-disabled", http.MethodPut).Handle(updateSiteChannelModelDisabled)).
		AddRoute(router.NewRoute("/:siteId/account/:accountId/model-context-1m", http.MethodPut).Handle(updateSiteChannelModelContext1M)).
		AddRoute(router.NewRoute("/:siteId/account/:accountId/projected-channel-settings", http.MethodPut).Handle(updateSiteProjectedChannelSettings)).
		AddRoute(router.NewRoute("/:siteId/account/:accountId/manual-models", http.MethodPost).Handle(addSiteManualModels)).
		AddRoute(router.NewRoute("/:siteId/account/:accountId/manual-models/delete", http.MethodPost).Handle(deleteSiteManualModel)).
		AddRoute(router.NewRoute("/:siteId/account/:accountId/model-routes/reset", http.MethodPost).Handle(resetSiteChannelModelRoutes))
}

func listSiteChannel(c *gin.Context) {
	includeHistory, err := parseBoolQuery(c, "include_history", true)
	if err != nil {
		resp.Error(c, http.StatusBadRequest, err.Error())
		return
	}
	data, err := op.SiteChannelListWithOptions(c.Request.Context(), op.SiteChannelListOptions{IncludeHistory: includeHistory})
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, data)
}

func getSiteChannel(c *gin.Context) {
	siteID, err := strconv.Atoi(c.Param("siteId"))
	if err != nil {
		resp.InvalidParam(c)
		return
	}
	data, err := op.SiteChannelGet(siteID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, data)
}

func getSiteChannelAccount(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	data, err := op.SiteChannelAccountGet(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, data)
}

func getSiteChannelModelHistory(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	data, err := op.SiteChannelModelHistory(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	resp.Success(c, data)
}

func createSiteChannelKey(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	var req model.SiteChannelKeyCreateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if _, err := sitesvc.CreateAccountToken(c.Request.Context(), accountID, req); err != nil {
		recordAuditFailure(c, "site_channel.key.create", map[string]any{
			"site_id":    siteID,
			"account_id": accountID,
			"group_key":  req.GroupKey,
			"name":       req.Name,
		}, err)
		resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelKeyCreateFailed, "site channel key create failed", err).WithStatus(http.StatusInternalServerError))
		return
	}
	data, err := op.SiteChannelAccountGet(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	recordAuditSuccess(c, "site_channel.key.create", map[string]any{
		"site_id":    siteID,
		"account_id": accountID,
		"group_key":  req.GroupKey,
		"name":       req.Name,
	})
	resp.Success(c, data)
}

func updateSiteSourceKeys(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	var req model.SiteSourceKeyUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := op.UpdateSiteSourceKeys(siteID, accountID, &req, c.Request.Context()); err != nil {
		recordAuditFailure(c, "site_channel.source_keys.update", map[string]any{
			"site_id":      siteID,
			"account_id":   accountID,
			"group_key":    req.GroupKey,
			"add_count":    len(req.KeysToAdd),
			"update_count": len(req.KeysToUpdate),
			"delete_count": len(req.KeysToDelete),
		}, err)
		resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelSourceKeyUpdateFailed, "site channel source key update failed", err).WithStatus(http.StatusInternalServerError))
		return
	}
	if err := reprojectSiteChannelAccount(c.Request.Context(), accountID); err != nil {
		resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelProjectFailed, "site channel project failed", err).WithStatus(http.StatusInternalServerError))
		return
	}
	data, err := op.SiteChannelAccountGet(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	recordAuditSuccess(c, "site_channel.source_keys.update", map[string]any{
		"site_id":      siteID,
		"account_id":   accountID,
		"group_key":    req.GroupKey,
		"add_count":    len(req.KeysToAdd),
		"update_count": len(req.KeysToUpdate),
		"delete_count": len(req.KeysToDelete),
	})
	resp.Success(c, data)
}

func updateSiteGroupProjection(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	var req model.SiteGroupProjectionUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := op.UpdateSiteGroupProjection(siteID, accountID, &req, c.Request.Context()); err != nil {
		status := siteChannelMutationErrorStatus(err)
		recordAuditFailure(c, "site_channel.group_projection.update", map[string]any{
			"site_id":             siteID,
			"account_id":          accountID,
			"group_key":           req.GroupKey,
			"projection_disabled": req.ProjectionDisabled,
		}, err)
		resp.ErrorWithAppError(c, status, apperror.Wrap(op.CodeSiteChannelProjectedSettingsFailed, "site group projection update failed", err).WithStatus(status))
		return
	}
	if err := reprojectSiteChannelAccount(c.Request.Context(), accountID); err != nil {
		resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelProjectFailed, "site channel project failed", err).WithStatus(http.StatusInternalServerError))
		return
	}
	data, err := op.SiteChannelAccountGet(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	recordAuditSuccess(c, "site_channel.group_projection.update", map[string]any{
		"site_id":             siteID,
		"account_id":          accountID,
		"group_key":           req.GroupKey,
		"projection_disabled": req.ProjectionDisabled,
	})
	resp.Success(c, data)
}

func updateSiteChannelModelRoutes(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	var req []model.SiteModelRouteUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	for _, item := range req {
		if err := op.SiteModelRouteUpdate(accountID, item.GroupKey, item.ModelName, item.RouteType, model.SiteModelRouteSourceManualOverride, true, item.RouteRawPayload, c.Request.Context()); err != nil {
			recordAuditFailure(c, "site_channel.model_routes.update", map[string]any{
				"site_id":    siteID,
				"account_id": accountID,
				"count":      len(req),
				"group_key":  item.GroupKey,
				"model_name": item.ModelName,
				"route_type": item.RouteType,
			}, err)
			resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelRouteUpdateFailed, "site channel route update failed", err).WithStatus(http.StatusInternalServerError))
			return
		}
	}
	if err := reprojectSiteChannelAccount(c.Request.Context(), accountID); err != nil {
		resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelProjectFailed, "site channel project failed", err).WithStatus(http.StatusInternalServerError))
		return
	}
	data, err := op.SiteChannelAccountGet(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	recordAuditSuccess(c, "site_channel.model_routes.update", map[string]any{
		"site_id":    siteID,
		"account_id": accountID,
		"count":      len(req),
	})
	resp.Success(c, data)
}

func updateSiteChannelModelDisabled(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	var req []model.SiteModelDisableUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	for _, item := range req {
		if err := op.SiteModelDisabledUpdate(accountID, item.GroupKey, item.ModelName, item.Disabled, c.Request.Context()); err != nil {
			recordAuditFailure(c, "site_channel.model_disabled.update", map[string]any{
				"site_id":    siteID,
				"account_id": accountID,
				"count":      len(req),
				"group_key":  item.GroupKey,
				"model_name": item.ModelName,
				"disabled":   item.Disabled,
			}, err)
			resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelModelDisableFailed, "site channel model disable failed", err).WithStatus(http.StatusInternalServerError))
			return
		}
	}
	if err := reprojectSiteChannelAccount(c.Request.Context(), accountID); err != nil {
		resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelProjectFailed, "site channel project failed", err).WithStatus(http.StatusInternalServerError))
		return
	}
	data, err := op.SiteChannelAccountGet(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	recordAuditSuccess(c, "site_channel.model_disabled.update", map[string]any{
		"site_id":    siteID,
		"account_id": accountID,
		"count":      len(req),
	})
	resp.Success(c, data)
}

func updateSiteChannelModelContext1M(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	var req []model.SiteModelContext1MUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	for _, item := range req {
		if err := op.SiteModelContext1MUpdate(accountID, item.GroupKey, item.ModelName, item.Context1M, c.Request.Context()); err != nil {
			recordAuditFailure(c, "site_channel.model_context_1m.update", map[string]any{
				"site_id":    siteID,
				"account_id": accountID,
				"count":      len(req),
				"group_key":  item.GroupKey,
				"model_name": item.ModelName,
				"context_1m": item.Context1M,
			}, err)
			resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelRouteUpdateFailed, "site channel model context 1m update failed", err).WithStatus(http.StatusInternalServerError))
			return
		}
	}
	data, err := op.SiteChannelAccountGet(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	recordAuditSuccess(c, "site_channel.model_context_1m.update", map[string]any{
		"site_id":    siteID,
		"account_id": accountID,
		"count":      len(req),
	})
	resp.Success(c, data)
}

func updateSiteProjectedChannelSettings(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	var req []model.SiteProjectedChannelSettingsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := op.UpdateSiteProjectedChannelSettings(siteID, accountID, req, c.Request.Context()); err != nil {
		status := siteChannelMutationErrorStatus(err)
		recordAuditFailure(c, "site_channel.projected_channel_settings.update", map[string]any{
			"site_id":    siteID,
			"account_id": accountID,
			"count":      len(req),
		}, err)
		resp.ErrorWithAppError(c, status, apperror.Wrap(op.CodeSiteChannelProjectedSettingsFailed, "site projected channel settings update failed", err).WithStatus(status))
		return
	}
	data, err := op.SiteChannelAccountGet(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	recordAuditSuccess(c, "site_channel.projected_channel_settings.update", map[string]any{
		"site_id":    siteID,
		"account_id": accountID,
		"count":      len(req),
	})
	resp.Success(c, data)
}

func addSiteManualModels(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	var req model.SiteManualModelAddRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := op.SiteManualModelsAdd(siteID, accountID, &req, c.Request.Context()); err != nil {
		status := siteChannelMutationErrorStatus(err)
		recordAuditFailure(c, "site_channel.manual_models.add", map[string]any{
			"site_id":    siteID,
			"account_id": accountID,
			"group_key":  req.GroupKey,
			"count":      len(req.Models),
		}, err)
		resp.ErrorWithAppError(c, status, apperror.Wrap(op.CodeSiteChannelManualModelFailed, "site manual model update failed", err).WithStatus(status))
		return
	}
	if err := reprojectSiteChannelAccount(c.Request.Context(), accountID); err != nil {
		resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelProjectFailed, "site channel project failed", err).WithStatus(http.StatusInternalServerError))
		return
	}
	data, err := op.SiteChannelAccountGet(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	recordAuditSuccess(c, "site_channel.manual_models.add", map[string]any{
		"site_id":    siteID,
		"account_id": accountID,
		"group_key":  req.GroupKey,
		"count":      len(req.Models),
	})
	resp.Success(c, data)
}

func deleteSiteManualModel(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	var req model.SiteManualModelDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		resp.InvalidJSON(c)
		return
	}
	if err := op.SiteManualModelDelete(siteID, accountID, &req, c.Request.Context()); err != nil {
		status := siteChannelMutationErrorStatus(err)
		recordAuditFailure(c, "site_channel.manual_models.delete", map[string]any{
			"site_id":    siteID,
			"account_id": accountID,
			"group_key":  req.GroupKey,
			"model_name": req.ModelName,
		}, err)
		resp.ErrorWithAppError(c, status, apperror.Wrap(op.CodeSiteChannelManualModelFailed, "site manual model update failed", err).WithStatus(status))
		return
	}
	if err := reprojectSiteChannelAccount(c.Request.Context(), accountID); err != nil {
		resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelProjectFailed, "site channel project failed", err).WithStatus(http.StatusInternalServerError))
		return
	}
	data, err := op.SiteChannelAccountGet(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	recordAuditSuccess(c, "site_channel.manual_models.delete", map[string]any{
		"site_id":    siteID,
		"account_id": accountID,
		"group_key":  req.GroupKey,
		"model_name": req.ModelName,
	})
	resp.Success(c, data)
}

func resetSiteChannelModelRoutes(c *gin.Context) {
	siteID, accountID, ok := parseSiteChannelIDs(c)
	if !ok {
		return
	}
	if err := op.SiteChannelResetAccountRoutes(siteID, accountID, c.Request.Context()); err != nil {
		recordAuditFailure(c, "site_channel.model_routes.reset", map[string]any{
			"site_id":    siteID,
			"account_id": accountID,
		}, err)
		resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelRouteUpdateFailed, "site channel route update failed", err).WithStatus(http.StatusInternalServerError))
		return
	}
	if err := reprojectSiteChannelAccount(c.Request.Context(), accountID); err != nil {
		resp.ErrorWithAppError(c, http.StatusInternalServerError, apperror.Wrap(op.CodeSiteChannelProjectFailed, "site channel project failed", err).WithStatus(http.StatusInternalServerError))
		return
	}
	data, err := op.SiteChannelAccountGet(siteID, accountID, c.Request.Context())
	if err != nil {
		resp.Error(c, http.StatusInternalServerError, err.Error())
		return
	}
	recordAuditSuccess(c, "site_channel.model_routes.reset", map[string]any{
		"site_id":    siteID,
		"account_id": accountID,
	})
	resp.Success(c, data)
}

func siteChannelMutationErrorStatus(err error) int {
	var appErr *apperror.Error
	if errors.As(err, &appErr) && appErr != nil && appErr.Status > 0 {
		return appErr.Status
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "not found"):
		return http.StatusNotFound
	case strings.Contains(message, "required"),
		strings.Contains(message, "invalid"),
		strings.Contains(message, "duplicate"),
		strings.Contains(message, "already exists"),
		strings.Contains(message, "json object"),
		strings.Contains(message, "unsupported"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func parseSiteChannelIDs(c *gin.Context) (int, int, bool) {
	siteID, err := strconv.Atoi(c.Param("siteId"))
	if err != nil {
		resp.InvalidParam(c)
		return 0, 0, false
	}
	accountID, err := strconv.Atoi(c.Param("accountId"))
	if err != nil {
		resp.InvalidParam(c)
		return 0, 0, false
	}
	return siteID, accountID, true
}

func reprojectSiteChannelAccount(parent context.Context, accountID int) error {
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()

	_, err := sitesvc.ProjectAccount(ctx, accountID)
	return err
}
