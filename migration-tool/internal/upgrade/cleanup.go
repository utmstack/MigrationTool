package upgrade

import (
	"fmt"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

func (u *Upgrader) cleanupInstance() error {
	ui.SubHeader("Phase 3: Cleanup V11")
	ui.Blank()

	commands := []struct {
		description string
		command     string
	}{
		{
			description: "Removing Swarm services",
			command:     "bash -c 'docker service ls -q | xargs -r docker service rm 2>/dev/null || true'",
		},
		{
			description: "Leaving Swarm cluster",
			command:     "docker swarm leave --force 2>/dev/null || true",
		},
		{
			description: "Stopping containers",
			command:     "bash -c 'docker stop $(docker ps -aq) 2>/dev/null || true'",
		},
		{
			description: "Removing containers",
			command:     "bash -c 'docker rm -f $(docker ps -aq) 2>/dev/null || true'",
		},
		{
			description: "Stopping Docker daemon",
			command:     "systemctl stop docker docker.socket containerd 2>/dev/null || true",
		},
		{
			description: "Waiting for release",
			command:     "sleep 3",
		},
		{
			description: "Purging Docker packages",
			command:     "bash -c 'DEBIAN_FRONTEND=noninteractive apt-get purge docker* containerd* -y -qq 2>/dev/null || true'",
		},
		{
			description: "Removing compose files",
			command:     "rm -f /root/compose.yml /compose.yml",
		},
		{
			description: "Removing old installer",
			command:     "rm -f /home/*/installer /root/installer",
		},
		{
			description: "Purging Nginx packages",
			command:     "bash -c 'DEBIAN_FRONTEND=noninteractive apt-get purge nginx* -y -qq 2>/dev/null || true'",
		},
		{
			description: "Cleaning UTMStack directory",
			command:     "find /utmstack -mindepth 1 -maxdepth 1 ! -name 'opensearch' ! -name 'backups' -exec rm -rf {} + 2>/dev/null || true",
		},
		{
			description: "Unmounting overlay filesystems",
			command:     "umount $(mount | grep '/var/lib/docker/overlay2' | awk '{print $3}') 2>/dev/null || true",
		},
		{
			description: "Removing Docker data",
			command:     "rm -rf /var/lib/docker 2>/dev/null || true",
		},
		{
			description: "Cleaning apt cache",
			command:     "bash -c 'apt-get autoremove -y -qq 2>/dev/null || true; apt-get autoclean -y -qq 2>/dev/null || true'",
		},
	}

	total := len(commands)
	for i, cmd := range commands {
		ui.StepCompact(i+1, total, cmd.description)

		output, err := u.executor.ExecuteCommandWithSudo(cmd.command)
		if err != nil {
			ui.Warning(fmt.Sprintf("%v (output: %s)", err, output))
		} else {
			ui.Success("Done")
		}
	}

	ui.Blank()
	ui.Success("Cleanup completed")
	return nil
}
