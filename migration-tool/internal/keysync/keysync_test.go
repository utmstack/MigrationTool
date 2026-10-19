package keysync

import (
	"os"
	"strings"
	"testing"
)

type fakeExec struct {
	inspectOut   string
	lsOut        string
	deploy       string
	updates      []string
	pipelineLS   string
	pipelineFile string
	files        map[string][]byte
	writes       map[string][]byte
}

func (f *fakeExec) Close() error { return nil }

func (f *fakeExec) ReadFile(path string) ([]byte, error) {
	b, ok := f.files[path]
	if !ok {
		return nil, os.ErrNotExist
	}
	return b, nil
}

func (f *fakeExec) WriteFile(path string, content []byte, _ os.FileMode) error {
	f.writes[path] = content
	return nil
}

func (f *fakeExec) ExecuteCommand(_ string) (string, error) { return "", nil }

func (f *fakeExec) ExecuteCommandWithSudo(command string) (string, error) {
	switch {
	case strings.Contains(command, "ls /utmstack/events-engine-workdir/pipeline"):
		return f.pipelineLS, nil
	case strings.Contains(command, "cat /utmstack/events-engine-workdir/pipeline/"):
		return f.pipelineFile, nil
	case strings.Contains(command, "docker service ls --quiet --filter \"name=utmstack_event-processor\""):
		return "<service-id>\n", nil
	case strings.Contains(command, "--format '{{.Spec.Name}}'"):
		return "utmstack_event-processor-worker\n", nil
	case strings.Contains(command, "--format '{{.Spec.TaskTemplate.ContainerSpec.Env}}'"):
		return "[\"WORK_DIR=/workdir\",\"MODE=worker\"]\n", nil
	case strings.Contains(command, "docker service update --force"):
		f.updates = append(f.updates, command)
		return "", nil
	case strings.Contains(command, "docker service inspect"):
		return f.inspectOut, nil
	case strings.Contains(command, "docker stack deploy"):
		f.deploy = command
		return "", nil
	case strings.Contains(command, "docker service ls"):
		return f.lsOut, nil
	}
	return "", nil
}

func (f *fakeExec) ExecuteCommandWithSudoStreaming(_ string) (string, error) {
	return "", nil
}

func (f *fakeExec) FileExists(path string) (bool, error) {
	_, ok := f.files[path]
	return ok, nil
}

const oldCompose = `services:
  backend:
    image: ghcr.io/utmstack/utmstack/backend:${UTMSTACK_TAG}
    environment:
      - POSTGRES_HOST=utmstack_postgres
      - ENCRYPTION_KEY=oldkey
      - INTERNAL_KEY=oldkey
`

func newFake(inspectOut string) *fakeExec {
	return &fakeExec{
		inspectOut: inspectOut,
		lsOut:      "ID NAME IMAGE",
		files: map[string][]byte{
			"/root/utmstack.yml":         []byte("internal_key: oldkey\n"),
			"/home/utmstack/compose.yml": []byte(oldCompose),
		},
		writes: map[string][]byte{},
	}
}

func TestSyncDeploysWithRunningTagNotDigest(t *testing.T) {
	// The running image ref carries the digest; the tag is what the compose
	// ${UTMSTACK_TAG} variable must resolve to, or every service is redeployed
	// against an image that does not exist locally.
	f := newFake("ghcr.io/utmstack/utmstack/frontend:v12.0.0@sha256:ef7ebc41a5c1bbffffc880e433024d86b2b69f3cf5121b9e386af3f67029834d\n")

	if err := Sync(f, "utmstack", "newkey"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if !strings.Contains(f.deploy, "UTMSTACK_TAG=v12.0.0 docker stack deploy") {
		t.Errorf("deploy must resolve UTMSTACK_TAG to the running tag, got: %q", f.deploy)
	}
	if strings.Contains(f.deploy, "ef7ebc41") {
		t.Errorf("deploy must not use the digest as tag, got: %q", f.deploy)
	}
	composeOut := string(f.writes["/home/utmstack/compose.yml"])
	if strings.Count(composeOut, "newkey") != 2 {
		t.Errorf("compose must carry the new key on both env vars:\n%s", composeOut)
	}
	ymlOut := string(f.writes["/root/utmstack.yml"])
	if !strings.Contains(ymlOut, "internal_key: newkey") {
		t.Errorf("utmstack.yml must carry the new key, got: %s", ymlOut)
	}
	// fake pipeline dir is empty, so the worker is not force-updated.
	if len(f.updates) != 0 {
		t.Errorf("expected no force update with an empty pipeline, got: %v", f.updates)
	}
}

func TestSyncPipelineOnlyChangeForceUpdatesWorker(t *testing.T) {
	// compose already holds the key: only the pipeline changed. The deploy is
	// then a no-op for the worker spec, so the worker must be force-updated to
	// reload the key it cached at startup.
	f := newFake("ghcr.io/utmstack/utmstack/frontend:v12.0.0-abc\n")
	f.files["/home/utmstack/compose.yml"] = []byte(strings.ReplaceAll(oldCompose, "oldkey", "newkey"))
	f.pipelineLS = "utmstack_plugins.yaml\n"
	f.pipelineFile = "        internalKey: oldkey\n        encryptionKey: oldkey\n"
	if err := Sync(f, "utmstack", "newkey"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if f.deploy == "" {
		t.Fatalf("deploy must run when the pipeline changed")
	}
	if len(f.updates) != 1 || !strings.Contains(f.updates[0], "utmstack_event-processor-worker") {
		t.Fatalf("expected a force update of the worker, got: %v", f.updates)
	}
}

func TestSyncAllInSyncIsNoOp(t *testing.T) {
	f := newFake("ghcr.io/utmstack/utmstack/frontend:v12.0.0-abc\n")
	f.files["/home/utmstack/compose.yml"] = []byte(strings.ReplaceAll(oldCompose, "oldkey", "newkey"))
	f.pipelineLS = "utmstack_plugins.yaml\n"
	f.pipelineFile = "        internalKey: newkey\n        encryptionKey: newkey\n"
	if err := Sync(f, "utmstack", "newkey"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if f.deploy != "" {
		t.Errorf("expected no deploy when everything is in sync, got: %q", f.deploy)
	}
	if len(f.updates) != 0 {
		t.Errorf("expected no force update when everything is in sync, got: %v", f.updates)
	}
}

func TestSyncSkipsDeployWhenComposeAlreadyHoldsKey(t *testing.T) {
	f := newFake("ghcr.io/utmstack/utmstack/frontend:v12.0.0@sha256:ef7ebc41")
	f.files["/home/utmstack/compose.yml"] = []byte(strings.ReplaceAll(oldCompose, "oldkey", "newkey"))

	if err := Sync(f, "utmstack", "newkey"); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if f.deploy != "" {
		t.Errorf("expected no deploy when the compose already holds the key, got: %q", f.deploy)
	}
	if _, wrote := f.writes["/home/utmstack/compose.yml"]; wrote {
		t.Error("compose must not be rewritten on a no-change sync")
	}
}
