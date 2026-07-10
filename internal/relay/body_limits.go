package relay

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/U188/octopus/internal/conf"
)

var errRelayBodyTooLarge = errors.New("body exceeds configured size limit")

var (
	maxRelayRequestBodyBytes  int64 = 32 << 20
	maxRelayResponseBodyBytes int64 = 128 << 20
)

const maxRelayErrorBodyBytes int64 = 1 << 20

func init() {
	maxRelayRequestBodyBytes = relayLimitFromEnv("RELAY_MAX_BODY_MB", maxRelayRequestBodyBytes)
	maxRelayResponseBodyBytes = relayLimitFromEnv("RELAY_MAX_RESPONSE_MB", maxRelayResponseBodyBytes)
}

func relayLimitFromEnv(suffix string, fallback int64) int64 {
	raw := strings.TrimSpace(os.Getenv(strings.ToUpper(conf.APP_NAME) + "_" + suffix))
	if raw == "" {
		return fallback
	}
	mb, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || mb <= 0 || mb > (1<<20) {
		return fallback
	}
	return mb << 20
}

func readRelayBody(r io.Reader, maxBytes int64) ([]byte, error) {
	if r == nil {
		return nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: maximum is %d bytes", errRelayBodyTooLarge, maxBytes)
	}
	return data, nil
}
