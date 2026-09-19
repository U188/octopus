package relay

import (
	"github.com/U188/octopus/internal/model"
	"github.com/U188/octopus/internal/relay/balancer"
)

func selectChannelKey(iter *balancer.Iterator, channel *model.Channel) (model.ChannelKey, bool) {
	if channel.NoAuth {
		if iter.SkipCircuitBreak(channel.ID, 0, channel.Name) {
			return model.ChannelKey{}, false
		}
		return model.ChannelKey{}, true
	}

	selectOpts := model.ChannelKeySelectOptions{
		ExcludeKeyIDs:  make(map[int]struct{}),
		PreferredKeyID: iter.StickyKeyID(),
	}
	for {
		usedKey := channel.GetChannelKey(selectOpts)
		if usedKey.ChannelKey == "" {
			if len(selectOpts.ExcludeKeyIDs) == 0 {
				iter.Skip(channel.ID, 0, channel.Name, "no available key")
			}
			return model.ChannelKey{}, false
		}
		if !iter.SkipCircuitBreak(channel.ID, usedKey.ID, channel.Name) {
			return usedKey, true
		}
		selectOpts.ExcludeKeyIDs[usedKey.ID] = struct{}{}
	}
}
