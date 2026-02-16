package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Layer represents a layer directory and its contents
type Layer struct {
	Name              string
	Path              string
	HasRpmList        bool
	HasDebList        bool
	HasCoprRepo       bool
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
	HasPixiLock       bool
	Depends           []string

	// Cached file contents (loaded on demand)
	rpmPackages []string
	debPackages []string
	coprRepos   []string
	ports       []string
	envConfig   *EnvConfig
	route       *RouteConfig
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

// scanLayer scans a single layer directory
func scanLayer(path string, name string) (*Layer, error) {
	layer := &Layer{
		Name: name,
		Path: path,
	}

	// Check for each possible file
	layer.HasRpmList = fileExists(filepath.Join(path, "rpm.list"))
	layer.HasDebList = fileExists(filepath.Join(path, "deb.list"))
	layer.HasCoprRepo = fileExists(filepath.Join(path, "copr.repo"))
	layer.HasRootYml = fileExists(filepath.Join(path, "root.yml"))
	layer.HasPixiToml = fileExists(filepath.Join(path, "pixi.toml"))
	layer.HasPyprojectToml = fileExists(filepath.Join(path, "pyproject.toml"))
	layer.HasEnvironmentYml = fileExists(filepath.Join(path, "environment.yml"))
	layer.HasPackageJson = fileExists(filepath.Join(path, "package.json"))
	layer.HasCargoToml = fileExists(filepath.Join(path, "Cargo.toml"))
	layer.HasSrcDir = dirExists(filepath.Join(path, "src"))
	layer.HasUserYml = fileExists(filepath.Join(path, "user.yml"))
	layer.HasSupervisord = fileExists(filepath.Join(path, "supervisord.conf"))
	layer.HasEnv = fileExists(filepath.Join(path, "env"))
	layer.HasPorts = fileExists(filepath.Join(path, "ports"))
	layer.HasRoute = fileExists(filepath.Join(path, "route"))
	layer.HasPixiLock = fileExists(filepath.Join(path, "pixi.lock"))

	// Read depends file if present
	dependsPath := filepath.Join(path, "depends")
	if fileExists(dependsPath) {
		deps, err := readLineFile(dependsPath)
		if err != nil {
			return nil, fmt.Errorf("reading depends: %w", err)
		}
		layer.Depends = deps
	}

	return layer, nil
}

// HasInstallFiles returns true if the layer has at least one install file
func (l *Layer) HasInstallFiles() bool {
	return l.HasRpmList || l.HasDebList || l.HasRootYml ||
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

// RpmPackages returns the packages from rpm.list (cached)
func (l *Layer) RpmPackages() ([]string, error) {
	if l.rpmPackages != nil {
		return l.rpmPackages, nil
	}
	if !l.HasRpmList {
		return nil, nil
	}

	pkgs, err := readLineFile(filepath.Join(l.Path, "rpm.list"))
	if err != nil {
		return nil, err
	}
	l.rpmPackages = pkgs
	return l.rpmPackages, nil
}

// DebPackages returns the packages from deb.list (cached)
func (l *Layer) DebPackages() ([]string, error) {
	if l.debPackages != nil {
		return l.debPackages, nil
	}
	if !l.HasDebList {
		return nil, nil
	}

	pkgs, err := readLineFile(filepath.Join(l.Path, "deb.list"))
	if err != nil {
		return nil, err
	}
	l.debPackages = pkgs
	return l.debPackages, nil
}

// CoprRepos returns the COPR repos from copr.repo (cached)
func (l *Layer) CoprRepos() ([]string, error) {
	if l.coprRepos != nil {
		return l.coprRepos, nil
	}
	if !l.HasCoprRepo {
		return nil, nil
	}

	repos, err := readLineFile(filepath.Join(l.Path, "copr.repo"))
	if err != nil {
		return nil, err
	}
	l.coprRepos = repos
	return l.coprRepos, nil
}

// EnvConfig returns the environment config from env file (cached)
func (l *Layer) EnvConfig() (*EnvConfig, error) {
	if l.envConfig != nil {
		return l.envConfig, nil
	}
	if !l.HasEnv {
		return nil, nil
	}

	cfg, err := ParseEnvFile(filepath.Join(l.Path, "env"))
	if err != nil {
		return nil, err
	}
	l.envConfig = cfg
	return l.envConfig, nil
}

// Ports returns the ports from the ports file (cached)
func (l *Layer) Ports() ([]string, error) {
	if l.ports != nil {
		return l.ports, nil
	}
	if !l.HasPorts {
		return nil, nil
	}

	ports, err := readLineFile(filepath.Join(l.Path, "ports"))
	if err != nil {
		return nil, err
	}
	l.ports = ports
	return l.ports, nil
}

// RouteConfig represents a route file declaration
type RouteConfig struct {
	Host string
	Port string
}

// Route returns the route config from the route file (cached)
func (l *Layer) Route() (*RouteConfig, error) {
	if l.route != nil {
		return l.route, nil
	}
	if !l.HasRoute {
		return nil, nil
	}

	lines, err := readLineFile(filepath.Join(l.Path, "route"))
	if err != nil {
		return nil, err
	}

	cfg := &RouteConfig{}
	for _, line := range lines {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		switch key {
		case "host":
			cfg.Host = value
		case "port":
			cfg.Port = value
		}
	}

	l.route = cfg
	return l.route, nil
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

// readLineFile reads a file and returns non-empty, non-comment lines
func readLineFile(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var lines []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// Remove comments
		if idx := strings.Index(line, "#"); idx >= 0 {
			line = line[:idx]
		}
		// Trim whitespace
		line = strings.TrimSpace(line)
		// Skip empty lines
		if line == "" {
			continue
		}
		lines = append(lines, line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return lines, nil
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
