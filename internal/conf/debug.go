package conf

import (
	"os"
	"strings"
)

func IsDebug() bool {
	return os.Getenv(strings.ToUpper(APP_NAME)+"_DEBUG") == "true"
}
