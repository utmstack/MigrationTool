package remote

import (
	"os"
	"strings"
)

type CommandExecutor interface {
	Close() error
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, content []byte, perm os.FileMode) error
	ExecuteCommand(command string) (string, error)
	ExecuteCommandWithSudo(command string) (string, error)
	ExecuteCommandWithSudoStreaming(command string) (string, error)
	FileExists(path string) (bool, error)
}

// IsLocalhost determines if the given IP address corresponds to localhost
func IsLocalhost(ip string) bool {
	return ip == "127.0.0.1" || ip == "localhost" || ip == "::1"
}

// EnsureSudoPrefix adds "sudo" to the command if it doesn't already have it
func EnsureSudoPrefix(command string) string {
	if strings.HasPrefix(command, "sudo") {
		return command
	}
	return "sudo " + command
}
