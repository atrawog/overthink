package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Config represents the build.json configuration file
type Config struct {
	Defaults ImageConfig            `json:"defaults"`
	Images   map[string]ImageConfig `json:"images"`
}

// MergeConfig configures post-build layer merging
type MergeConfig struct {
	Auto  bool `json:"auto,omitempty"`   // enable automatic merging after builds
	MinMB int  `json:"min_mb,omitempty"` // layers smaller than this are merge candidates (default: 100)
	MaxMB int  `json:"max_mb,omitempty"` // maximum size of a merged layer (default: 1024)
}

// ImageConfig represents configuration for a single image or defaults
type ImageConfig struct {
	Base      string       `json:"base,omitempty"`
	Bootc     bool         `json:"bootc,omitempty"`
	Platforms []string     `json:"platforms,omitempty"`
	Tag       string       `json:"tag,omitempty"`
	Registry  string       `json:"registry,omitempty"`
	Pkg       string       `json:"pkg,omitempty"`
	Layers    []string     `json:"layers,omitempty"`
	Ports     []string     `json:"ports,omitempty"` // runtime port mappings ["host:container"]
	User      string       `json:"user,omitempty"`  // username (default: "user")
	UID       int          `json:"uid,omitempty"`    // user ID (default: 1000)
	GID       int          `json:"gid,omitempty"`    // group ID (default: 1000)
	Merge     *MergeConfig `json:"merge,omitempty"`  // layer merge settings
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

// LoadConfig reads and parses build.json from the given directory
func LoadConfig(dir string) (*Config, error) {
	path := filepath.Join(dir, "build.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading build.json: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing build.json: %w", err)
	}

	return &cfg, nil
}

// ResolveImage resolves a single image's configuration by applying defaults
func (c *Config) ResolveImage(name string, calverTag string) (*ResolvedImage, error) {
	img, ok := c.Images[name]
	if !ok {
		return nil, fmt.Errorf("image %q not found in build.json", name)
	}

	resolved := &ResolvedImage{
		Name: name,
	}

	// Resolve base: image -> defaults -> "fedora:43"
	resolved.Base = img.Base
	if resolved.Base == "" {
		resolved.Base = c.Defaults.Base
	}
	if resolved.Base == "" {
		resolved.Base = "fedora:43"
	}

	// Check if base is internal (another image in build.json) or external
	if _, isInternal := c.Images[resolved.Base]; isInternal {
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
	resolved.UID = img.UID
	if resolved.UID == 0 {
		resolved.UID = c.Defaults.UID
	}
	if resolved.UID == 0 {
		resolved.UID = 1000
	}

	// Resolve GID: image -> defaults -> 1000
	resolved.GID = img.GID
	if resolved.GID == 0 {
		resolved.GID = c.Defaults.GID
	}
	if resolved.GID == 0 {
		resolved.GID = 1000
	}

	// Resolve merge config: image -> defaults -> nil
	if img.Merge != nil {
		resolved.Merge = img.Merge
	} else if c.Defaults.Merge != nil {
		resolved.Merge = c.Defaults.Merge
	}

	// Home directory will be resolved later (after inspecting base image)
	resolved.Home = fmt.Sprintf("/home/%s", resolved.User)

	// Compute full tag
	if resolved.Registry != "" {
		resolved.FullTag = fmt.Sprintf("%s/%s:%s", resolved.Registry, name, resolved.Tag)
	} else {
		resolved.FullTag = fmt.Sprintf("%s:%s", name, resolved.Tag)
	}

	return resolved, nil
}

// ResolveAllImages resolves all images in the config
func (c *Config) ResolveAllImages(calverTag string) (map[string]*ResolvedImage, error) {
	resolved := make(map[string]*ResolvedImage)
	for name := range c.Images {
		img, err := c.ResolveImage(name, calverTag)
		if err != nil {
			return nil, err
		}
		resolved[name] = img
	}
	return resolved, nil
}

// ImageNames returns a sorted list of image names
func (c *Config) ImageNames() []string {
	names := make([]string, 0, len(c.Images))
	for name := range c.Images {
		names = append(names, name)
	}
	// Sort for deterministic output
	sortStrings(names)
	return names
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
