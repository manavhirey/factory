package worker

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	pluginSchemaVersion  = 1
	maxPluginPromptBytes = 32 << 10
)

var (
	pluginIDPattern       = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)
	pluginVersionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)
	pluginCommitPattern   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	pluginCommandPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`)
	pluginAssetPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$`)
	pluginIdentityPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}@[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)
)

type Plugin struct {
	ID               string
	Version          string
	Description      string
	Runtime          string
	Prompt           string
	RequiredCommands []string
	InstructionSets  []string
	ContextFiles     []string
	Provenance       PluginProvenance
	Artifacts        []PluginArtifact
}

type PluginProvenance struct {
	Repository string `toml:"repository"`
	Commit     string `toml:"commit"`
	License    string `toml:"license"`
}

type PluginArtifact struct {
	Kind    string `toml:"kind"`
	Name    string `toml:"name"`
	Version string `toml:"version"`
	Source  string `toml:"source"`
}

type pluginManifest struct {
	SchemaVersion    int              `toml:"schema_version"`
	ID               string           `toml:"id"`
	Version          string           `toml:"version"`
	Description      string           `toml:"description"`
	Runtime          string           `toml:"runtime"`
	PromptFile       string           `toml:"prompt_file"`
	RequiredCommands []string         `toml:"required_commands"`
	InstructionSets  []string         `toml:"instruction_sets"`
	ContextFiles     []string         `toml:"context_files"`
	Provenance       PluginProvenance `toml:"provenance"`
	Artifacts        []PluginArtifact `toml:"artifacts"`
}

type commandLookup func(string) (string, error)

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
		plugin, loadErr := loadPlugin(root, pluginID, config.Runtime, lookup)
		if loadErr != nil {
			return nil, loadErr
		}
		plugins = append(plugins, plugin)
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].ID < plugins[j].ID })
	return plugins, nil
}

func loadPlugin(root, enabledID, runtime string, lookup commandLookup) (Plugin, error) {
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
	if err := regularNonSymlink(manifestPath, "plugin manifest"); err != nil {
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
	for _, contextFile := range manifest.ContextFiles {
		if _, err := resolvePluginFile(directory, contextFile); err != nil {
			return Plugin{}, fmt.Errorf("plugin %q context file %q: %w", enabledID, contextFile, err)
		}
	}
	plugin := Plugin{
		ID: enabledID, Version: manifest.Version, Description: manifest.Description,
		Runtime: manifest.Runtime, Prompt: prompt,
		RequiredCommands: append([]string(nil), manifest.RequiredCommands...),
		InstructionSets:  append([]string(nil), manifest.InstructionSets...),
		ContextFiles:     append([]string(nil), manifest.ContextFiles...),
		Provenance:       manifest.Provenance,
		Artifacts:        append([]PluginArtifact(nil), manifest.Artifacts...),
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
	if err := validateUniqueValues(manifest.ContextFiles, pluginAssetPattern, "context_files"); err != nil {
		return err
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
	for index, artifact := range manifest.Artifacts {
		if !pluginAssetPattern.MatchString(artifact.Kind) || !pluginAssetPattern.MatchString(artifact.Name) ||
			strings.TrimSpace(artifact.Version) == "" || len(artifact.Version) > 100 {
			return fmt.Errorf("artifact %d has invalid kind, name, or version", index+1)
		}
		if err := validateHTTPSURL(artifact.Source, fmt.Sprintf("artifact %d source", index+1)); err != nil {
			return err
		}
	}
	return nil
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
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", name, err)
	}
	return filepath.Clean(real), nil
}

func resolvePluginFile(directory, relative string) (string, error) {
	path := filepath.Join(directory, relative)
	if err := regularNonSymlink(path, "prompt_file"); err != nil {
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

func regularNonSymlink(path, name string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect %s: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s must be a regular non-symlink file", name)
	}
	return nil
}

func pathWithin(root, candidate string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
