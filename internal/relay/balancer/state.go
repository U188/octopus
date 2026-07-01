package balancer

import "github.com/U188/octopus/internal/op"

func init() {
	op.RegisterRelayBalancerStateReset(ResetStateByChannel)
}

func ResetStateByChannel(channelID int) {
	resetCircuitBreakerByChannel(channelID)
	resetStickyByChannel(channelID)
}
