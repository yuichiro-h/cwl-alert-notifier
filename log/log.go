package log

import (
	"go.uber.org/zap"
)

var c Config

type Config struct {
	Debug bool
}

func SetConfig(config Config) {
	c = config
}

func Get() *zap.Logger {
	if c.Debug {
		logger, _ := zap.NewDevelopment()
		return logger
	}

	logger, _ := zap.NewProduction()
	return logger
}
