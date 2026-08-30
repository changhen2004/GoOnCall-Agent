package bootstrap

import (
	"os"

	"gooncall-agent/internal/config"
)

// loadConfig 加载配置。
func loadConfig() (*config.Config, error) {
	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "configs/config.yaml"
	}
	return config.Load(cfgPath)
}
