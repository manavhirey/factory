package worker

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/owainlewis/factory/internal/protocol"
)

func TestEmptyPluginSetPreservesPromptBytes(t *testing.T) {
	claim := protocol.Claim{
		Task:       protocol.Task{Title: "Build calculator", Description: "Implement the approved spec."},
		Repository: protocol.Repository{RemoteIdentity: "example.invalid/calculator"},
	}
	want := "You are running in a Factory managed Git worktree.\n" +
		"Work only on the assigned task and repository. Preserve unrelated changes and do not touch Factory state or unrelated worktrees. " +
		"and do not delete worktrees or branches. Complete and verify the task before returning a concise result.\n\n" +
		"Task title: Build calculator\n" +
		"Repository: example.invalid/calculator\n\n" +
		"Implement the approved spec."
	if got := buildPrompt(claim, []Plugin{}); got != want {
		t.Fatalf("control prompt changed:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestLoadPluginsSortsAndComposesDeterministicContext(t *testing.T) {
	root := t.TempDir()
	writePluginFixture(t, root, "z-review", "1.2.0", "Z prompt", "codex", []string{"dotnet"})
	writePluginFixture(t, root, "a-review", "0.1.0", "A prompt", "codex", nil)
	plugins, err := loadPlugins(Config{
		Runtime: "codex", PluginDirectory: root,
		EnabledPlugins: []string{"z-review", "a-review"},
	}, func(command string) (string, error) {
		if command == "dotnet" {
			return "/test/bin/dotnet", nil
		}
		return "", errors.New("missing")
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pluginIdentities(plugins), []string{"a-review@0.1.0", "z-review@1.2.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plugin identities = %#v, want %#v", got, want)
	}
	claim := protocol.Claim{
		Task:       protocol.Task{Title: "Task", Description: "Description"},
		Repository: protocol.Repository{RemoteIdentity: "example.invalid/repo"},
	}
	prompt := buildPrompt(claim, plugins)
	aIndex := strings.Index(prompt, "BEGIN FACTORY PLUGIN a-review@0.1.0")
	zIndex := strings.Index(prompt, "BEGIN FACTORY PLUGIN z-review@1.2.0")
	taskIndex := strings.Index(prompt, "Task title: Task")
	if aIndex < 0 || zIndex <= aIndex || taskIndex <= zIndex {
		t.Fatalf("plugin prompt ordering is not deterministic:\n%s", prompt)
	}
	if !strings.Contains(prompt, "A prompt") || !strings.Contains(prompt, "Z prompt") {
		t.Fatalf("plugin prompt content missing:\n%s", prompt)
	}
}

func TestLoadPluginsRejectsUnsafeOrIncompleteBundles(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
		lookup commandLookup
		want   string
	}{
		{
			name: "unknown manifest field",
			mutate: func(t *testing.T, directory string) {
				appendFile(t, filepath.Join(directory, "plugin.toml"), "unsafe_hook = \"run-me\"\n")
			},
			want: "unknown fields",
		},
		{
			name: "runtime mismatch",
			mutate: func(t *testing.T, directory string) {
				replaceFile(t, filepath.Join(directory, "plugin.toml"), `runtime = "codex"`, `runtime = "claude-code"`)
			},
			want: "does not match worker runtime",
		},
		{
			name: "prompt traversal",
			mutate: func(t *testing.T, directory string) {
				replaceFile(t, filepath.Join(directory, "plugin.toml"), `prompt_file = "prompt.md"`, `prompt_file = "../outside.md"`)
			},
			want: "local relative path",
		},
		{
			name: "symlinked prompt",
			mutate: func(t *testing.T, directory string) {
				prompt := filepath.Join(directory, "prompt.md")
				if err := os.Remove(prompt); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(filepath.Dir(directory), "outside.md")
				if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, prompt); err != nil {
					t.Fatal(err)
				}
			},
			want: "regular non-symlink",
		},
		{
			name:   "missing command",
			mutate: func(t *testing.T, directory string) {},
			lookup: func(command string) (string, error) {
				return "", errors.New("missing")
			},
			want: "required command",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			directory := writePluginFixture(t, root, "dotnet-quality", "0.1.0", "Review", "codex", []string{"dotnet"})
			test.mutate(t, directory)
			lookup := test.lookup
			if lookup == nil {
				lookup = func(command string) (string, error) { return "/test/bin/" + command, nil }
			}
			_, err := loadPlugins(Config{
				Runtime: "codex", PluginDirectory: root,
				EnabledPlugins: []string{"dotnet-quality"},
			}, lookup)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestValidateConfigRequiresAbsolutePluginDirectory(t *testing.T) {
	config := Config{
		Server: "http://127.0.0.1:7337", Name: "test", Runtime: "codex",
		MaxConcurrent: 1, DataDirectory: t.TempDir(),
		PluginDirectory: "relative/plugins", EnabledPlugins: []string{"dotnet-quality"},
	}
	if err := validateConfig(config); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative plugin directory error = %v", err)
	}
}

func TestPluginDependencyHealthIsRechecked(t *testing.T) {
	plugins := []Plugin{{ID: "dotnet-quality", RequiredCommands: []string{"dotnet", "cwm-roslyn-navigator"}}}
	available := map[string]bool{"dotnet": true, "cwm-roslyn-navigator": true}
	lookup := func(command string) (string, error) {
		if available[command] {
			return "/test/bin/" + command, nil
		}
		return "", errors.New("missing")
	}
	if err := checkPluginDependencies(plugins, lookup); err != nil {
		t.Fatalf("initial plugin health = %v", err)
	}
	delete(available, "cwm-roslyn-navigator")
	if err := checkPluginDependencies(plugins, lookup); err == nil || !strings.Contains(err.Error(), "cwm-roslyn-navigator") {
		t.Fatalf("missing dependency health error = %v", err)
	}
}

func TestBundledDotnetQualityPluginLoads(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	plugins, err := loadPlugins(Config{
		Runtime: "codex", PluginDirectory: root,
		EnabledPlugins: []string{"dotnet-quality"},
	}, func(command string) (string, error) { return "/test/bin/" + command, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pluginIdentities(plugins), []string{"dotnet-quality@0.1.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bundled plugin identities = %#v, want %#v", got, want)
	}
	if got, want := plugins[0].ContextFiles, []string{"codex/agents/dotnet-quality-reviewer.toml"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bundled plugin context files = %#v, want %#v", got, want)
	}
}

func TestManifestRejectsUnsortedPluginEvidence(t *testing.T) {
	dataDirectory := t.TempDir()
	workerID := fixtureUUID(1)
	manifest := fixtureManifest(dataDirectory, workerID, 10)
	manifest.ActivePlugins = []string{"z-review@1.0.0", "a-review@1.0.0"}
	if err := newManifestStore(dataDirectory, workerID).validate(manifest); err == nil ||
		!strings.Contains(err.Error(), "unique and sorted") {
		t.Fatalf("unsorted plugin evidence error = %v", err)
	}
}

func writePluginFixture(t *testing.T, root, id, version, prompt, runtime string, commands []string) string {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	quotedCommands := make([]string, 0, len(commands))
	for _, command := range commands {
		quotedCommands = append(quotedCommands, fmt.Sprintf("%q", command))
	}
	manifest := fmt.Sprintf(`schema_version = 1
id = %q
version = %q
description = "Test plugin"
runtime = %q
prompt_file = "prompt.md"
required_commands = [%s]
instruction_sets = ["code-review"]

[provenance]
repository = "https://example.invalid/plugin.git"
commit = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
license = "MIT"
`, id, version, runtime, strings.Join(quotedCommands, ", "))
	if err := os.WriteFile(filepath.Join(directory, "plugin.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "prompt.md"), []byte(prompt), 0o600); err != nil {
		t.Fatal(err)
	}
	return directory
}

func appendFile(t *testing.T, path, value string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(value); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func replaceFile(t *testing.T, path, old, replacement string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(body), old, replacement, 1)
	if updated == string(body) {
		t.Fatalf("fixture text %q not found", old)
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}
