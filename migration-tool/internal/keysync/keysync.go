// Package keysync propagates a V11 internal key to a destination V12
// instance: it updates /root/utmstack.yml, the plugin pipeline YAML,
// and the live compose env (ENCRYPTION_KEY/INTERNAL_KEY), then redeploys
// the stack if any of them changed, so source-encrypted credentials
// decrypt correctly there.
package keysync

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/compose"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/config"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/yaml"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

var internalKeyEnvVars = []string{"ENCRYPTION_KEY", "INTERNAL_KEY"}

// V12 services decrypt stored credentials with ENCRYPTION_KEY/INTERNAL_KEY,
// which the V12 installer bakes into compose.yml at install time. The V11
// ciphertext migrated over is encrypted with the V11 key, so until the
// destination compose carries that key every decrypt on the destination
// produces garbage (e.g. Azure rejects the O365 client secret). utmstack.yml
// alone is not enough: running services read the compose env, not the yml.
func Sync(exec remote.CommandExecutor, sshUser, internalKey string) error {
	if internalKey == "" {
		ui.Info("No internal key to sync")
		return nil
	}

	existing, err := exec.ReadFile(config.UTMStackYMLPath)
	if err != nil {
		existing = []byte("internal_key: " + internalKey + "\n")
	}
	updatedYML, err := yaml.UpdateInternalKeyInYAML(existing, internalKey)
	if err != nil {
		updatedYML = []byte("internal_key: " + internalKey + "\n")
	}
	if err := exec.WriteFile(config.UTMStackYMLPath, updatedYML, 0600); err != nil {
		return fmt.Errorf("writing %s: %w", config.UTMStackYMLPath, err)
	}

	// The plugin SDK reads the key from the pipeline YAML, not the compose env;
	// it must carry it before the redeploy restarts the services.
	pipelineChanged, err := compose.SyncPipeline(exec, internalKey)
	if err != nil {
		return err
	}

	composeContent, err := compose.ReadComposeBytes(exec, sshUser)
	if err != nil {
		return err
	}
	composePath := compose.CandidatePathForUser(sshUser)
	changed := 0
	for _, v := range internalKeyEnvVars {
		var n int
		composeContent, n, err = compose.SetComposeEnvVars(composeContent, v, internalKey)
		if err != nil {
			return err
		}
		changed += n
	}
	if changed == 0 && !pipelineChanged {
		ui.Success("Security credentials already in sync")
		return nil
	}
	if changed > 0 {
		if err := exec.WriteFile(composePath, composeContent, 0644); err != nil {
			return fmt.Errorf("writing %s: %w", composePath, err)
		}
	}

	// The plugin caches its key in memory at startup, so a pipeline change
	// needs a restart to take effect. A compose change alone is enough because
	// docker stack deploy recreates the services with the new env. But a
	// pipeline-only change does not alter the service spec, so the deploy is a
	// no-op for the worker and we must force-update it explicitly.
	ui.Info("Propagating internal key to running services (stack redeploy, brief downtime)")
	tag, err := currentStackTag(exec)
	if err != nil {
		return err
	}
	deploy := "UTMSTACK_TAG=" + tag + " docker stack deploy -c " + composePath + " utmstack"
	if _, err := exec.ExecuteCommandWithSudo(deploy); err != nil {
		return fmt.Errorf("stack deploy: %w", err)
	}
	if err := waitStackConvergence(exec); err != nil {
		return err
	}
	if pipelineChanged {
		if err := forceUpdatePipelineServices(exec); err != nil {
			return fmt.Errorf("restarting event processors: %w", err)
		}
	}
	ui.Success("Security credentials synchronized")
	return nil
}

// currentStackTag reads the image tag the running stack uses, from the
// frontend service; the compose references images as ${UTMSTACK_TAG}, so the
// redeploy must export the same tag or docker rejects the reference.
func currentStackTag(exec remote.CommandExecutor) (string, error) {
	out, err := exec.ExecuteCommandWithSudo("docker service inspect utmstack_frontend --format '{{.Spec.TaskTemplate.ContainerSpec.Image}}'")
	if err != nil {
		return "", fmt.Errorf("inspect frontend image: %w", err)
	}
	tag := strings.TrimSpace(out)
	// Image refs carry a digest ("tag@sha256:..."); cut it off, otherwise
	// the last ":" lands inside the digest and the tag comes out as one.
	if at := strings.Index(tag, "@"); at >= 0 {
		tag = tag[:at]
	}
	i := strings.LastIndex(tag, ":")
	if i < 0 {
		return "", fmt.Errorf("no tag in image %q", tag)
	}
	return tag[i+1:], nil
}

// forceUpdatePipelineServices restarts every event processor that reads the
// pipeline YAML. docker stack deploy only recreates a service when its spec
// changes, and a pipeline-only key fix changes no spec, so the worker would
// keep the key it cached at startup without this.
func forceUpdatePipelineServices(exec remote.CommandExecutor) error {
	services, err := exec.ExecuteCommandWithSudo(`docker service ls --quiet --filter "name=utmstack_event-processor"`)
	if err != nil {
		return err
	}
	for _, id := range strings.Fields(services) {
		name, err := exec.ExecuteCommandWithSudo(`docker service inspect ` + id + ` --format '{{.Spec.Name}}'`)
		if err != nil {
			return err
		}
		env, err := exec.ExecuteCommandWithSudo(`docker service inspect ` + id + ` --format '{{.Spec.TaskTemplate.ContainerSpec.Env}}'`)
		if err != nil {
			return err
		}
		if !strings.Contains(env, "WORK_DIR=/workdir") {
			continue
		}
		ui.Info("Restarting " + name + " to reload the plugin key")
		if _, err := exec.ExecuteCommandWithSudo(`docker service update --force ` + name); err != nil {
			return err
		}
	}
	return nil
}

func waitStackConvergence(exec remote.CommandExecutor) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for stack convergence: %w", ctx.Err())
		case <-ticker.C:
			out, err := exec.ExecuteCommandWithSudo("docker service ls")
			if err != nil {
				continue
			}
			if !strings.Contains(out, "updating") {
				return nil
			}
		}
	}
}
