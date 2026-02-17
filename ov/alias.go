package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// aliasNameRe matches valid alias names: starts with alphanumeric, allows dots/underscores/hyphens
var aliasNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

const aliasMarker = "# ov-alias"

// generateAliasScript produces the wrapper script content for a host command alias.
// The wrapper builds a properly quoted command string and calls ov shell -c.
func generateAliasScript(image, command string) string {
	return fmt.Sprintf(`#!/bin/sh
# ov-alias
# image: %s
# command: %s
_ov_q(){ printf "'"; printf '%%s' "$1" | sed "s/'/'\\\\''/g"; printf "' "; }
c="%s"; for a in "$@"; do c="$c $(_ov_q "$a")"; done
exec ov shell %s -c "$c"
`, image, command, command, image)
}

// writeAliasScript writes a wrapper script to dir/name with mode 0755.
func writeAliasScript(dir, name, image, command string) error {
	path := filepath.Join(dir, name)
	content := generateAliasScript(image, command)
	if err := os.WriteFile(path, []byte(content), 0755); err != nil {
		return fmt.Errorf("writing alias script %s: %w", path, err)
	}
	return nil
}

// removeAliasScript verifies the file has the ov-alias marker, then deletes it.
func removeAliasScript(dir, name string) error {
	path := filepath.Join(dir, name)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("alias %q not found in %s", name, dir)
		}
		return err
	}

	if !strings.Contains(string(data), aliasMarker) {
		return fmt.Errorf("%s is not an ov alias (missing marker)", path)
	}

	return os.Remove(path)
}

// AliasInfo holds parsed metadata from a wrapper script.
type AliasInfo struct {
	Name    string
	Image   string
	Command string
}

// listAliasScripts scans dir for files with the ov-alias marker and returns their metadata.
func listAliasScripts(dir string) ([]AliasInfo, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var aliases []AliasInfo
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		path := filepath.Join(dir, entry.Name())
		info, err := parseAliasScript(path)
		if err != nil || info == nil {
			continue
		}
		info.Name = entry.Name()
		aliases = append(aliases, *info)
	}

	return aliases, nil
}

// parseAliasScript reads a file and extracts alias metadata if it has the marker.
func parseAliasScript(path string) (*AliasInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	var hasMarker bool
	var image, command string

	for scanner.Scan() {
		line := scanner.Text()
		if line == aliasMarker {
			hasMarker = true
		}
		if strings.HasPrefix(line, "# image: ") {
			image = strings.TrimPrefix(line, "# image: ")
		}
		if strings.HasPrefix(line, "# command: ") {
			command = strings.TrimPrefix(line, "# command: ")
		}
	}

	if !hasMarker {
		return nil, nil
	}

	return &AliasInfo{Image: image, Command: command}, nil
}

// CollectedAlias represents a resolved alias ready for installation.
type CollectedAlias struct {
	Name    string
	Command string
}

// CollectImageAliases gathers aliases from the image's own layers + image-level config.
// No base chain traversal — aliases are leaf-image specific.
// Layer aliases come first; image-level overrides by name.
func CollectImageAliases(cfg *Config, layers map[string]*Layer, imageName string) ([]CollectedAlias, error) {
	img, ok := cfg.Images[imageName]
	if !ok {
		return nil, fmt.Errorf("image %q not found in images.yml", imageName)
	}

	// Resolve layers for this image (includes transitive deps)
	resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var result []CollectedAlias

	// Collect from layers
	for _, layerName := range resolved {
		layer, ok := layers[layerName]
		if !ok || !layer.HasAliases {
			continue
		}
		for _, a := range layer.Aliases() {
			if seen[a.Name] {
				continue
			}
			seen[a.Name] = true
			result = append(result, CollectedAlias{Name: a.Name, Command: a.Command})
		}
	}

	// Collect from image config (overrides layer aliases with same name)
	for _, a := range img.Aliases {
		cmd := a.Command
		if cmd == "" {
			cmd = a.Name
		}
		if seen[a.Name] {
			// Override: find and replace
			for i := range result {
				if result[i].Name == a.Name {
					result[i].Command = cmd
					break
				}
			}
		} else {
			seen[a.Name] = true
			result = append(result, CollectedAlias{Name: a.Name, Command: cmd})
		}
	}

	return result, nil
}

// defaultAliasDir returns ~/.local/bin, creating it if needed.
func defaultAliasDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.Getenv("HOME"), ".local", "bin")
	}
	return filepath.Join(home, ".local", "bin")
}

// --- CLI Commands ---

// AliasCmd groups alias subcommands
type AliasCmd struct {
	Add       AliasAddCmd       `cmd:"" help:"Create a host command alias"`
	Remove    AliasRemoveCmd    `cmd:"" help:"Remove an alias"`
	List      AliasListCmd      `cmd:"" help:"List all installed aliases"`
	Install   AliasInstallCmd   `cmd:"" help:"Install default aliases from layer.yml / images.yml"`
	Uninstall AliasUninstallCmd `cmd:"" help:"Remove all aliases for an image"`
}

// AliasAddCmd creates a single alias
type AliasAddCmd struct {
	Name    string `arg:"" help:"Alias name (command on host)"`
	Image   string `arg:"" help:"Image name from images.yml"`
	Command string `arg:"" optional:"" help:"Command inside container (default: alias name)"`
	Dest    string `long:"dest" default:"" help:"Directory for wrapper scripts (default: ~/.local/bin)"`
}

func (c *AliasAddCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	// Validate image exists
	img, ok := cfg.Images[c.Image]
	if !ok {
		return fmt.Errorf("image %q not found in images.yml", c.Image)
	}
	if !img.IsEnabled() {
		return fmt.Errorf("image %q is disabled", c.Image)
	}

	command := c.Command
	if command == "" {
		command = c.Name
	}

	dest := c.Dest
	if dest == "" {
		dest = defaultAliasDir()
	}

	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dest, err)
	}

	if err := writeAliasScript(dest, c.Name, c.Image, command); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Created alias %s -> %s (image: %s)\n", c.Name, command, c.Image)
	return nil
}

// AliasRemoveCmd removes a single alias
type AliasRemoveCmd struct {
	Name string `arg:"" help:"Alias name to remove"`
	Dest string `long:"dest" default:"" help:"Directory for wrapper scripts (default: ~/.local/bin)"`
}

func (c *AliasRemoveCmd) Run() error {
	dest := c.Dest
	if dest == "" {
		dest = defaultAliasDir()
	}

	if err := removeAliasScript(dest, c.Name); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "Removed alias %s\n", c.Name)
	return nil
}

// AliasListCmd lists all installed aliases
type AliasListCmd struct {
	Dest string `long:"dest" default:"" help:"Directory for wrapper scripts (default: ~/.local/bin)"`
}

func (c *AliasListCmd) Run() error {
	dest := c.Dest
	if dest == "" {
		dest = defaultAliasDir()
	}

	aliases, err := listAliasScripts(dest)
	if err != nil {
		return err
	}

	for _, a := range aliases {
		fmt.Printf("%s\t%s\t%s\n", a.Name, a.Image, a.Command)
	}
	return nil
}

// AliasInstallCmd installs all default aliases for an image
type AliasInstallCmd struct {
	Image string `arg:"" help:"Image name from images.yml"`
	Dest  string `long:"dest" default:"" help:"Directory for wrapper scripts (default: ~/.local/bin)"`
}

func (c *AliasInstallCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanLayers(dir)
	if err != nil {
		return err
	}

	aliases, err := CollectImageAliases(cfg, layers, c.Image)
	if err != nil {
		return err
	}

	if len(aliases) == 0 {
		fmt.Fprintf(os.Stderr, "No aliases defined for image %s\n", c.Image)
		return nil
	}

	dest := c.Dest
	if dest == "" {
		dest = defaultAliasDir()
	}

	if err := os.MkdirAll(dest, 0755); err != nil {
		return fmt.Errorf("creating directory %s: %w", dest, err)
	}

	for _, a := range aliases {
		if err := writeAliasScript(dest, a.Name, c.Image, a.Command); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "Installed %s -> %s\n", a.Name, a.Command)
	}

	fmt.Fprintf(os.Stderr, "Installed %d alias(es) for %s\n", len(aliases), c.Image)
	return nil
}

// AliasUninstallCmd removes all aliases for an image
type AliasUninstallCmd struct {
	Image string `arg:"" help:"Image name from images.yml"`
	Dest  string `long:"dest" default:"" help:"Directory for wrapper scripts (default: ~/.local/bin)"`
}

func (c *AliasUninstallCmd) Run() error {
	dest := c.Dest
	if dest == "" {
		dest = defaultAliasDir()
	}

	aliases, err := listAliasScripts(dest)
	if err != nil {
		return err
	}

	count := 0
	for _, a := range aliases {
		if a.Image == c.Image {
			path := filepath.Join(dest, a.Name)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("removing %s: %w", path, err)
			}
			fmt.Fprintf(os.Stderr, "Removed %s\n", a.Name)
			count++
		}
	}

	fmt.Fprintf(os.Stderr, "Removed %d alias(es) for %s\n", count, c.Image)
	return nil
}

// ListAliasesCmd lists layers with alias declarations
type ListAliasesCmd struct{}

func (c *ListAliasesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	layers, err := ScanLayers(dir)
	if err != nil {
		return err
	}

	result := AliasLayers(layers)
	names := make([]string, 0, len(result))
	for _, layer := range result {
		names = append(names, layer.Name)
	}
	sortStrings(names)

	for _, name := range names {
		layer := layers[name]
		for _, a := range layer.Aliases() {
			fmt.Printf("%s\t%s\t%s\n", name, a.Name, a.Command)
		}
	}
	return nil
}

