package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Config represents the images.yaml configuration file
type Config struct {
	Defaults ImageConfig            `yaml:"defaults"`
	Images   map[string]ImageConfig `yaml:"images"`
}

// MergeConfig configures post-build layer merging
type MergeConfig struct {
	Auto  bool `yaml:"auto,omitempty"`   // enable automatic merging after builds
	MaxMB int  `yaml:"max_mb,omitempty"` // maximum size of a merged layer (default: 1024)
}

// ImageConfig represents configuration for a single image or defaults
type ImageConfig struct {
	Enabled   *bool        `yaml:"enabled,omitempty"`
	Base      string       `yaml:"base,omitempty"`
	Bootc     bool         `yaml:"bootc,omitempty"`
	Platforms []string     `yaml:"platforms,omitempty"`
	Tag       string       `yaml:"tag,omitempty"`
	Registry  string       `yaml:"registry,omitempty"`
	Pkg       string       `yaml:"pkg,omitempty"`
	Layers    []string     `yaml:"layers,omitempty"`
	Ports     []string     `yaml:"ports,omitempty"` // runtime port mappings ["host:container"]
	User      string       `yaml:"user,omitempty"`  // username (default: "user")
	UID       *int         `yaml:"uid,omitempty"`   // user ID (default: 1000)
	GID       *int         `yaml:"gid,omitempty"`   // group ID (default: 1000)
	Merge     *MergeConfig `yaml:"merge,omitempty"` // layer merge settings
}

// IsEnabled returns true if the image is enabled (nil defaults to true)
func (ic *ImageConfig) IsEnabled() bool {
	if ic.Enabled == nil {
		return true
	}
	return *ic.Enabled
}

// boolPtr returns a pointer to a bool value
func boolPtr(v bool) *bool {
	return &v
}

// ResolvedImage represents a fully resolved image configuration
type ResolvedImage struct {
	Name      string
	Base      string   // Resolved base (external OCI ref or internal image name)
	Bootc     bool
	Platforms []string
	Tag       string
	Registry  string
	Pkg       string
	Layers    []string
	Ports     []string // runtime port mappings

	// User configuration
	User string // username
	UID  int    // user ID
	GID  int    // group ID
	Home string // resolved home directory (detected or /home/<user>)

	// Merge configuration
	Merge *MergeConfig // layer merge settings (nil means use CLI defaults)

	// Derived fields
	IsExternalBase bool   // true if base is external OCI image, false if internal
	FullTag        string // registry/name:tag
}

// LoadConfig reads and parses images.yaml from the given directory
func LoadConfig(dir string) (*Config, error) {
	path := filepath.Join(dir, "images.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading images.yaml: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing images.yaml: %w", err)
	}

	return &cfg, nil
}

// ResolveImage resolves a single image's configuration by applying defaults
func (c *Config) ResolveImage(name string, calverTag string) (*ResolvedImage, error) {
	img, ok := c.Images[name]
	if !ok {
		return nil, fmt.Errorf("image %q not found in images.yaml", name)
	}
	if !img.IsEnabled() {
		return nil, fmt.Errorf("image %q is disabled", name)
	}

	resolved := &ResolvedImage{
		Name: name,
	}

	// Resolve base: image -> defaults -> "quay.io/fedora/fedora:43"
	resolved.Base = img.Base
	if resolved.Base == "" {
		resolved.Base = c.Defaults.Base
	}
	if resolved.Base == "" {
		resolved.Base = "quay.io/fedora/fedora:43"
	}

	// Check if base is internal (another enabled image in images.yaml) or external
	if baseImg, isInternal := c.Images[resolved.Base]; isInternal && baseImg.IsEnabled() {
		resolved.IsExternalBase = false
	} else {
		resolved.IsExternalBase = true
	}

	// Resolve bootc: image -> defaults -> false
	resolved.Bootc = img.Bootc
	if !resolved.Bootc {
		resolved.Bootc = c.Defaults.Bootc
	}

	// Resolve platforms: image -> defaults -> ["linux/amd64", "linux/arm64"]
	resolved.Platforms = img.Platforms
	if len(resolved.Platforms) == 0 {
		resolved.Platforms = c.Defaults.Platforms
	}
	if len(resolved.Platforms) == 0 {
		resolved.Platforms = []string{"linux/amd64", "linux/arm64"}
	}

	// Resolve tag: image -> defaults -> "auto"
	resolved.Tag = img.Tag
	if resolved.Tag == "" {
		resolved.Tag = c.Defaults.Tag
	}
	if resolved.Tag == "" {
		resolved.Tag = "auto"
	}
	// If tag is "auto", use the computed calver
	if resolved.Tag == "auto" {
		resolved.Tag = calverTag
	}

	// Resolve registry: image -> defaults -> ""
	resolved.Registry = img.Registry
	if resolved.Registry == "" {
		resolved.Registry = c.Defaults.Registry
	}

	// Resolve pkg: image -> defaults -> "rpm"
	resolved.Pkg = img.Pkg
	if resolved.Pkg == "" {
		resolved.Pkg = c.Defaults.Pkg
	}
	if resolved.Pkg == "" {
		resolved.Pkg = "rpm"
	}

	// Layers are not inherited, they're image-specific
	resolved.Layers = img.Layers

	// Resolve ports: image -> defaults -> nil
	resolved.Ports = img.Ports
	if len(resolved.Ports) == 0 {
		resolved.Ports = c.Defaults.Ports
	}

	// Resolve user: image -> defaults -> "user"
	resolved.User = img.User
	if resolved.User == "" {
		resolved.User = c.Defaults.User
	}
	if resolved.User == "" {
		resolved.User = "user"
	}

	// Resolve UID: image -> defaults -> 1000
	resolved.UID = resolveIntPtr(img.UID, c.Defaults.UID, 1000)

	// Resolve GID: image -> defaults -> 1000
	resolved.GID = resolveIntPtr(img.GID, c.Defaults.GID, 1000)

	// Resolve merge config: image -> defaults -> nil
	if img.Merge != nil {
		resolved.Merge = img.Merge
	} else if c.Defaults.Merge != nil {
		resolved.Merge = c.Defaults.Merge
	}

	// Home directory will be resolved later (after inspecting base image)
	if resolved.User == "root" {
		resolved.Home = "/root"
	} else {
		resolved.Home = fmt.Sprintf("/home/%s", resolved.User)
	}

	// Compute full tag
	if resolved.Registry != "" {
		resolved.FullTag = fmt.Sprintf("%s/%s:%s", resolved.Registry, name, resolved.Tag)
	} else {
		resolved.FullTag = fmt.Sprintf("%s:%s", name, resolved.Tag)
	}

	return resolved, nil
}

// ResolveAllImages resolves all enabled images in the config
func (c *Config) ResolveAllImages(calverTag string) (map[string]*ResolvedImage, error) {
	resolved := make(map[string]*ResolvedImage)
	for name, img := range c.Images {
		if !img.IsEnabled() {
			continue
		}
		ri, err := c.ResolveImage(name, calverTag)
		if err != nil {
			return nil, err
		}
		resolved[name] = ri
	}
	return resolved, nil
}

// ImageNames returns a sorted list of enabled image names
func (c *Config) ImageNames() []string {
	names := make([]string, 0, len(c.Images))
	for name, img := range c.Images {
		if !img.IsEnabled() {
			continue
		}
		names = append(names, name)
	}
	// Sort for deterministic output
	sortStrings(names)
	return names
}

// resolveIntPtr resolves a *int value through fallback chain: value -> fallback -> defaultVal
func resolveIntPtr(value, fallback *int, defaultVal int) int {
	if value != nil {
		return *value
	}
	if fallback != nil {
		return *fallback
	}
	return defaultVal
}

// intPtr returns a pointer to an int value
func intPtr(v int) *int {
	return &v
}

// sortStrings sorts a slice of strings in place
func sortStrings(s []string) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}
