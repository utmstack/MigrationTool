package compose

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// fakeSudoExecutor records every command run and returns canned outputs,
// enough to drive SyncPipeline without a network.
type fakeSudoExecutor struct {
	commands []string
	outputs  map[string]string
	files    map[string][]byte
}

func (f *fakeSudoExecutor) Close() error { return nil }

func (f *fakeSudoExecutor) ReadFile(path string) ([]byte, error) {
	return f.files[path], nil
}

func (f *fakeSudoExecutor) WriteFile(path string, content []byte, perm os.FileMode) error {
	f.files[path] = content
	return nil
}

func (f *fakeSudoExecutor) ExecuteCommand(command string) (string, error) {
	return "", nil
}

func (f *fakeSudoExecutor) ExecuteCommandWithSudo(command string) (string, error) {
	f.commands = append(f.commands, command)
	if out, ok := f.outputs[command]; ok {
		return out, nil
	}
	if strings.HasPrefix(command, "cat ") {
		if b, ok := f.files[strings.TrimPrefix(command, "cat ")]; ok {
			return string(b), nil
		}
		return "", fmt.Errorf("cat: no such file")
	}
	return "", nil
}

func (f *fakeSudoExecutor) ExecuteCommandWithSudoStreaming(command string) (string, error) {
	f.commands = append(f.commands, command)
	return "", nil
}

func (f *fakeSudoExecutor) FileExists(path string) (bool, error) {
	return len(f.files[path]) > 0, nil
}

func TestReplaceKeyLines(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		key      string
		value    string
		wantOut  string
		wantDiff int
	}{
		{
			name:     "rewrites a changed value keeping indentation",
			input:    "plugins:\n    com.utmstack:\n        internalKey: old\n        encryptionKey: old\n",
			key:      "internalKey",
			value:    "new",
			wantOut:  "plugins:\n    com.utmstack:\n        internalKey: new\n        encryptionKey: old\n",
			wantDiff: 1,
		},
		{
			name:     "no-op when the value already matches",
			input:    "        internalKey: same\n",
			key:      "internalKey",
			value:    "same",
			wantOut:  "        internalKey: same\n",
			wantDiff: 0,
		},
		{
			name:     "leaves other keys alone",
			input:    "        internalKey: old\n        password: secret\n",
			key:      "internalKey",
			value:    "new",
			wantOut:  "        internalKey: new\n        password: secret\n",
			wantDiff: 1,
		},
		{
			name:     "does not match a key embedded in a longer word",
			input:    "        xinternalKey: old\n",
			key:      "internalKey",
			value:    "new",
			wantOut:  "        xinternalKey: old\n",
			wantDiff: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, diff := ReplaceKeyLines([]byte(tt.input), tt.key, tt.value)
			if string(got) != tt.wantOut {
				t.Errorf("got %q, want %q", got, tt.wantOut)
			}
			if diff != tt.wantDiff {
				t.Errorf("diff got %d, want %d", diff, tt.wantDiff)
			}
		})
	}
}

func TestSyncPipeline(t *testing.T) {
	tests := []struct {
		name        string
		files       map[string][]byte
		key         string
		wantChanged bool
	}{
		{
			name: "rewrites a changed key",
			files: map[string][]byte{
				"/utmstack/events-engine-workdir/pipeline/utmstack_plugins.yaml": []byte("internalKey: old\nencryptionKey: old\n"),
			},
			key:         "new",
			wantChanged: true,
		},
		{
			name: "no-op when already synced",
			files: map[string][]byte{
				"/utmstack/events-engine-workdir/pipeline/utmstack_plugins.yaml": []byte("internalKey: new\nencryptionKey: new\n"),
			},
			key:         "new",
			wantChanged: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ls must list the yaml file that exists
			list := ""
			for name := range tt.files {
				list += name[strings.LastIndex(name, "/")+1:] + "\n"
			}
			f := &fakeSudoExecutor{
				outputs: map[string]string{"ls " + pipelineDir + " 2>/dev/null || true": list},
				files:   tt.files,
			}
			changed, err := SyncPipeline(f, tt.key)
			if err != nil {
				t.Fatalf("err=%v", err)
			}
			if changed != tt.wantChanged {
				t.Errorf("changed got %v, want %v", changed, tt.wantChanged)
			}
		})
	}
}
