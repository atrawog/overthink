package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// VolumeYAML represents a volume declaration in layer.yml
type VolumeYAML struct {
	Name string `yaml:"name"`
	Path string `yaml:"path"`
}

// AliasYAML represents a command alias declaration in layer.yml
type AliasYAML struct {
	Name    string `yaml:"name"`
	Command string `yaml:"command"`
}

// LayerYAML represents the parsed layer.yml file
type LayerYAML struct {
	Depends    []string          `yaml:"depends,omitempty"`
	Env        map[string]string `yaml:"env,omitempty"`
	PathAppend []string          `yaml:"path_append,omitempty"`
	Ports      []int             `yaml:"ports,omitempty"`
	Route      *RouteYAML        `yaml:"route,omitempty"`
	Service    string            `yaml:"service,omitempty"`
	Rpm        *RpmConfig        `yaml:"rpm,omitempty"`
	Deb        *DebConfig        `yaml:"deb,omitempty"`
	Volumes    []VolumeYAML      `yaml:"volumes,omitempty"`
	Aliases    []AliasYAML       `yaml:"aliases,omitempty"`
}

// RouteYAML represents a route declaration in layer.yml
type RouteYAML struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// RpmConfig represents RPM package configuration in layer.yml
type RpmConfig struct {
	Packages []string  `yaml:"packages,omitempty"`
	Copr     []string  `yaml:"copr,omitempty"`
	Repos    []RpmRepo `yaml:"repos,omitempty"`
	Exclude  []string  `yaml:"exclude,omitempty"`
	Options  []string  `yaml:"options,omitempty"`
}

// RpmRepo represents an external RPM repository
type RpmRepo struct {
	Name   string `yaml:"name"`
	URL    string `yaml:"url"`
	GPGKey string `yaml:"gpgkey,omitempty"`
}

// DebConfig represents Debian package configuration in layer.yml
type DebConfig struct {
	Packages []string `yaml:"packages,omitempty"`
}

// Layer represents a layer directory and its contents
type Layer struct {
	Name              string
	Path              string
	HasRootYml        bool
	HasPixiToml       bool
	HasPyprojectToml  bool
	HasEnvironmentYml bool
	HasPackageJson    bool
	HasCargoToml      bool
	HasSrcDir         bool
	HasUserYml        bool
	HasSupervisord    bool
	HasEnv            bool
	HasPorts          bool
	HasRoute          bool
	HasVolumes        bool
	HasAliases        bool
	HasPixiLock       bool
	Depends           []string

	// Pre-populated from layer.yml
	rpmConfig   *RpmConfig
	debConfig   *DebConfig
	ports       []string
	envConfig   *EnvConfig
	route       *RouteConfig
	serviceConf string
	volumes     []VolumeYAML
	aliases     []AliasYAML
}

// ScanLayers scans the layers/ directory and returns all layers
func ScanLayers(dir string) (map[string]*Layer, error) {
	layersDir := filepath.Join(dir, "layers")
	entries, err := os.ReadDir(layersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return make(map[string]*Layer), nil
		}
		return nil, fmt.Errorf("reading layers directory: %w", err)
	}

	layers := make(map[string]*Layer)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		layer, err := scanLayer(filepath.Join(layersDir, name), name)
		if err != nil {
			return nil, fmt.Errorf("scanning layer %s: %w", name, err)
		}
		layers[name] = layer
	}

	return layers, nil
}

// parseLayerYAML reads and unmarshals a layer.yml file
func parseLayerYAML(path string) (*LayerYAML, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ly LayerYAML
	if err := yaml.Unmarshal(data, &ly); err != nil {
		return nil, err
	}
	return &ly, nil
}

// scanLayer scans a single layer directory
func scanLayer(path string, name string) (*Layer, error) {
	layer := &Layer{
		Name: name,
		Path: path,
	}

	// Check for install files
	layer.HasRootYml = fileExists(filepath.Join(path, "root.yml"))
	layer.HasPixiToml = fileExists(filepath.Join(path, "pixi.toml"))
	layer.HasPyprojectToml = fileExists(filepath.Join(path, "pyproject.toml"))
	layer.HasEnvironmentYml = fileExists(filepath.Join(path, "environment.yml"))
	layer.HasPackageJson = fileExists(filepath.Join(path, "package.json"))
	layer.HasCargoToml = fileExists(filepath.Join(path, "Cargo.toml"))
	layer.HasSrcDir = dirExists(filepath.Join(path, "src"))
	layer.HasUserYml = fileExists(filepath.Join(path, "user.yml"))
	layer.HasPixiLock = fileExists(filepath.Join(path, "pixi.lock"))

	// Parse layer.yml if present
	yamlPath := filepath.Join(path, "layer.yml")
	if fileExists(yamlPath) {
		ly, err := parseLayerYAML(yamlPath)
		if err != nil {
			return nil, fmt.Errorf("parsing layer.yml: %w", err)
		}

		layer.Depends = ly.Depends
		layer.HasSupervisord = ly.Service != ""
		layer.serviceConf = ly.Service
		layer.HasEnv = len(ly.Env) > 0 || len(ly.PathAppend) > 0
		layer.HasPorts = len(ly.Ports) > 0
		layer.HasRoute = ly.Route != nil

		// Pre-populate package config
		layer.rpmConfig = ly.Rpm
		layer.debConfig = ly.Deb

		// Pre-populate ports cache
		if layer.HasPorts {
			layer.ports = make([]string, len(ly.Ports))
			for i, p := range ly.Ports {
				layer.ports[i] = strconv.Itoa(p)
			}
		}

		// Pre-populate env cache
		if layer.HasEnv {
			env := ly.Env
			if env == nil {
				env = make(map[string]string)
			}
			layer.envConfig = &EnvConfig{
				Vars:       env,
				PathAppend: ly.PathAppend,
			}
		}

		// Pre-populate route cache
		if ly.Route != nil {
			layer.route = &RouteConfig{
				Host: ly.Route.Host,
				Port: strconv.Itoa(ly.Route.Port),
			}
		}

		// Pre-populate volumes
		layer.HasVolumes = len(ly.Volumes) > 0
		layer.volumes = ly.Volumes

		// Pre-populate aliases
		layer.HasAliases = len(ly.Aliases) > 0
		layer.aliases = ly.Aliases
	}

	return layer, nil
}

// HasInstallFiles returns true if the layer has at least one install file
func (l *Layer) HasInstallFiles() bool {
	hasRpm := l.rpmConfig != nil && len(l.rpmConfig.Packages) > 0
	hasDeb := l.debConfig != nil && len(l.debConfig.Packages) > 0
	return hasRpm || hasDeb || l.HasRootYml ||
		l.HasPixiToml || l.HasPyprojectToml || l.HasEnvironmentYml ||
		l.HasPackageJson || l.HasCargoToml || l.HasUserYml
}

// PixiManifest returns the filename of the pixi manifest if it exists
func (l *Layer) PixiManifest() string {
	if l.HasPixiToml {
		return "pixi.toml"
	}
	if l.HasPyprojectToml {
		return "pyproject.toml"
	}
	if l.HasEnvironmentYml {
		return "environment.yml"
	}
	return ""
}

// RpmConfig returns the RPM package config (pre-populated from layer.yml)
func (l *Layer) RpmConfig() *RpmConfig {
	return l.rpmConfig
}

// DebConfig returns the Debian package config (pre-populated from layer.yml)
func (l *Layer) DebConfig() *DebConfig {
	return l.debConfig
}

// EnvConfig returns the environment config (pre-populated from layer.yml)
func (l *Layer) EnvConfig() (*EnvConfig, error) {
	if l.envConfig != nil {
		return l.envConfig, nil
	}
	return nil, nil
}

// Ports returns the ports (pre-populated from layer.yml)
func (l *Layer) Ports() ([]string, error) {
	if l.ports != nil {
		return l.ports, nil
	}
	return nil, nil
}

// ServiceConf returns the supervisord service fragment from layer.yml
func (l *Layer) ServiceConf() string {
	return l.serviceConf
}

// RouteConfig represents a route file declaration
type RouteConfig struct {
	Host string
	Port string
}

// Route returns the route config (pre-populated from layer.yml)
func (l *Layer) Route() (*RouteConfig, error) {
	if l.route != nil {
		return l.route, nil
	}
	return nil, nil
}

// RouteLayers returns layers that have a route file
func RouteLayers(layers map[string]*Layer) []*Layer {
	var routes []*Layer
	for _, layer := range layers {
		if layer.HasRoute {
			routes = append(routes, layer)
		}
	}
	return routes
}

// LayerNames returns a sorted list of layer names
func LayerNames(layers map[string]*Layer) []string {
	names := make([]string, 0, len(layers))
	for name := range layers {
		names = append(names, name)
	}
	sortStrings(names)
	return names
}

// Volumes returns the volume declarations (pre-populated from layer.yml)
func (l *Layer) Volumes() []VolumeYAML {
	return l.volumes
}

// ServiceLayers returns layers that have supervisord.conf
func ServiceLayers(layers map[string]*Layer) []*Layer {
	var services []*Layer
	for _, layer := range layers {
		if layer.HasSupervisord {
			services = append(services, layer)
		}
	}
	return services
}

// VolumeLayers returns layers that have volume declarations
func VolumeLayers(layers map[string]*Layer) []*Layer {
	var vols []*Layer
	for _, layer := range layers {
		if layer.HasVolumes {
			vols = append(vols, layer)
		}
	}
	return vols
}

// Aliases returns the alias declarations (pre-populated from layer.yml)
func (l *Layer) Aliases() []AliasYAML {
	return l.aliases
}

// AliasLayers returns layers that have alias declarations
func AliasLayers(layers map[string]*Layer) []*Layer {
	var result []*Layer
	for _, layer := range layers {
		if layer.HasAliases {
			result = append(result, layer)
		}
	}
	return result
}

// NeedsGit returns true if the pixi manifest contains git-based dependencies
func (l *Layer) NeedsGit() bool {
	manifest := l.PixiManifest()
	if manifest == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(l.Path, manifest))
	if err != nil {
		return false
	}
	content := string(data)
	// Check for PyPI git+ format and pixi { git = "..." } format
	return strings.Contains(content, "git+") || strings.Contains(content, "{ git =")
}

// HasPypiDeps returns true if the pixi manifest has PyPI dependencies
func (l *Layer) HasPypiDeps() bool {
	manifest := l.PixiManifest()
	if manifest == "" {
		return false
	}
	data, err := os.ReadFile(filepath.Join(l.Path, manifest))
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "[pypi-dependencies]")
}

// fileExists checks if a file exists
func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// dirExists checks if a directory exists
func dirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}
