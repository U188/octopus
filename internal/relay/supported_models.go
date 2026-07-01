package relay

import "strings"

func supportedModelAllowed(supportedModels string, modelName string) bool {
	supportedModels = strings.TrimSpace(supportedModels)
	if supportedModels == "" {
		return true
	}
	target := strings.TrimSpace(modelName)
	if target == "" {
		return false
	}
	for _, item := range strings.Split(supportedModels, ",") {
		if strings.TrimSpace(item) == target {
			return true
		}
	}
	return false
}
