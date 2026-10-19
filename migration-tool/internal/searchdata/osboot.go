package searchdata

import (
	"fmt"

	"github.com/utmstack/UTMStackEnterprise/migration-tool/internal/remote"
	"github.com/utmstack/UTMStackEnterprise/migration-tool/pkg/ui"
)

const (
	// PhantomName is the transient standalone OpenSearch container the upgrade
	// boots over the preserved V11 volume after V12 is installed.
	PhantomName   = "utmstack_migration_os"
	phantomVolume = "/utmstack/opensearch/data"
)

// Phantom boots and stops a single-node OpenSearch over the V11 volume so the
// upgrade can stream its data into V12 ClickHouse without a disk dump.
type Phantom struct {
	exec  remote.CommandExecutor
	image string
	pass  string
}

func NewPhantom(exec remote.CommandExecutor, image, pass string) *Phantom {
	return &Phantom{exec: exec, image: image, pass: pass}
}

func (p *Phantom) runCommand() string {
	return fmt.Sprintf(
		"docker run -d --name %s -p 9200:9200 -v %s:/usr/share/opensearch/data "+
			"-e cluster.name=utmstack -e node.name=node1 -e discovery.type=single-node "+
			"-e bootstrap.memory_lock=false "+
			"-e OPENSEARCH_INITIAL_ADMIN_PASSWORD='%s' "+
			"-e action.auto_create_index=true -e 'OPENSEARCH_JAVA_OPTS=-Xms512m -Xmx512m' "+
			"-e network.host=0.0.0.0 %s",
		PhantomName, phantomVolume, p.pass, p.image,
	)
}

func (p *Phantom) running() bool {
	out, _ := p.exec.ExecuteCommandWithSudo("docker ps -q --filter name=^" + PhantomName + "$")
	return trimRight(out) != ""
}

func (p *Phantom) exists() bool {
	out, _ := p.exec.ExecuteCommandWithSudo("docker ps -a -q --filter name=^" + PhantomName + "$")
	return trimRight(out) != ""
}

// Boot pulls the image when missing and starts the phantom container. It is
// idempotent: a running phantom is reused, a stopped one is restarted.
func (p *Phantom) Boot() error {
	if p.image == "" {
		return fmt.Errorf("phantom image tag was not captured")
	}
	if p.pass == "" {
		return fmt.Errorf("phantom OpenSearch password was not captured")
	}
	if p.running() {
		ui.Info("Phantom OpenSearch already running, reusing it")
		return nil
	}
	if p.exists() {
		ui.Info("Phantom OpenSearch container found (stopped), starting it")
		if out, err := p.exec.ExecuteCommandWithSudo("docker start " + PhantomName); err != nil {
			return fmt.Errorf("docker start phantom: %v (out: %s)", err, trimRight(out))
		}
		ui.Success("Phantom OpenSearch started")
		return nil
	}
	if out, err := p.exec.ExecuteCommandWithSudo("docker image inspect " + p.image + " >/dev/null 2>&1"); err != nil {
		ui.StartOperation("Pulling OpenSearch image " + p.image)
		if _, err := p.exec.ExecuteCommandWithSudo("docker pull " + p.image); err != nil {
			return fmt.Errorf("docker pull %s: %v (out: %s)", p.image, err, trimRight(out))
		}
		ui.Success("Image pulled")
	}
	ui.StartOperation("Starting phantom OpenSearch over " + phantomVolume)
	out, err := p.exec.ExecuteCommandWithSudo(p.runCommand())
	if err != nil {
		return fmt.Errorf("docker run phantom: %v (out: %s)", err, trimRight(out))
	}
	ui.Success("Phantom OpenSearch started")
	return nil
}

// Teardown removes the phantom container. Call it only after a clean stream.
func (p *Phantom) Teardown() error {
	out, err := p.exec.ExecuteCommandWithSudo("docker rm -f " + PhantomName)
	if err != nil {
		return fmt.Errorf("docker rm -f %s: %v (out: %s)", PhantomName, err, trimRight(out))
	}
	ui.Success("Phantom OpenSearch removed")
	return nil
}

func trimRight(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == ' ') {
		s = s[:len(s)-1]
	}
	return s
}
