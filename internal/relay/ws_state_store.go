package relay

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/U188/octopus/internal/relay/balancer"
	"github.com/U188/octopus/internal/utils/safe"
)

type wsConversationStateEntry struct {
	state     *wsConversationState
	expiresAt time.Time
}

var wsConversationStore sync.Map // key: apiKeyID:requestModel:downstreamSessionID -> *wsConversationStateEntry

func init() {
	safe.Go("ws-conversation-state-cleanup", func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for now := range ticker.C {
			cleanupWSConversationStateStore(now)
		}
	})
}

func wsConversationStateKey(apiKeyID int, requestModel, downstreamSessionID string) string {
	return fmt.Sprintf("%d:%s:%s", apiKeyID, strings.TrimSpace(requestModel), strings.TrimSpace(downstreamSessionID))
}

func loadWSConversationState(apiKeyID int, requestModel, downstreamSessionID string) *wsConversationState {
	requestModel = strings.TrimSpace(requestModel)
	downstreamSessionID = strings.TrimSpace(downstreamSessionID)
	if requestModel == "" || downstreamSessionID == "" {
		return nil
	}

	v, ok := wsConversationStore.Load(wsConversationStateKey(apiKeyID, requestModel, downstreamSessionID))
	if !ok {
		return nil
	}

	entry, ok := v.(*wsConversationStateEntry)
	if !ok || entry == nil || entry.state == nil {
		wsConversationStore.Delete(wsConversationStateKey(apiKeyID, requestModel, downstreamSessionID))
		return nil
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		wsConversationStore.Delete(wsConversationStateKey(apiKeyID, requestModel, downstreamSessionID))
		return nil
	}

	return cloneWSConversationState(entry.state)
}

func storeWSConversationState(apiKeyID int, requestModel string, state *wsConversationState, ttl time.Duration) {
	requestModel = strings.TrimSpace(requestModel)
	downstreamSessionID := ""
	if state != nil {
		downstreamSessionID = strings.TrimSpace(state.DownstreamSessionID)
	}
	if requestModel == "" || state == nil || downstreamSessionID == "" {
		return
	}
	if ttl <= 0 {
		ttl = wsClientMaxAge
	}

	cloned := cloneWSConversationState(state)
	if cloned == nil {
		return
	}
	cloned.RequestModel = requestModel

	wsConversationStore.Store(wsConversationStateKey(apiKeyID, requestModel, downstreamSessionID), &wsConversationStateEntry{
		state:     cloned,
		expiresAt: time.Now().Add(ttl),
	})
}

func deleteWSConversationState(apiKeyID int, requestModel, downstreamSessionID string) {
	requestModel = strings.TrimSpace(requestModel)
	downstreamSessionID = strings.TrimSpace(downstreamSessionID)
	if requestModel == "" || downstreamSessionID == "" {
		return
	}
	wsConversationStore.Delete(wsConversationStateKey(apiKeyID, requestModel, downstreamSessionID))
}

func resolveWSConversationState(apiKeyID int, requestModel string, localState *wsConversationState, allowStoredRestore bool, downstreamSessionID string) *wsConversationState {
	requestModel = strings.TrimSpace(requestModel)
	downstreamSessionID = strings.TrimSpace(downstreamSessionID)
	if requestModel == "" {
		return localState
	}
	if localState != nil && localState.MatchesRequestModel(requestModel) {
		return localState
	}
	if !allowStoredRestore {
		return nil
	}
	return loadWSConversationState(apiKeyID, requestModel, downstreamSessionID)
}

func wsConversationStateToSticky(state *wsConversationState) *balancer.SessionEntry {
	if state == nil || state.ChannelID <= 0 {
		return nil
	}
	return &balancer.SessionEntry{
		ChannelID:    state.ChannelID,
		ChannelKeyID: state.ChannelKeyID,
		Timestamp:    time.Now(),
	}
}

func wsConversationStateTTL(sessionKeepTimeSec int) time.Duration {
	if sessionKeepTimeSec <= 0 {
		return wsClientMaxAge
	}
	ttl := time.Duration(sessionKeepTimeSec) * time.Second
	if ttl > wsClientMaxAge {
		return wsClientMaxAge
	}
	return ttl
}

func resetWSConversationStateStore() {
	wsConversationStore.Range(func(key, _ any) bool {
		wsConversationStore.Delete(key)
		return true
	})
}

func cleanupWSConversationStateStore(now time.Time) {
	wsConversationStore.Range(func(key, value any) bool {
		entry, ok := value.(*wsConversationStateEntry)
		if !ok || entry == nil || entry.state == nil || (!entry.expiresAt.IsZero() && now.After(entry.expiresAt)) {
			wsConversationStore.Delete(key)
		}
		return true
	})
}
