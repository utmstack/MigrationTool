package remote

import (
	"bufio"
	crypto_rand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// knownHostsFile is the path to store accepted host keys
const knownHostsFile = ".utmstack_known_hosts"

type SSHClient struct {
	client *ssh.Client
}

type SSHConfig struct {
	Host       string
	Port       int
	User       string
	Password   string
	PrivateKey string
}

// shellQuote escapes a string for safe use in shell commands.
// This prevents command injection via file paths.
func shellQuote(s string) string {
	// Use single quotes and escape any single quotes within the string
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// getKnownHostsPath returns the path to the known hosts file
func getKnownHostsPath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return knownHostsFile
	}
	return filepath.Join(homeDir, knownHostsFile)
}

// loadKnownHosts loads previously accepted host keys
func loadKnownHosts() map[string]string {
	hosts := make(map[string]string)
	path := getKnownHostsPath()

	file, err := os.Open(path)
	if err != nil {
		return hosts
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), " ", 2)
		if len(parts) == 2 {
			hosts[parts[0]] = parts[1]
		}
	}
	return hosts
}

// saveKnownHost saves an accepted host key
func saveKnownHost(address, fingerprint string) error {
	path := getKnownHostsPath()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = fmt.Fprintf(file, "%s %s\n", address, fingerprint)
	return err
}

// formatFingerprint formats a public key as a SHA256 fingerprint
func formatFingerprint(key ssh.PublicKey) string {
	hash := sha256.Sum256(key.Marshal())
	return "SHA256:" + hex.EncodeToString(hash[:])
}

// hostKeyCallback creates a callback that verifies host keys.
// On first connection, it prompts the user to accept the key.
// On subsequent connections, it verifies against stored keys.
func hostKeyCallback(address string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fingerprint := formatFingerprint(key)
		knownHosts := loadKnownHosts()

		// Check if we've seen this host before
		if storedFingerprint, exists := knownHosts[address]; exists {
			if storedFingerprint == fingerprint {
				return nil // Key matches
			}
			// Key changed - potential MITM attack
			return fmt.Errorf("WARNING: HOST KEY HAS CHANGED for %s!\n"+
				"Expected: %s\n"+
				"Received: %s\n"+
				"This could indicate a man-in-the-middle attack. "+
				"If you trust this host, remove the entry from %s and reconnect",
				address, storedFingerprint, fingerprint, getKnownHostsPath())
		}

		// First connection - prompt user to accept with timeout
		fmt.Printf("\nThe authenticity of host '%s' can't be established.\n", address)
		fmt.Printf("Host key fingerprint is %s\n", fingerprint)
		fmt.Printf("Are you sure you want to continue connecting (yes/no)? ")

		responseCh := make(chan string, 1)
		go func() {
			var response string
			fmt.Scanln(&response)
			responseCh <- response
		}()

		var response string
		select {
		case response = <-responseCh:
			// Got response
		case <-time.After(30 * time.Second):
			return fmt.Errorf("host key verification timed out (no response within 30 seconds)")
		}

		response = strings.ToLower(strings.TrimSpace(response))

		if response != "yes" && response != "y" {
			return fmt.Errorf("host key verification rejected by user")
		}

		// Save the accepted key
		if err := saveKnownHost(address, fingerprint); err != nil {
			fmt.Printf("Warning: could not save host key: %v\n", err)
		} else {
			fmt.Printf("Host key saved to %s\n", getKnownHostsPath())
		}

		return nil
	}
}

func NewSSHClient(config SSHConfig) (*SSHClient, error) {
	var authMethods []ssh.AuthMethod

	if config.PrivateKey != "" {
		key, err := os.ReadFile(config.PrivateKey)
		if err != nil {
			return nil, fmt.Errorf("failed to read private key: %w", err)
		}

		signer, err := ssh.ParsePrivateKey(key)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}

		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}

	if config.Password != "" {
		authMethods = append(authMethods, ssh.Password(config.Password))
	}

	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no authentication method provided (password or private key required)")
	}

	port := config.Port
	if port == 0 {
		port = 22
	}

	address := fmt.Sprintf("%s:%d", config.Host, port)

	sshConfig := &ssh.ClientConfig{
		User:            config.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback(address),
		Timeout:         30 * time.Second,
	}

	client, err := ssh.Dial("tcp", address, sshConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to connect via SSH: %w", err)
	}

	return &SSHClient{client: client}, nil
}

func (c *SSHClient) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

func (c *SSHClient) ReadFile(remotePath string) ([]byte, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	quotedPath := shellQuote(remotePath)
	command := fmt.Sprintf("cat %s", quotedPath)
	if strings.HasPrefix(remotePath, "/root/") || remotePath == "/root" {
		command = fmt.Sprintf("sudo cat %s", quotedPath)
	}

	output, err := session.Output(command)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", remotePath, err)
	}

	return output, nil
}

func (c *SSHClient) WriteFile(remotePath string, content []byte, perm os.FileMode) error {
	useSudo := strings.HasPrefix(remotePath, "/root/") || remotePath == "/root"

	remoteDir := path.Dir(remotePath)
	quotedPath := shellQuote(remotePath)
	quotedDir := shellQuote(remoteDir)

	if remoteDir != "." {
		mkdirSession, err := c.client.NewSession()
		if err != nil {
			return fmt.Errorf("failed to create mkdir session: %w", err)
		}
		defer mkdirSession.Close()
		mkdirCmd := fmt.Sprintf("mkdir -p %s", quotedDir)
		if useSudo {
			mkdirCmd = fmt.Sprintf("sudo mkdir -p %s", quotedDir)
		}
		// Log but don't fail - directory may already exist
		// The actual write will fail if we can't create the parent dir
		_ = mkdirSession.Run(mkdirCmd)
	}

	// Generate unique temp file path in same directory as target for atomic rename
	// Using same filesystem ensures mv is atomic
	randomBytes := make([]byte, 8)
	if _, err := crypto_rand.Read(randomBytes); err != nil {
		return fmt.Errorf("failed to generate random bytes: %w", err)
	}
	tempFileName := fmt.Sprintf(".utmstack_tmp_%d_%x", time.Now().UnixNano(), randomBytes)
	tempPath := path.Join(remoteDir, tempFileName)
	quotedTempPath := shellQuote(tempPath)

	// Step 1: Write content to temp file
	writeSession, err := c.client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}
	defer writeSession.Close()

	stdin, err := writeSession.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe: %w", err)
	}

	// Write to temp file first
	writeCmd := fmt.Sprintf("cat > %s", quotedTempPath)
	if useSudo {
		writeCmd = fmt.Sprintf("sudo tee %s > /dev/null", quotedTempPath)
	}

	// Use Start() instead of Run() so we can write to stdin while command is running.
	// Pattern: Start() → Write stdin → Close stdin → Wait()
	if err := writeSession.Start(writeCmd); err != nil {
		return fmt.Errorf("failed to start command: %w", err)
	}

	// Write content to stdin while command is running
	if _, err := stdin.Write(content); err != nil {
		// Attempt cleanup of temp file on error
		c.cleanupTempFile(quotedTempPath, useSudo)
		return fmt.Errorf("failed to write to stdin: %w", err)
	}

	// Close stdin to signal EOF to the remote command
	if err := stdin.Close(); err != nil {
		c.cleanupTempFile(quotedTempPath, useSudo)
		return fmt.Errorf("failed to close stdin: %w", err)
	}

	// Wait for write command to complete
	if err := writeSession.Wait(); err != nil {
		c.cleanupTempFile(quotedTempPath, useSudo)
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Step 2: Set permissions and atomically move temp file to target
	moveSession, err := c.client.NewSession()
	if err != nil {
		c.cleanupTempFile(quotedTempPath, useSudo)
		return fmt.Errorf("failed to create move session: %w", err)
	}
	defer moveSession.Close()

	// chmod temp file then mv to target (mv on same filesystem is atomic)
	moveCmd := fmt.Sprintf("chmod %o %s && mv %s %s", perm, quotedTempPath, quotedTempPath, quotedPath)
	if useSudo {
		moveCmd = fmt.Sprintf("sudo chmod %o %s && sudo mv %s %s", perm, quotedTempPath, quotedTempPath, quotedPath)
	}

	if err := moveSession.Run(moveCmd); err != nil {
		c.cleanupTempFile(quotedTempPath, useSudo)
		return fmt.Errorf("failed to move temp file to target: %w", err)
	}

	return nil
}

// cleanupTempFile attempts to remove a temp file on the remote host.
// Errors are silently ignored as this is best-effort cleanup.
func (c *SSHClient) cleanupTempFile(quotedTempPath string, useSudo bool) {
	cleanupSession, err := c.client.NewSession()
	if err != nil {
		return
	}
	defer cleanupSession.Close()

	cleanupCmd := fmt.Sprintf("rm -f %s", quotedTempPath)
	if useSudo {
		cleanupCmd = fmt.Sprintf("sudo rm -f %s", quotedTempPath)
	}
	_ = cleanupSession.Run(cleanupCmd)
}

func (c *SSHClient) ExecuteCommand(command string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	output, err := session.CombinedOutput(command)
	if err != nil {
		return string(output), fmt.Errorf("command failed: %w", err)
	}

	return string(output), nil
}

func (c *SSHClient) ExecuteCommandWithSudo(command string) (string, error) {
	return c.ExecuteCommand(EnsureSudoPrefix(command))
}

func (c *SSHClient) ExecuteCommandWithSudoStreaming(command string) (string, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	// Redirect stdout and stderr to the standard output to see in real time
	session.Stdout = os.Stdout
	session.Stderr = os.Stderr

	// Execute command and wait without timeout
	err = session.Run(EnsureSudoPrefix(command))
	if err != nil {
		return "", fmt.Errorf("command failed: %w", err)
	}

	return "", nil
}

func (c *SSHClient) FileExists(remotePath string) (bool, error) {
	session, err := c.client.NewSession()
	if err != nil {
		return false, fmt.Errorf("failed to create session: %w", err)
	}
	defer session.Close()

	quotedPath := shellQuote(remotePath)
	err = session.Run(fmt.Sprintf("test -f %s", quotedPath))
	if err != nil {
		return false, nil
	}

	return true, nil
}
