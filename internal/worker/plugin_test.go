package worker

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

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
			want: "symlink component",
		},
		{
			name: "missing declared reviewer asset",
			mutate: func(t *testing.T, directory string) {
				if err := os.Remove(filepath.Join(directory, "reviewer-skills", "code-review", "SKILL.md")); err != nil {
					t.Fatal(err)
				}
			},
			want: "reviewer asset",
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
		PluginArtifactDirectory: "/opt/factory/plugin-artifacts", EnabledPlugins: []string{"dotnet-quality"},
	}, func(command string) (string, error) { return "/test/bin/" + command, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got, want := pluginIdentities(plugins), []string{"dotnet-quality@0.2.0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bundled plugin identities = %#v, want %#v", got, want)
	}
	if got, want := plugins[0].ContextFiles, []string{"codex/agents/dotnet-quality-reviewer.toml"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bundled plugin context files = %#v, want %#v", got, want)
	}
	for _, instructionSet := range plugins[0].InstructionSets {
		if strings.Contains(plugins[0].Prompt, instructionSet) {
			t.Fatalf("implementation-parent prompt leaks reviewer instruction %q", instructionSet)
		}
	}
	if !strings.Contains(plugins[0].Prompt, `fork_turns="none"`) {
		t.Fatal("implementation-parent prompt does not require a context-isolated custom-role spawn")
	}
	if got := plugins[0].HealthChecks; len(got) != 1 || got[0].SemanticTool != "find_symbol" {
		t.Fatalf("bundled plugin semantic health checks = %#v", got)
	}
	artifact := plugins[0].Artifacts[0]
	if artifact.Name != "CWM.RoslynNavigator" || artifact.Version != "0.8.0" ||
		artifact.PackageSHA256 != "18432346439a1f1a1fdc1b82f7f944a4e6f4202347a2cfece7ee75992530cfcd" ||
		artifact.ExecutableSHA256 != "becde1c2c2b4a478f099d16f5c5dc426365fa4c7d8c0d656fe7e031086b2bb5d" {
		t.Fatalf("bundled Roslyn artifact pin = %#v", artifact)
	}
	if got := plugins[0].HealthChecks[0].Arguments; len(got) != 1 || got[0] != "/opt/factory/plugin-artifacts/dotnet-quality/CWM.RoslynNavigator.0.8.0/tools/net10.0/any/CWM.RoslynNavigator.dll" {
		t.Fatalf("bundled Roslyn command arguments = %#v", got)
	}
}

func TestBundledDotnetQualityPluginRealHealth(t *testing.T) {
	if os.Getenv("FACTORY_PLUGIN_REAL_HEALTH") != "1" {
		t.Skip("set FACTORY_PLUGIN_REAL_HEALTH=1 in an isolated provisioned worker")
	}
	root, err := filepath.Abs(filepath.Join("..", "..", "plugins"))
	if err != nil {
		t.Fatal(err)
	}
	plugins, err := loadPlugins(Config{
		Runtime: "codex", PluginDirectory: root,
		PluginArtifactDirectory: "/opt/factory/plugin-artifacts", EnabledPlugins: []string{"dotnet-quality"},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPluginActivation(context.Background(), plugins, nil, nil,
		os.Getenv("FACTORY_PLUGIN_SMOKE_CODEX_HOME")); err != nil {
		t.Fatal(err)
	}
}

func TestPluginActivationRequiresExactInstalledAgentAndSemanticProbe(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "reviewer.toml")
	if err := os.WriteFile(source, []byte("reviewed-agent"), 0o600); err != nil {
		t.Fatal(err)
	}
	codexHome := filepath.Join(root, ".codex")
	if err := os.MkdirAll(filepath.Join(codexHome, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(codexHome, "agents", "reviewer.toml")
	if err := os.WriteFile(installed, []byte("reviewed-agent"), 0o600); err != nil {
		t.Fatal(err)
	}
	called := false
	err := checkPluginActivation(context.Background(), []Plugin{{
		ID:            "dotnet-quality",
		ContextAssets: []PluginContextAsset{{RelativePath: "codex/agents/reviewer.toml", SourcePath: source}},
		HealthChecks: []PluginHealthCheck{{
			MCPServer: "cwm_roslyn_navigator", Command: "cwm-roslyn-navigator",
			Arguments: []string{"/reviewed/tool.dll"}, Environment: map[string]string{"REVIEW_MODE": "strict"},
			RequiredTools: []string{"find_symbol"}, SemanticTool: "find_symbol",
			SemanticArgs:   json.RawMessage(`{"name":"ReviewerHealthMarker"}`),
			ExpectedResult: "ReviewerHealthMarker", StartupTimeout: 1,
		}},
	}}, func(string) (string, error) { return "/test/bin/cwm-roslyn-navigator", nil },
		func(_ context.Context, spec mcpProbeSpec) error {
			called = spec.SemanticTool == "find_symbol" && spec.ExpectedResult == "ReviewerHealthMarker" &&
				reflect.DeepEqual(spec.Arguments, []string{"/reviewed/tool.dll"}) && spec.Environment["REVIEW_MODE"] == "strict"
			return nil
		}, codexHome)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("semantic MCP probe was not required")
	}
	if err := os.WriteFile(installed, []byte("drifted-agent"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = checkPluginActivation(context.Background(), []Plugin{{
		ID:            "dotnet-quality",
		ContextAssets: []PluginContextAsset{{RelativePath: "codex/agents/reviewer.toml", SourcePath: source}},
	}}, nil, nil, codexHome)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("drifted installed agent error = %v", err)
	}
}

func TestPluginArtifactRequiresPinnedPackageAndExecutableHashes(t *testing.T) {
	root := t.TempDir()
	pluginRoot := filepath.Join(root, "dotnet-quality")
	if err := os.Mkdir(pluginRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	executableBody := []byte("official executable")
	dependencyBody := []byte("official dependency")
	packageBody := testZip(t, map[string][]byte{"tool.dll": executableBody, "dependency.dll": dependencyBody})
	if err := os.WriteFile(filepath.Join(pluginRoot, "tool.nupkg"), packageBody, 0o600); err != nil {
		t.Fatal(err)
	}
	extracted := filepath.Join(pluginRoot, "expanded")
	if err := os.Mkdir(extracted, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extracted, "tool.dll"), executableBody, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extracted, "dependency.dll"), dependencyBody, 0o600); err != nil {
		t.Fatal(err)
	}
	artifact := PluginArtifact{
		Name: "CWM.RoslynNavigator", PackageFile: "tool.nupkg", ExtractedDirectory: "expanded", ExecutableFile: "expanded/tool.dll",
		PackageSHA256: testSHA256(packageBody), ExecutableSHA256: testSHA256(executableBody),
	}
	plugin := Plugin{ID: "dotnet-quality", ArtifactRoot: root}
	if err := verifyPluginArtifact(plugin, artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(extracted, "dependency.dll"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyPluginArtifact(plugin, artifact); err == nil || !strings.Contains(err.Error(), "does not match retained package") {
		t.Fatalf("extracted dependency drift error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(extracted, "dependency.dll"), dependencyBody, 0o600); err != nil {
		t.Fatal(err)
	}
	artifact.ExecutableSHA256 = strings.Repeat("0", 64)
	if err := verifyPluginArtifact(plugin, artifact); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("artifact hash mismatch error = %v", err)
	}
}

func TestArtifactHealthRejectsShellOrExtraArguments(t *testing.T) {
	wanted := "/opt/factory/tool.dll"
	for _, server := range []codexMCPServer{
		{Command: "sh", Args: []string{wanted}},
		{Command: "dotnet", Args: []string{wanted, "--extra"}},
	} {
		if err := validatePinnedArtifactServer(server, wanted); err == nil {
			t.Fatalf("unsafe server %#v was accepted", server)
		}
	}
	if err := validatePinnedArtifactServer(codexMCPServer{Command: "dotnet", Args: []string{wanted}}, wanted); err != nil {
		t.Fatal(err)
	}
}

func TestSecurePluginPathsRejectSymlinkAncestorsAndWritableDirectories(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "asset"), []byte("reviewed"), 0o600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(root, "linked")
	if err := os.Symlink(real, symlink); err != nil {
		t.Fatal(err)
	}
	if err := secureRegularFile(root, filepath.Join(symlink, "asset"), "asset"); err == nil || !strings.Contains(err.Error(), "symlink component") {
		t.Fatalf("symlink ancestor error = %v", err)
	}
	if err := os.Chmod(real, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := secureRegularFile(root, filepath.Join(real, "asset"), "asset"); err == nil || !strings.Contains(err.Error(), "world-writable") {
		t.Fatalf("writable ancestor error = %v", err)
	}
}

func TestInstalledReviewerAssetClosureRejectsUndeclaredFiles(t *testing.T) {
	codexHome := t.TempDir()
	root := filepath.Join(codexHome, "reviewer-skills", "dotnet-quality", "verify")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("reviewed"), 0o600); err != nil {
		t.Fatal(err)
	}
	plugin := Plugin{ID: "dotnet-quality", ReviewerAssets: []PluginContextAsset{{RelativePath: "reviewer-skills/verify/SKILL.md"}}}
	if err := verifyInstalledReviewerAssetClosure(codexHome, plugin); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "undeclared.md"), []byte("extra"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyInstalledReviewerAssetClosure(codexHome, plugin); err == nil || !strings.Contains(err.Error(), "reviewed manifest") {
		t.Fatalf("undeclared reviewer asset error = %v", err)
	}
}

func TestProbeMCPServerRequiresCleanProtocolAndSemanticCall(t *testing.T) {
	t.Setenv("FACTORY_MCP_PROBE_HELPER", "1")
	for _, test := range []struct {
		name    string
		mode    string
		want    string
		timeout time.Duration
	}{
		{name: "semantic success", mode: "success"},
		{name: "notification burst", mode: "notifications"},
		{name: "stdout log corruption", mode: "polluted", want: "not valid MCP JSON"},
		{name: "partial stdout", mode: "partial", want: "not valid MCP JSON"},
		{name: "early exit", mode: "exit", want: "not valid MCP JSON"},
		{name: "semantic result missing", mode: "missing-result", want: "before startup timeout", timeout: 750 * time.Millisecond},
		{name: "echoed marker not found", mode: "echo-not-found", want: "before startup timeout", timeout: 750 * time.Millisecond},
		{name: "tool error echoes marker", mode: "tool-error", want: "isError=true"},
		{name: "malformed structured content", mode: "malformed-result", want: "structured Roslyn result"},
		{name: "empty tool content", mode: "empty-result", want: "no non-empty text content"},
		{name: "timeout", mode: "timeout", want: "deadline exceeded", timeout: 250 * time.Millisecond},
	} {
		t.Run(test.name, func(t *testing.T) {
			timeout := test.timeout
			if timeout == 0 {
				timeout = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			err := probeMCPServer(ctx, mcpProbeSpec{
				Executable:    os.Args[0],
				Arguments:     []string{"-test.run=TestMCPProbeHelperProcess", "--", test.mode},
				RequiredTools: []string{"find_symbol"}, SemanticTool: "find_symbol",
				SemanticArgs:   json.RawMessage(`{"name":"ReviewerHealthMarker"}`),
				ExpectedResult: "ReviewerHealthMarker",
			})
			if test.want == "" && err != nil {
				t.Fatal(err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("probe error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestProbeMCPServerReportsCleanupFailure(t *testing.T) {
	t.Setenv("FACTORY_MCP_PROBE_HELPER", "1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := probeMCPServer(ctx, mcpProbeSpec{
		Executable: os.Args[0], Arguments: []string{"-test.run=TestMCPProbeHelperProcess", "--", "success"},
		RequiredTools: []string{"find_symbol"}, SemanticTool: "find_symbol",
		SemanticArgs: json.RawMessage(`{"name":"ReviewerHealthMarker"}`), ExpectedResult: "ReviewerHealthMarker",
		StopProcessGroup: func(pid int, identity string, grace time.Duration) error {
			_ = stopCapturedProcessGroup(pid, identity, grace)
			return errors.New("simulated cleanup failure")
		},
	})
	if err == nil || !strings.Contains(err.Error(), "clean up MCP server process group") ||
		!strings.Contains(err.Error(), "simulated cleanup failure") {
		t.Fatalf("cleanup failure error = %v", err)
	}
}

func TestMCPProbeHelperProcess(t *testing.T) {
	if os.Getenv("FACTORY_MCP_PROBE_HELPER") != "1" {
		return
	}
	mode := os.Args[len(os.Args)-1]
	if mode == "early-child-exit" {
		command := exec.Command("sleep", "60")
		if err := command.Start(); err != nil {
			os.Exit(2)
		}
		_ = os.WriteFile(os.Getenv("FACTORY_MCP_CHILD_PID_FILE"), []byte(fmt.Sprintf("%d", command.Process.Pid)), 0o600)
		time.Sleep(100 * time.Millisecond)
		return
	}
	if mode == "timeout" {
		select {}
	}
	decoder := json.NewDecoder(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for {
		var request struct {
			ID     json.RawMessage        `json:"id"`
			Method string                 `json:"method"`
			Params map[string]interface{} `json:"params"`
		}
		if err := decoder.Decode(&request); err != nil {
			return
		}
		switch request.Method {
		case "initialize":
			if mode == "partial" {
				fmt.Fprint(os.Stdout, `{`)
				return
			}
			if mode == "exit" {
				return
			}
			if mode == "notifications" {
				for index := 0; index < 100; index++ {
					_ = encoder.Encode(map[string]interface{}{
						"jsonrpc": "2.0", "method": "notifications/progress",
						"params": map[string]int{"index": index},
					})
				}
			}
			if mode == "polluted" {
				fmt.Fprintln(os.Stdout, "warn: host log leaked to stdout")
			}
			_ = encoder.Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": 1,
				"result": map[string]interface{}{"protocolVersion": "2024-11-05", "capabilities": map[string]interface{}{}},
			})
		case "tools/list":
			_ = encoder.Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": 2,
				"result": map[string]interface{}{"tools": []map[string]string{{"name": "find_symbol"}}},
			})
		case "tools/call":
			marker := "ReviewerHealthMarker"
			if mode == "missing-result" {
				marker = "different"
			}
			payload := map[string]interface{}{
				"Count": 1, "TotalFound": 1,
				"Symbols": []map[string]string{{"Name": marker, "Kind": "class"}},
			}
			if mode == "echo-not-found" || mode == "tool-error" {
				payload = map[string]interface{}{
					"Count": 0, "TotalFound": 0, "Symbols": []interface{}{},
					"Message": "ReviewerHealthMarker was not found",
				}
			}
			text, _ := json.Marshal(payload)
			content := []map[string]string{{"type": "text", "text": string(text)}}
			if mode == "malformed-result" {
				content[0]["text"] = "ReviewerHealthMarker"
			}
			if mode == "empty-result" {
				content = nil
			}
			_ = encoder.Encode(map[string]interface{}{
				"jsonrpc": "2.0", "id": request.ID,
				"result": map[string]interface{}{"content": content, "isError": mode == "tool-error"},
			})
		}
	}
}

func TestProbeMCPServerStopsItsProcessGroupOnTimeout(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is unavailable")
	}
	t.Setenv("FACTORY_MCP_PROBE_HELPER", "1")
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("FACTORY_MCP_CHILD_PID_FILE", pidFile)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	err := probeMCPServer(ctx, mcpProbeSpec{
		Executable: os.Args[0], Arguments: []string{"-test.run=TestMCPProbeChildProcess", "--"},
	})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("timeout error = %v", err)
	}
	body, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	var childPID int
	if _, err := fmt.Sscanf(string(body), "%d", &childPID); err != nil {
		t.Fatal(err)
	}
	assertProcessStopped(t, childPID)
}

func TestProbeMCPServerCleansDescendantAfterLeaderExit(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("sleep is unavailable")
	}
	t.Setenv("FACTORY_MCP_PROBE_HELPER", "1")
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	t.Setenv("FACTORY_MCP_CHILD_PID_FILE", pidFile)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := probeMCPServer(ctx, mcpProbeSpec{
		Executable: os.Args[0], Arguments: []string{"-test.run=TestMCPProbeHelperProcess", "--", "early-child-exit"},
	})
	if err == nil {
		t.Fatal("early leader exit unexpectedly passed MCP health")
	}
	body, readErr := os.ReadFile(pidFile)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var childPID int
	if _, scanErr := fmt.Sscanf(string(body), "%d", &childPID); scanErr != nil {
		t.Fatal(scanErr)
	}
	assertProcessStopped(t, childPID)
}

func TestMCPProbeChildProcess(t *testing.T) {
	if os.Getenv("FACTORY_MCP_PROBE_HELPER") != "1" {
		return
	}
	command := exec.Command("sleep", "60")
	if err := command.Start(); err != nil {
		os.Exit(2)
	}
	_ = os.WriteFile(os.Getenv("FACTORY_MCP_CHILD_PID_FILE"), []byte(fmt.Sprintf("%d", command.Process.Pid)), 0o600)
	select {}
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
reviewer_assets = ["reviewer-skills/code-review/SKILL.md"]

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
	skillDirectory := filepath.Join(directory, "reviewer-skills", "code-review")
	if err := os.MkdirAll(skillDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDirectory, "SKILL.md"), []byte("# Code review"), 0o600); err != nil {
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

func testSHA256(body []byte) string {
	digest := sha256.Sum256(body)
	return fmt.Sprintf("%x", digest[:])
}

func assertProcessStopped(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for processRunning(pid) && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	if processRunning(pid) {
		t.Fatalf("child process %d survived probe cleanup", pid)
	}
}

func processRunning(pid int) bool {
	output, err := exec.Command("ps", "-o", "stat=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		return false
	}
	state := strings.TrimSpace(string(output))
	return state != "" && !strings.HasPrefix(state, "Z")
}

func testZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var body bytes.Buffer
	archive := zip.NewWriter(&body)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return body.Bytes()
}
