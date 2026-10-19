package compose

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/config"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
	"gopkg.in/yaml.v3"
)

type ComposeFile struct {
	Services map[string]Service `yaml:"services"`
}

type Service struct {
	Environment []string `yaml:"environment"`
}

// PostgresYAMLServiceName is the service key in the compose.yml written by the
// UTMStack installer ("postgres" in both V11 and V12). The Docker Swarm
// service name is prefixed with the stack: utmstack_postgres.
const PostgresYAMLServiceName = "postgres"

// composeYMLPaths returns the candidate locations of the live compose.yml,
// ordered by likelihood. After an updater self-copy the live one can sit
// next to /usr/local/bin/utmstack_installer; the last entry covers the case
// where the installer ran as root in local mode.
func composeYMLPaths(sshUser string) []string {
	homePath := config.GetComposeYMLPath(sshUser)
	paths := []string{homePath, config.RootComposeYMLPath}
	if homePath != "/root/compose.yml" {
		paths = append(paths, "/root/compose.yml")
	}
	return paths
}

func CandidatePathForUser(sshUser string) string {
	return config.GetComposeYMLPath(sshUser)
}

func ReadComposeBytes(exec ReadFileExecutor, sshUser string) ([]byte, error) {
	var lastErr error
	for _, p := range composeYMLPaths(sshUser) {
		b, err := exec.ReadFile(p)
		if err == nil && len(b) > 0 {
			return b, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("could not read compose.yml (tried %v): %w", composeYMLPaths(sshUser), lastErr)
}

// FindComposeEnvVar returns the value of varName from the environment of
// serviceKey in the given compose file. Environment entries are "KEY=VALUE"
// strings.
func FindComposeEnvVar(content []byte, serviceKey, varName string) (string, error) {
	var compose ComposeFile
	if err := yaml.Unmarshal(content, &compose); err != nil {
		return "", fmt.Errorf("failed to parse compose.yml: %w", err)
	}
	svc, ok := compose.Services[serviceKey]
	if !ok {
		return "", fmt.Errorf("service %q not found in compose.yml", serviceKey)
	}
	for _, env := range svc.Environment {
		if key, val, found := strings.Cut(env, "="); found && key == varName {
			return val, nil
		}
	}
	return "", fmt.Errorf("%s not found in service %q", varName, serviceKey)
}

// ReadFileExecutor is the subset of remote.CommandExecutor used to pull
// files off a host, so the compose helpers work with both SSH and local
// executors.
type ReadFileExecutor interface {
	ReadFile(path string) ([]byte, error)
}

// The installer emits one env var per line; rewriting in place keeps the rest
// of the compose byte-identical, which a yaml round-trip would break.
func SetComposeEnvVars(content []byte, varName, newValue string) ([]byte, int, error) {
	re := regexp.MustCompile(`(?m)^([ \t]*- ` + regexp.QuoteMeta(varName) + `=)[^\n]*`)
	changed := 0
	out := re.ReplaceAllStringFunc(string(content), func(m string) string {
		if strings.HasSuffix(m, "="+newValue) {
			return m
		}
		changed++
		return m[:strings.Index(m, "=")+1] + newValue
	})
	return []byte(out), changed, nil
}

const pipelineDir = "/utmstack/events-engine-workdir/pipeline"

// pluginKeyLines are the scalar fields the V12 installer bakes with the
// instance key into the pipeline YAML.
var pluginKeyLines = []string{"internalKey", "encryptionKey"}

// pipelineExecutor is the minimal surface SyncPipeline needs, so the pipeline
// helpers work with both SSH and local executors and with test fakes.
type pipelineExecutor interface {
	ExecuteCommandWithSudo(command string) (string, error)
	WriteFile(path string, content []byte, perm os.FileMode) error
}

// SyncPipeline rewrites the key scalars in every yaml of the destination
// pipeline dir. The plugin SDK reads those files for its config, so the
// migrated ciphertext is only decryptable once they carry the synced key.
// Reads go through sudo (files are root-owned); a missing or empty dir is a
// warning, not an error. Returns whether any file changed.
func SyncPipeline(exec pipelineExecutor, internalKey string) (bool, error) {
	out, err := exec.ExecuteCommandWithSudo("ls " + pipelineDir + " 2>/dev/null || true")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(out) == "" {
		ui.Warning("Pipeline directory not found — skipping plugin config key sync")
		return false, nil
	}
	anyChanged := false
	for _, name := range strings.Fields(out) {
		if !strings.HasSuffix(name, ".yaml") {
			continue
		}
		path := pipelineDir + "/" + name
		content, err := exec.ExecuteCommandWithSudo("cat " + path)
		if err != nil {
			ui.Warning("Cannot read " + path + ": " + err.Error())
			continue
		}
		updated := []byte(content)
		changed := 0
		for _, k := range pluginKeyLines {
			u, n := ReplaceKeyLines(updated, k, internalKey)
			updated = u
			changed += n
		}
		if changed == 0 {
			continue
		}
		if err := exec.WriteFile(path, updated, 0600); err != nil {
			return anyChanged, fmt.Errorf("writing %s: %w", path, err)
		}
		anyChanged = true
		ui.Info("Updated plugin config keys in " + name)
	}
	return anyChanged, nil
}

// ReplaceKeyLines rewrites every "keyName: <value>" line to carry newValue,
// preserving the line's original indentation.
func ReplaceKeyLines(content []byte, keyName, newValue string) ([]byte, int) {
	lines := strings.Split(string(content), "\n")
	changed := 0
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, keyName+":") {
			continue
		}
		if trimmed == keyName+": "+newValue {
			continue
		}
		ind := strings.Index(line, keyName+":")
		lines[i] = line[:ind] + keyName + ": " + newValue
		changed++
	}
	return []byte(strings.Join(lines, "\n")), changed
}
