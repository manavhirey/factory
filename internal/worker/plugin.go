package worker

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	pluginSchemaVersion  = 1
	maxPluginPromptBytes = 32 << 10
)

var (
	pluginIDPattern        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	pluginVersionPattern   = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)
	pluginCommitPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	pluginCommandPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	pluginAssetPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	pluginAgentNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	pluginIdentityPattern  = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}@[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)
	pluginSHA256Pattern    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Plugin struct {
	Root             string
	ID               string
	Version          string
	Description      string
	Runtime          string
	Prompt           string
	RequiredCommands []string
	InstructionSets  []string
	ContextFiles     []string
	ContextAssets    []PluginContextAsset
	ReviewerAssets   []PluginContextAsset
	HealthChecks     []PluginHealthCheck
	ArtifactRoot     string
	Provenance       PluginProvenance
	Artifacts        []PluginArtifact
}

type PluginContextAsset struct {
	RelativePath string
	SourcePath   string
}

type PluginHealthCheck struct {
	Kind           string
	ContextFile    string
	MCPServer      string
	RequiredTools  []string
	Command        string
	Arguments      []string
	Environment    map[string]string
	SemanticTool   string
	SemanticArgs   json.RawMessage
	ExpectedResult string
	WorkingDir     string
	StartupTimeout int
	Artifact       PluginArtifact
}

type PluginProvenance struct {
	Repository string `toml:"repository"`
	Commit     string `toml:"commit"`
	License    string `toml:"license"`
}

type PluginArtifact struct {
	Kind               string `toml:"kind"`
	Name               string `toml:"name"`
	Version            string `toml:"version"`
	Source             string `toml:"source"`
	PackageFile        string `toml:"package_file"`
	PackageSHA256      string `toml:"package_sha256"`
	ExtractedDirectory string `toml:"extracted_directory"`
	ExecutableFile     string `toml:"executable_file"`
	ExecutableSHA256   string `toml:"executable_sha256"`
}

type pluginManifest struct {
	SchemaVersion    int                         `toml:"schema_version"`
	ID               string                      `toml:"id"`
	Version          string                      `toml:"version"`
	Description      string                      `toml:"description"`
	Runtime          string                      `toml:"runtime"`
	PromptFile       string                      `toml:"prompt_file"`
	RequiredCommands []string                    `toml:"required_commands"`
	InstructionSets  []string                    `toml:"instruction_sets"`
	ReviewerAssets   []string                    `toml:"reviewer_assets"`
	ContextFiles     []string                    `toml:"context_files"`
	HealthChecks     []pluginHealthCheckManifest `toml:"health_checks"`
	Provenance       PluginProvenance            `toml:"provenance"`
	Artifacts        []PluginArtifact            `toml:"artifacts"`
}

type pluginHealthCheckManifest struct {
	Kind             string   `toml:"kind"`
	ContextFile      string   `toml:"context_file"`
	MCPServer        string   `toml:"mcp_server"`
	Artifact         string   `toml:"artifact"`
	WorkingDirectory string   `toml:"working_directory"`
	RequiredTools    []string `toml:"required_tools"`
	SemanticTool     string   `toml:"semantic_tool"`
	SemanticArgsJSON string   `toml:"semantic_args_json"`
	ExpectedResult   string   `toml:"expected_result_contains"`
}

type codexAgentConfig struct {
	Name                  string                    `toml:"name"`
	Description           string                    `toml:"description"`
	ModelReasoningEffort  string                    `toml:"model_reasoning_effort"`
	SandboxMode           string                    `toml:"sandbox_mode"`
	DeveloperInstructions string                    `toml:"developer_instructions"`
	MCPServers            map[string]codexMCPServer `toml:"mcp_servers"`
}

type codexMCPServer struct {
	Command           string            `toml:"command"`
	Args              []string          `toml:"args"`
	Env               map[string]string `toml:"env"`
	StartupTimeoutSec int               `toml:"startup_timeout_sec"`
	ToolTimeoutSec    int               `toml:"tool_timeout_sec"`
	Enabled           bool              `toml:"enabled"`
	Required          bool              `toml:"required"`
}

type commandLookup func(string) (string, error)
type mcpProbe func(context.Context, mcpProbeSpec) error

type mcpProbeSpec struct {
	Executable       string
	Arguments        []string
	Environment      map[string]string
	WorkingDir       string
	RequiredTools    []string
	SemanticTool     string
	SemanticArgs     json.RawMessage
	ExpectedResult   string
	StopProcessGroup func(int, string, time.Duration) error
}

func (plugin Plugin) Identity() string {
	return plugin.ID + "@" + plugin.Version
}

func pluginIdentities(plugins []Plugin) []string {
	identities := make([]string, 0, len(plugins))
	for _, plugin := range plugins {
		identities = append(identities, plugin.Identity())
	}
	return identities
}

func checkPluginDependencies(plugins []Plugin, lookup commandLookup) error {
	if lookup == nil {
		lookup = exec.LookPath
	}
	for _, plugin := range plugins {
		for _, command := range plugin.RequiredCommands {
			if _, err := lookup(command); err != nil {
				return fmt.Errorf("plugin %q required command %q is unavailable", plugin.ID, command)
			}
		}
	}
	return nil
}

func checkPluginActivation(ctx context.Context, plugins []Plugin, lookup commandLookup, probe mcpProbe, codexHome string) error {
	if err := checkPluginDependencies(plugins, lookup); err != nil {
		return err
	}
	if lookup == nil {
		lookup = exec.LookPath
	}
	if probe == nil {
		probe = probeMCPServer
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home for plugin context isolation: %w", err)
	}
	if codexHome == "" {
		codexHome = os.Getenv("CODEX_HOME")
		if codexHome == "" {
			codexHome = filepath.Join(userHome, ".codex")
		}
	}
	for _, plugin := range plugins {
		if len(plugin.ContextAssets)+len(plugin.ReviewerAssets) > 0 {
			if _, err := secureDirectory(codexHome, "Codex home"); err != nil {
				return fmt.Errorf("plugin %q: %w", plugin.ID, err)
			}
		}
		for _, artifact := range plugin.Artifacts {
			if err := verifyPluginArtifact(plugin, artifact); err != nil {
				return fmt.Errorf("plugin %q artifact %q: %w", plugin.ID, artifact.Name, err)
			}
		}
		for _, asset := range plugin.ContextAssets {
			installed := filepath.Join(codexHome, strings.TrimPrefix(asset.RelativePath, "codex/"))
			if err := secureRegularFile(codexHome, installed, "installed plugin context"); err != nil {
				return fmt.Errorf("plugin %q context %q is not installed: %w", plugin.ID, asset.RelativePath, err)
			}
			source, err := os.ReadFile(asset.SourcePath)
			if err != nil {
				return fmt.Errorf("read plugin %q context %q: %w", plugin.ID, asset.RelativePath, err)
			}
			body, err := os.ReadFile(installed)
			if err != nil {
				return fmt.Errorf("read installed plugin %q context %q: %w", plugin.ID, asset.RelativePath, err)
			}
			if !bytes.Equal(source, body) {
				return fmt.Errorf("plugin %q context %q does not match the reviewed plugin asset", plugin.ID, asset.RelativePath)
			}
		}
		if err := verifyInstalledReviewerAssetClosure(codexHome, plugin); err != nil {
			return fmt.Errorf("plugin %q reviewer asset closure: %w", plugin.ID, err)
		}
		for _, asset := range plugin.ReviewerAssets {
			installed := filepath.Join(codexHome, "reviewer-skills", plugin.ID,
				strings.TrimPrefix(asset.RelativePath, "reviewer-skills/"))
			if err := secureRegularFile(codexHome, installed, "installed reviewer instruction"); err != nil {
				return fmt.Errorf("plugin %q reviewer instruction %q is not installed: %w", plugin.ID, asset.RelativePath, err)
			}
			source, err := os.ReadFile(asset.SourcePath)
			if err != nil {
				return fmt.Errorf("read plugin %q reviewer instruction %q: %w", plugin.ID, asset.RelativePath, err)
			}
			body, err := os.ReadFile(installed)
			if err != nil {
				return fmt.Errorf("read installed plugin %q reviewer instruction %q: %w", plugin.ID, asset.RelativePath, err)
			}
			if !bytes.Equal(source, body) {
				return fmt.Errorf("plugin %q reviewer instruction %q does not match the reviewed plugin asset", plugin.ID, asset.RelativePath)
			}
		}
		for _, healthCheck := range plugin.HealthChecks {
			executable, err := lookup(healthCheck.Command)
			if err != nil {
				return fmt.Errorf("plugin %q health check command %q is unavailable", plugin.ID, healthCheck.Command)
			}
			probeContext, cancel := context.WithTimeout(ctx, time.Duration(healthCheck.StartupTimeout)*time.Second)
			err = probe(probeContext, mcpProbeSpec{
				Executable: executable, Arguments: healthCheck.Arguments, WorkingDir: healthCheck.WorkingDir,
				Environment:   healthCheck.Environment,
				RequiredTools: healthCheck.RequiredTools, SemanticTool: healthCheck.SemanticTool,
				SemanticArgs: healthCheck.SemanticArgs, ExpectedResult: healthCheck.ExpectedResult,
			})
			cancel()
			if err != nil {
				return fmt.Errorf("plugin %q MCP server %q failed its protocol health check: %w", plugin.ID, healthCheck.MCPServer, err)
			}
		}
	}
	return nil
}

func verifyInstalledReviewerAssetClosure(codexHome string, plugin Plugin) error {
	if len(plugin.ReviewerAssets) == 0 {
		return nil
	}
	root := filepath.Join(codexHome, "reviewer-skills", plugin.ID)
	if _, err := resolveSecureDirectory(codexHome, root, "installed reviewer asset root"); err != nil {
		return err
	}
	expected := make(map[string]bool, len(plugin.ReviewerAssets))
	for _, asset := range plugin.ReviewerAssets {
		expected[filepath.ToSlash(strings.TrimPrefix(asset.RelativePath, "reviewer-skills/"))] = true
	}
	actual := make(map[string]bool, len(expected))
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root || entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("installed reviewer asset %q is a symlink", path)
		}
		if err := secureRegularFile(root, path, "installed reviewer asset"); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		actual[filepath.ToSlash(relative)] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("installed tree has %d files, reviewed manifest has %d", len(actual), len(expected))
	}
	for path := range expected {
		if !actual[path] {
			return fmt.Errorf("reviewed asset %q is missing", path)
		}
	}
	return nil
}

func loadPlugins(config Config, lookup commandLookup) ([]Plugin, error) {
	if lookup == nil {
		lookup = exec.LookPath
	}
	if config.PluginDirectory == "" {
		return []Plugin{}, nil
	}
	root, err := realDirectory(config.PluginDirectory, "plugin_directory")
	if err != nil {
		return nil, err
	}
	if len(config.EnabledPlugins) == 0 {
		return []Plugin{}, nil
	}
	plugins := make([]Plugin, 0, len(config.EnabledPlugins))
	for _, pluginID := range config.EnabledPlugins {
		plugin, loadErr := loadPlugin(root, config.PluginArtifactDirectory, pluginID, config.Runtime, lookup)
		if loadErr != nil {
			return nil, loadErr
		}
		plugins = append(plugins, plugin)
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].ID < plugins[j].ID })
	return plugins, nil
}

func loadPlugin(root, artifactRoot, enabledID, runtime string, lookup commandLookup) (Plugin, error) {
	if !pluginIDPattern.MatchString(enabledID) {
		return Plugin{}, fmt.Errorf("enabled plugin %q is invalid", enabledID)
	}
	directory, err := realDirectory(filepath.Join(root, enabledID), "plugin "+enabledID)
	if err != nil {
		return Plugin{}, err
	}
	if !pathWithin(root, directory) {
		return Plugin{}, fmt.Errorf("plugin %q resolves outside plugin_directory", enabledID)
	}
	manifestPath := filepath.Join(directory, "plugin.toml")
	if err := secureRegularFile(directory, manifestPath, "plugin manifest"); err != nil {
		return Plugin{}, fmt.Errorf("plugin %q: %w", enabledID, err)
	}
	var manifest pluginManifest
	metadata, err := toml.DecodeFile(manifestPath, &manifest)
	if err != nil {
		return Plugin{}, fmt.Errorf("load plugin %q manifest: %w", enabledID, err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		sort.Strings(keys)
		return Plugin{}, fmt.Errorf("plugin %q manifest has unknown fields: %s", enabledID, strings.Join(keys, ", "))
	}
	if err := validatePluginManifest(manifest, enabledID, runtime); err != nil {
		return Plugin{}, fmt.Errorf("plugin %q: %w", enabledID, err)
	}
	promptPath, err := resolvePluginFile(directory, manifest.PromptFile)
	if err != nil {
		return Plugin{}, fmt.Errorf("plugin %q prompt: %w", enabledID, err)
	}
	body, err := os.ReadFile(promptPath)
	if err != nil {
		return Plugin{}, fmt.Errorf("read plugin %q prompt: %w", enabledID, err)
	}
	prompt := strings.TrimSpace(string(body))
	if prompt == "" || len(body) > maxPluginPromptBytes {
		return Plugin{}, fmt.Errorf("plugin prompt must be non-empty and at most %d bytes", maxPluginPromptBytes)
	}
	contextAssets := make([]PluginContextAsset, 0, len(manifest.ContextFiles))
	contextPaths := make(map[string]string, len(manifest.ContextFiles))
	for _, contextFile := range manifest.ContextFiles {
		path, err := resolvePluginFile(directory, contextFile)
		if err != nil {
			return Plugin{}, fmt.Errorf("plugin %q context file %q: %w", enabledID, contextFile, err)
		}
		contextPaths[contextFile] = path
		contextAssets = append(contextAssets, PluginContextAsset{RelativePath: contextFile, SourcePath: path})
	}
	reviewerAssets := make([]PluginContextAsset, 0, len(manifest.ReviewerAssets))
	for _, relative := range manifest.ReviewerAssets {
		path, err := resolvePluginFile(directory, relative)
		if err != nil {
			return Plugin{}, fmt.Errorf("plugin %q reviewer asset %q: %w", enabledID, relative, err)
		}
		reviewerAssets = append(reviewerAssets, PluginContextAsset{RelativePath: relative, SourcePath: path})
	}
	if len(manifest.Artifacts) > 0 && artifactRoot == "" {
		return Plugin{}, fmt.Errorf("plugin %q requires plugin_artifact_directory", enabledID)
	}
	if artifactRoot != "" && !filepath.IsAbs(artifactRoot) {
		return Plugin{}, errors.New("plugin_artifact_directory must be an absolute path")
	}
	healthChecks := make([]PluginHealthCheck, 0, len(manifest.HealthChecks))
	for _, healthCheck := range manifest.HealthChecks {
		contextPath := contextPaths[healthCheck.ContextFile]
		server, err := loadCodexMCPServer(contextPath, healthCheck.MCPServer)
		if err != nil {
			return Plugin{}, fmt.Errorf("plugin %q health check for %q: %w", enabledID, healthCheck.MCPServer, err)
		}
		if !contains(manifest.RequiredCommands, server.Command) {
			return Plugin{}, fmt.Errorf("plugin %q health check command %q must be declared in required_commands", enabledID, server.Command)
		}
		artifact, found := findPluginArtifact(manifest.Artifacts, healthCheck.Artifact)
		if !found || artifact.Kind != "nuget-tool" {
			return Plugin{}, fmt.Errorf("plugin %q health check artifact %q must identify a nuget-tool", enabledID, healthCheck.Artifact)
		}
		executablePath := filepath.Join(artifactRoot, enabledID, filepath.FromSlash(artifact.ExecutableFile))
		if err := validatePinnedArtifactServer(server, executablePath); err != nil {
			return Plugin{}, fmt.Errorf("plugin %q MCP server: %w", enabledID, err)
		}
		workingDirectory, err := resolvePluginDirectory(directory, healthCheck.WorkingDirectory)
		if err != nil {
			return Plugin{}, fmt.Errorf("plugin %q health check working directory: %w", enabledID, err)
		}
		healthChecks = append(healthChecks, PluginHealthCheck{
			Kind: healthCheck.Kind, ContextFile: healthCheck.ContextFile,
			MCPServer: healthCheck.MCPServer, RequiredTools: append([]string(nil), healthCheck.RequiredTools...),
			Command: server.Command, Arguments: append([]string(nil), server.Args...), Environment: cloneStringMap(server.Env),
			SemanticTool: healthCheck.SemanticTool, SemanticArgs: json.RawMessage(healthCheck.SemanticArgsJSON),
			ExpectedResult: healthCheck.ExpectedResult, WorkingDir: workingDirectory, StartupTimeout: server.StartupTimeoutSec,
			Artifact: artifact,
		})
	}
	plugin := Plugin{
		Root: directory, ID: enabledID, Version: manifest.Version, Description: manifest.Description,
		Runtime: manifest.Runtime, Prompt: prompt,
		RequiredCommands: append([]string(nil), manifest.RequiredCommands...),
		InstructionSets:  append([]string(nil), manifest.InstructionSets...),
		ContextFiles:     append([]string(nil), manifest.ContextFiles...),
		ContextAssets:    contextAssets,
		ReviewerAssets:   reviewerAssets, ArtifactRoot: artifactRoot,
		HealthChecks: healthChecks,
		Provenance:   manifest.Provenance,
		Artifacts:    append([]PluginArtifact(nil), manifest.Artifacts...),
	}
	if err := checkPluginDependencies([]Plugin{plugin}, lookup); err != nil {
		return Plugin{}, err
	}
	return plugin, nil
}

func validatePluginManifest(manifest pluginManifest, enabledID, runtime string) error {
	if manifest.SchemaVersion != pluginSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", manifest.SchemaVersion)
	}
	if manifest.ID != enabledID || !pluginIDPattern.MatchString(manifest.ID) {
		return errors.New("manifest id must match the enabled plugin directory")
	}
	if !pluginVersionPattern.MatchString(manifest.Version) {
		return errors.New("version must be a semantic version")
	}
	if strings.TrimSpace(manifest.Description) == "" || len(manifest.Description) > 500 {
		return errors.New("description is required and limited to 500 bytes")
	}
	if manifest.Runtime != runtime {
		return fmt.Errorf("runtime %q does not match worker runtime %q", manifest.Runtime, runtime)
	}
	if manifest.PromptFile == "" || filepath.IsAbs(manifest.PromptFile) || !filepath.IsLocal(manifest.PromptFile) {
		return errors.New("prompt_file must be a local relative path")
	}
	if err := validateUniqueValues(manifest.RequiredCommands, pluginCommandPattern, "required_commands"); err != nil {
		return err
	}
	if err := validateUniqueValues(manifest.InstructionSets, pluginAssetPattern, "instruction_sets"); err != nil {
		return err
	}
	if err := validateUniqueValues(manifest.ReviewerAssets, pluginAssetPattern, "reviewer_assets"); err != nil {
		return err
	}
	reviewerAssets := make(map[string]bool, len(manifest.ReviewerAssets))
	for _, asset := range manifest.ReviewerAssets {
		if !strings.HasPrefix(asset, "reviewer-skills/") {
			return errors.New("reviewer_assets must be below reviewer-skills")
		}
		reviewerAssets[asset] = true
	}
	for _, instructionSet := range manifest.InstructionSets {
		required := filepath.ToSlash(filepath.Join("reviewer-skills", instructionSet, "SKILL.md"))
		if !reviewerAssets[required] {
			return fmt.Errorf("instruction set %q is missing %q from reviewer_assets", instructionSet, required)
		}
	}
	if err := validateUniqueValues(manifest.ContextFiles, pluginAssetPattern, "context_files"); err != nil {
		return err
	}
	contextFiles := make(map[string]bool, len(manifest.ContextFiles))
	for _, contextFile := range manifest.ContextFiles {
		if !strings.HasPrefix(contextFile, "codex/agents/") || filepath.Ext(contextFile) != ".toml" {
			return errors.New("context_files must identify Codex agent TOML files below codex/agents")
		}
		contextFiles[contextFile] = true
	}
	seenHealthChecks := make(map[string]bool, len(manifest.HealthChecks))
	for index, healthCheck := range manifest.HealthChecks {
		if healthCheck.Kind != "roslyn-navigator-mcp" {
			return fmt.Errorf("health check %d kind must be roslyn-navigator-mcp", index+1)
		}
		if !contextFiles[healthCheck.ContextFile] {
			return fmt.Errorf("health check %d context_file must be declared in context_files", index+1)
		}
		if !pluginAgentNamePattern.MatchString(healthCheck.MCPServer) {
			return fmt.Errorf("health check %d mcp_server is invalid", index+1)
		}
		if err := validateUniqueValues(healthCheck.RequiredTools, pluginAssetPattern, fmt.Sprintf("health check %d required_tools", index+1)); err != nil {
			return err
		}
		if len(healthCheck.RequiredTools) == 0 {
			return fmt.Errorf("health check %d required_tools must not be empty", index+1)
		}
		if !pluginAssetPattern.MatchString(healthCheck.Artifact) {
			return fmt.Errorf("health check %d artifact is invalid", index+1)
		}
		if healthCheck.WorkingDirectory == "" || filepath.IsAbs(healthCheck.WorkingDirectory) || !filepath.IsLocal(healthCheck.WorkingDirectory) {
			return fmt.Errorf("health check %d working_directory must be a local relative path", index+1)
		}
		if !pluginAssetPattern.MatchString(healthCheck.SemanticTool) ||
			!contains(healthCheck.RequiredTools, healthCheck.SemanticTool) {
			return fmt.Errorf("health check %d semantic_tool must be declared in required_tools", index+1)
		}
		var semanticArguments map[string]any
		if len(healthCheck.SemanticArgsJSON) > 4096 ||
			json.Unmarshal([]byte(healthCheck.SemanticArgsJSON), &semanticArguments) != nil || semanticArguments == nil {
			return fmt.Errorf("health check %d semantic_args_json must be a JSON object of at most 4096 bytes", index+1)
		}
		if strings.TrimSpace(healthCheck.ExpectedResult) == "" || len(healthCheck.ExpectedResult) > 500 {
			return fmt.Errorf("health check %d expected_result_contains is required and limited to 500 bytes", index+1)
		}
		identity := healthCheck.ContextFile + "\x00" + healthCheck.MCPServer
		if seenHealthChecks[identity] {
			return fmt.Errorf("health check %d duplicates context_file and mcp_server", index+1)
		}
		seenHealthChecks[identity] = true
	}
	if err := validateHTTPSURL(manifest.Provenance.Repository, "provenance repository"); err != nil {
		return err
	}
	if !pluginCommitPattern.MatchString(manifest.Provenance.Commit) {
		return errors.New("provenance commit must be an exact lowercase 40-character Git SHA")
	}
	if strings.TrimSpace(manifest.Provenance.License) == "" || len(manifest.Provenance.License) > 100 {
		return errors.New("provenance license is required and limited to 100 bytes")
	}
	seenArtifacts := make(map[string]bool, len(manifest.Artifacts))
	for index, artifact := range manifest.Artifacts {
		if !pluginAssetPattern.MatchString(artifact.Kind) || !pluginAssetPattern.MatchString(artifact.Name) ||
			strings.TrimSpace(artifact.Version) == "" || len(artifact.Version) > 100 {
			return fmt.Errorf("artifact %d has invalid kind, name, or version", index+1)
		}
		if err := validateHTTPSURL(artifact.Source, fmt.Sprintf("artifact %d source", index+1)); err != nil {
			return err
		}
		if artifact.Kind != "nuget-tool" {
			return fmt.Errorf("artifact %d kind must be nuget-tool", index+1)
		}
		if seenArtifacts[artifact.Name] {
			return fmt.Errorf("artifact %d name is duplicated", index+1)
		}
		seenArtifacts[artifact.Name] = true
		if !pluginAssetPattern.MatchString(artifact.PackageFile) || !pluginAssetPattern.MatchString(artifact.ExtractedDirectory) ||
			!pluginAssetPattern.MatchString(artifact.ExecutableFile) || !filepath.IsLocal(artifact.PackageFile) ||
			!filepath.IsLocal(artifact.ExtractedDirectory) || !filepath.IsLocal(artifact.ExecutableFile) {
			return fmt.Errorf("artifact %d package and executable paths must be local relative paths", index+1)
		}
		extracted := filepath.Clean(filepath.FromSlash(artifact.ExtractedDirectory))
		executable := filepath.Clean(filepath.FromSlash(artifact.ExecutableFile))
		if !pathWithin(extracted, executable) {
			return fmt.Errorf("artifact %d executable_file must be below extracted_directory", index+1)
		}
		if !pluginSHA256Pattern.MatchString(artifact.PackageSHA256) || !pluginSHA256Pattern.MatchString(artifact.ExecutableSHA256) {
			return fmt.Errorf("artifact %d SHA-256 values must be exact lowercase digests", index+1)
		}
	}
	for index, healthCheck := range manifest.HealthChecks {
		if !seenArtifacts[healthCheck.Artifact] {
			return fmt.Errorf("health check %d references an undeclared artifact", index+1)
		}
	}
	return nil
}

func loadCodexMCPServer(path, serverName string) (codexMCPServer, error) {
	var agent codexAgentConfig
	metadata, err := toml.DecodeFile(path, &agent)
	if err != nil {
		return codexMCPServer{}, fmt.Errorf("parse Codex agent context: %w", err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) != 0 {
		keys := make([]string, 0, len(undecoded))
		for _, key := range undecoded {
			keys = append(keys, key.String())
		}
		sort.Strings(keys)
		return codexMCPServer{}, fmt.Errorf("Codex agent context has unknown fields: %s", strings.Join(keys, ", "))
	}
	if !pluginAgentNamePattern.MatchString(agent.Name) || strings.TrimSpace(agent.Description) == "" ||
		strings.TrimSpace(agent.DeveloperInstructions) == "" {
		return codexMCPServer{}, errors.New("Codex agent context requires a valid name, description, and developer_instructions")
	}
	if agent.ModelReasoningEffort != "low" && agent.ModelReasoningEffort != "medium" &&
		agent.ModelReasoningEffort != "high" && agent.ModelReasoningEffort != "xhigh" {
		return codexMCPServer{}, errors.New("Codex agent context has an unsupported model_reasoning_effort")
	}
	if agent.SandboxMode != "read-only" && agent.SandboxMode != "workspace-write" && agent.SandboxMode != "danger-full-access" {
		return codexMCPServer{}, errors.New("Codex agent context has an unsupported sandbox_mode")
	}
	server, found := agent.MCPServers[serverName]
	if !found {
		return codexMCPServer{}, fmt.Errorf("Codex agent context does not register MCP server %q", serverName)
	}
	if !pluginCommandPattern.MatchString(server.Command) || !server.Enabled || !server.Required {
		return codexMCPServer{}, errors.New("MCP server must use a command basename and be enabled and required")
	}
	if server.StartupTimeoutSec < 1 || server.StartupTimeoutSec > 60 {
		return codexMCPServer{}, errors.New("MCP server startup_timeout_sec must be between 1 and 60")
	}
	if server.ToolTimeoutSec < 1 || server.ToolTimeoutSec > 600 {
		return codexMCPServer{}, errors.New("MCP server tool_timeout_sec must be between 1 and 600")
	}
	for _, argument := range server.Args {
		if argument == "" || len(argument) > 1000 || strings.ContainsRune(argument, '\x00') {
			return codexMCPServer{}, errors.New("MCP server args contain an invalid entry")
		}
	}
	for key, value := range server.Env {
		if strings.TrimSpace(key) == "" || strings.Contains(key, "=") || strings.ContainsRune(value, '\x00') {
			return codexMCPServer{}, errors.New("MCP server env contains an invalid entry")
		}
	}
	return server, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func findPluginArtifact(artifacts []PluginArtifact, name string) (PluginArtifact, bool) {
	for _, artifact := range artifacts {
		if artifact.Name == name {
			return artifact, true
		}
	}
	return PluginArtifact{}, false
}

func validatePinnedArtifactServer(server codexMCPServer, executablePath string) error {
	if server.Command != "dotnet" {
		return errors.New("artifact health requires the dotnet runner; shells and other interpreters are forbidden")
	}
	if len(server.Args) != 1 || server.Args[0] != executablePath {
		return fmt.Errorf("args must contain only the pinned artifact executable %q", executablePath)
	}
	return nil
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func verifyPluginArtifact(plugin Plugin, artifact PluginArtifact) error {
	root, err := secureDirectory(plugin.ArtifactRoot, "plugin artifact directory")
	if err != nil {
		return err
	}
	pluginRoot, err := resolveSecureDirectory(root, filepath.Join(root, plugin.ID), "plugin artifact root")
	if err != nil {
		return err
	}
	packagePath := filepath.Join(pluginRoot, filepath.FromSlash(artifact.PackageFile))
	if err := verifyFileSHA256(pluginRoot, packagePath, artifact.PackageSHA256, "retained NuGet package"); err != nil {
		return err
	}
	extractedRoot, err := resolveSecureDirectory(pluginRoot,
		filepath.Join(pluginRoot, filepath.FromSlash(artifact.ExtractedDirectory)), "extracted NuGet package")
	if err != nil {
		return err
	}
	if err := verifyExtractedNuGetPackage(packagePath, extractedRoot); err != nil {
		return err
	}
	executablePath := filepath.Join(pluginRoot, filepath.FromSlash(artifact.ExecutableFile))
	if err := verifyFileSHA256(pluginRoot, executablePath, artifact.ExecutableSHA256, "artifact executable"); err != nil {
		return err
	}
	return nil
}

func verifyExtractedNuGetPackage(packagePath, extractedRoot string) error {
	archive, err := zip.OpenReader(packagePath)
	if err != nil {
		return fmt.Errorf("open retained NuGet package: %w", err)
	}
	defer archive.Close()
	expected := make(map[string]bool, len(archive.File))
	for _, entry := range archive.File {
		relative := filepath.Clean(filepath.FromSlash(entry.Name))
		if entry.FileInfo().IsDir() {
			continue
		}
		if !filepath.IsLocal(relative) || entry.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("NuGet package contains unsafe entry %q", entry.Name)
		}
		expected[filepath.ToSlash(relative)] = true
		extractedPath := filepath.Join(extractedRoot, relative)
		if err := secureRegularFile(extractedRoot, extractedPath, "extracted NuGet entry"); err != nil {
			return err
		}
		archiveFile, err := entry.Open()
		if err != nil {
			return fmt.Errorf("open NuGet entry %q: %w", entry.Name, err)
		}
		archiveHash := sha256.New()
		_, copyErr := io.Copy(archiveHash, archiveFile)
		closeErr := archiveFile.Close()
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf("hash NuGet entry %q", entry.Name)
		}
		extractedHash, err := fileSHA256(extractedRoot, extractedPath, "extracted NuGet entry")
		if err != nil {
			return err
		}
		if !bytes.Equal(archiveHash.Sum(nil), extractedHash) {
			return fmt.Errorf("extracted NuGet entry %q does not match retained package", entry.Name)
		}
	}
	actual := make(map[string]bool, len(expected))
	err = filepath.WalkDir(extractedRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == extractedRoot {
			return nil
		}
		if entry.IsDir() {
			_, err := resolveSecureDirectory(extractedRoot, path, "extracted NuGet directory")
			return err
		}
		if err := secureRegularFile(extractedRoot, path, "extracted NuGet entry"); err != nil {
			return err
		}
		relative, err := filepath.Rel(extractedRoot, path)
		if err != nil {
			return err
		}
		actual[filepath.ToSlash(relative)] = true
		return nil
	})
	if err != nil {
		return err
	}
	if len(actual) != len(expected) {
		return fmt.Errorf("extracted NuGet tree has %d files, retained package has %d", len(actual), len(expected))
	}
	for path := range expected {
		if !actual[path] {
			return fmt.Errorf("extracted NuGet entry %q is missing", path)
		}
	}
	return nil
}

func fileSHA256(root, path, name string) ([]byte, error) {
	if err := secureRegularFile(root, path, name); err != nil {
		return nil, err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s before hashing: %w", name, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", name, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect open %s: %w", name, err)
	}
	if !os.SameFile(before, opened) {
		return nil, fmt.Errorf("%s changed while it was opened", name)
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return nil, fmt.Errorf("hash %s: %w", name, err)
	}
	return digest.Sum(nil), nil
}

func verifyFileSHA256(root, path, expected, name string) error {
	digest, err := fileSHA256(root, path, name)
	if err != nil {
		return err
	}
	actual := fmt.Sprintf("%x", digest)
	if actual != expected {
		return fmt.Errorf("SHA-256 is %s, want %s", actual, expected)
	}
	return nil
}

func probeMCPServer(ctx context.Context, spec mcpProbeSpec) (returnErr error) {
	command := exec.Command(spec.Executable, spec.Arguments...)
	command.Dir = spec.WorkingDir
	command.Env = append(os.Environ(), sortedEnvironment(spec.Environment)...)
	configureNewProcessGroup(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return fmt.Errorf("open stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open stdout: %w", err)
	}
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return fmt.Errorf("start server: %w", err)
	}
	pid := command.Process.Pid
	identity, identityErr := processIdentity(pid)
	if identityErr != nil {
		_ = forceStopStartedProcessGroup(pid)
		_ = command.Wait()
		return fmt.Errorf("capture server process identity: %w", identityErr)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stopProcessGroup := spec.StopProcessGroup
	if stopProcessGroup == nil {
		stopProcessGroup = stopCapturedProcessGroup
	}
	defer func() {
		_ = stdin.Close()
		cleanupErr := stopProcessGroup(pid, identity, time.Second)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			cleanupErr = errors.Join(cleanupErr, errors.New("server process did not exit after cleanup"))
		}
		if cleanupErr != nil {
			cleanupErr = fmt.Errorf("clean up MCP server process group: %w", cleanupErr)
			if returnErr == nil {
				returnErr = cleanupErr
			} else {
				returnErr = errors.Join(returnErr, cleanupErr)
			}
		}
	}()

	encoder := json.NewEncoder(stdin)
	responses := make(chan mcpProbeResponse, 1)
	decodeErrors := make(chan error, 1)
	go func() {
		decoder := json.NewDecoder(bufio.NewReader(stdout))
		for {
			var response mcpProbeResponse
			if err := decoder.Decode(&response); err != nil {
				decodeErrors <- err
				return
			}
			responses <- response
		}
	}()

	initialize := map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]string{"name": "factory-plugin-health", "version": "1"},
		},
	}
	if err := encoder.Encode(initialize); err != nil {
		return fmt.Errorf("send initialize: %w", err)
	}
	response, err := awaitMCPResponse(ctx, responses, decodeErrors, 1)
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if response.Error != nil || len(response.Result) == 0 {
		return errors.New("initialize returned an error or empty result")
	}
	if err := encoder.Encode(map[string]any{
		"jsonrpc": "2.0", "method": "notifications/initialized", "params": map[string]any{},
	}); err != nil {
		return fmt.Errorf("send initialized notification: %w", err)
	}
	if err := encoder.Encode(map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": map[string]any{},
	}); err != nil {
		return fmt.Errorf("send tools/list: %w", err)
	}
	response, err = awaitMCPResponse(ctx, responses, decodeErrors, 2)
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	if response.Error != nil {
		return errors.New("tools/list returned an error")
	}
	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(response.Result, &result); err != nil {
		return fmt.Errorf("decode tools/list result: %w", err)
	}
	available := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		available[tool.Name] = true
	}
	for _, tool := range spec.RequiredTools {
		if !available[tool] {
			return fmt.Errorf("required tool %q was not registered", tool)
		}
	}
	var semanticArguments map[string]any
	if err := json.Unmarshal(spec.SemanticArgs, &semanticArguments); err != nil {
		return fmt.Errorf("decode semantic probe arguments: %w", err)
	}
	for requestID := 3; ; requestID++ {
		if err := encoder.Encode(map[string]any{
			"jsonrpc": "2.0", "id": requestID, "method": "tools/call",
			"params": map[string]any{"name": spec.SemanticTool, "arguments": semanticArguments},
		}); err != nil {
			return fmt.Errorf("send semantic tools/call: %w", err)
		}
		response, err = awaitMCPResponse(ctx, responses, decodeErrors, requestID)
		if err != nil {
			return fmt.Errorf("semantic tools/call did not become ready: %w", err)
		}
		if response.Error != nil {
			return errors.New("semantic tools/call returned a JSON-RPC error")
		}
		ready, err := semanticCallSucceeded(response.Result, spec.ExpectedResult)
		if err != nil {
			return fmt.Errorf("semantic tools/call result: %w", err)
		}
		if ready {
			return nil
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("semantic tool %q did not return the expected marker before startup timeout", spec.SemanticTool)
		case <-timer.C:
		}
	}
}

func stopCapturedProcessGroup(pid int, identity string, grace time.Duration) error {
	actual, identityErr := processIdentity(pid)
	if identityErr == nil {
		if actual != identity {
			return fmt.Errorf("refuse to signal process group after leader identity changed")
		}
		return stopOwnedProcessGroup(pid, identity, grace)
	}
	if processAlive(pid) {
		return fmt.Errorf("refuse to signal process group while leader identity is unverifiable: %w", identityErr)
	}
	if !processGroupAlive(pid) {
		return nil
	}
	if err := stopLeaderlessProcessGroup(pid, grace); err != nil {
		return fmt.Errorf("terminate captured process group %d after leader exit: %w", pid, err)
	}
	return nil
}

func semanticCallSucceeded(raw json.RawMessage, expectedSymbol string) (bool, error) {
	var callResult struct {
		IsError bool `json:"isError"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &callResult); err != nil {
		return false, fmt.Errorf("decode CallToolResult: %w", err)
	}
	if callResult.IsError {
		return false, errors.New("CallToolResult reported isError=true")
	}
	textCount := 0
	for _, content := range callResult.Content {
		if content.Type != "text" || strings.TrimSpace(content.Text) == "" {
			continue
		}
		textCount++
		var payload struct {
			Count      int `json:"Count"`
			TotalFound int `json:"TotalFound"`
			Symbols    []struct {
				Name string `json:"Name"`
			} `json:"Symbols"`
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(content.Text), &object); err != nil || object == nil {
			return false, errors.New("text content is not a structured Roslyn result object")
		}
		if err := json.Unmarshal([]byte(content.Text), &payload); err != nil {
			return false, errors.New("decode structured Roslyn result")
		}
		if payload.Count < 1 || payload.TotalFound < 1 {
			continue
		}
		for _, symbol := range payload.Symbols {
			if symbol.Name == expectedSymbol {
				return true, nil
			}
		}
	}
	if textCount == 0 {
		return false, errors.New("CallToolResult has no non-empty text content")
	}
	return false, nil
}

type mcpProbeResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   json.RawMessage `json:"error"`
}

func awaitMCPResponse(ctx context.Context, responses <-chan mcpProbeResponse, decodeErrors <-chan error, wantedID int) (mcpProbeResponse, error) {
	wanted := fmt.Sprintf("%d", wantedID)
	for {
		select {
		case <-ctx.Done():
			return mcpProbeResponse{}, ctx.Err()
		case err := <-decodeErrors:
			return mcpProbeResponse{}, fmt.Errorf("stdout was not valid MCP JSON: %w", err)
		case response := <-responses:
			if response.JSONRPC != "2.0" {
				return mcpProbeResponse{}, errors.New("response has an invalid jsonrpc version")
			}
			if string(response.ID) == wanted {
				return response, nil
			}
		}
	}
}

func validateUniqueValues(values []string, pattern *regexp.Regexp, name string) error {
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if !pattern.MatchString(value) {
			return fmt.Errorf("%s value %q is invalid", name, value)
		}
		if seen[value] {
			return fmt.Errorf("%s value %q is duplicated", name, value)
		}
		seen[value] = true
	}
	return nil
}

func validateHTTPSURL(value, name string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("%s must be an HTTPS URL without credentials or fragment", name)
	}
	return nil
}

func realDirectory(path, name string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be an absolute path", name)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", name, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s must be a real directory, not a symlink", name)
	}
	if err := validatePathOwner(info, name); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	return filepath.Clean(real), nil
}

func resolvePluginFile(directory, relative string) (string, error) {
	path := filepath.Join(directory, relative)
	if err := secureRegularFile(directory, path, "plugin file"); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve prompt_file: %w", err)
	}
	if !pathWithin(directory, real) {
		return "", errors.New("prompt_file resolves outside the plugin directory")
	}
	return real, nil
}

func resolvePluginDirectory(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) || !filepath.IsLocal(relative) {
		return "", errors.New("directory must be a local relative path")
	}
	return resolveSecureDirectory(root, filepath.Join(root, relative), "plugin directory")
}

func secureDirectory(path, name string) (string, error) {
	return realDirectory(path, name)
}

func resolveSecureDirectory(root, path, name string) (string, error) {
	if err := securePathComponents(root, path, true, name); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	if !pathWithin(root, real) {
		return "", fmt.Errorf("%s resolves outside its trusted root", name)
	}
	return filepath.Clean(real), nil
}

func secureRegularFile(root, path, name string) error {
	return securePathComponents(root, path, false, name)
}

func securePathComponents(root, path string, finalDirectory bool, name string) error {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	if !filepath.IsAbs(root) || !pathWithin(root, path) {
		return fmt.Errorf("%s must remain within its trusted root", name)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return fmt.Errorf("resolve %s relative path: %w", name, err)
	}
	components := strings.Split(relative, string(filepath.Separator))
	current := root
	for index, component := range components {
		if component == "." || component == "" {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s contains symlink component %q", name, current)
		}
		last := index == len(components)-1
		if last && !finalDirectory {
			if !info.Mode().IsRegular() {
				return fmt.Errorf("%s must be a regular non-symlink file", name)
			}
		} else if !info.IsDir() {
			return fmt.Errorf("%s component %q must be a directory", name, current)
		}
		if err := validatePathOwner(info, name); err != nil {
			return err
		}
	}
	return nil
}

func sortedEnvironment(environment map[string]string) []string {
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+environment[key])
	}
	return values
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
