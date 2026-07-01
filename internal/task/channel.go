package task

import (
	"context"
	"time"

	"github.com/U188/octopus/internal/helper"
	"github.com/U188/octopus/internal/op"
	"github.com/U188/octopus/internal/utils/log"
)

func ChannelBaseUrlDelayTask() {
	log.Debugf("channel base url delay task started")
	startTime := time.Now()
	defer func() {
		log.Debugf("channel base url delay task finished, update time: %s", time.Since(startTime))
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	channels, err := op.ChannelList(ctx)
	if err != nil {
		log.Errorf("failed to list channels: %v", err)
		return
	}
	for _, channel := range channels {
		helper.ChannelBaseUrlDelayUpdate(&channel, ctx)
	}
}
