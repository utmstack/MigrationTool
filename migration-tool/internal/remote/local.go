package remote

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type LocalExecutor struct{}

func NewLocalExecutor() *LocalExecutor {
	return &LocalExecutor{}
}

func (l *LocalExecutor) Close() error {
	return nil
}

func (l *LocalExecutor) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (l *LocalExecutor) WriteFile(path string, content []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory: %w", err)
		}
	}

	// Use atomic write: temp file + rename to prevent corrupt files on failure
	tempFile, err := os.CreateTemp(dir, ".tmp_*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		if err != nil {
			os.Remove(tempPath)
		}
	}()

	if _, err = tempFile.Write(content); err != nil {
		tempFile.Close()
		return fmt.Errorf("failed to write to temp file: %w", err)
	}
	if err = tempFile.Close(); err != nil {
		return fmt.Errorf("failed to close temp file: %w", err)
	}

	// Set permissions before rename
	if err = os.Chmod(tempPath, perm); err != nil {
		return fmt.Errorf("failed to set file permissions: %w", err)
	}

	if err = os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

func (l *LocalExecutor) ExecuteCommand(command string) (string, error) {
	// Use a 2-minute timeout to prevent indefinite hangs
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return string(output), fmt.Errorf("command timed out after 2 minutes: %w", ctx.Err())
	}
	if err != nil {
		return string(output), fmt.Errorf("command failed: %w", err)
	}
	return string(output), nil
}

func (l *LocalExecutor) ExecuteCommandWithSudo(command string) (string, error) {
	return l.ExecuteCommand(EnsureSudoPrefix(command))
}

// ExecuteCommandWithSudoStreaming runs a command with sudo and streams output to stdout/stderr.
// NOTE: Intentionally has no timeout because this is used for long-running operations
// like the UTMStack installer that can take 10+ minutes to complete.
func (l *LocalExecutor) ExecuteCommandWithSudoStreaming(command string) (string, error) {
	cmd := exec.Command("bash", "-c", EnsureSudoPrefix(command))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("command failed: %w", err)
	}
	return "", nil
}

func (l *LocalExecutor) FileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
