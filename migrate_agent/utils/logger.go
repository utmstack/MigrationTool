package utils

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/threatwinds/logger"
	"github.com/utmstack/MigrationTool/config"
)

var (
	Logger         *logger.Logger
	loggerInitOnce sync.Once
)

func InitLogger() error {
	var initErr error

	loggerInitOnce.Do(func() {
		logDir := config.MigrationLogPath()

		if err := os.MkdirAll(logDir, 0755); err != nil {
			initErr = err
			return
		}

		logFile := filepath.Join(logDir, "migration.log")
		Logger = logger.NewLogger(
			&logger.Config{
				Format:  "text",
				Level:   200, // Info level
				Output:  logFile,
				Retries: 3,
				Wait:    5,
			},
		)
	})

	return initErr
}
