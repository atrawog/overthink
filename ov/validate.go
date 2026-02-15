package main

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidationError collects multiple validation errors
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return fmt.Sprintf("validation error: %s", e.Errors[0])
	}
	return fmt.Sprintf("%d validation errors:\n\n  %s", len(e.Errors), strings.Join(e.Errors, "\n  "))
}

// Add adds an error to the collection
func (e *ValidationError) Add(format string, args ...interface{}) {
	e.Errors = append(e.Errors, fmt.Sprintf(format, args...))
}

// HasErrors returns true if there are any errors
func (e *ValidationError) HasErrors() bool {
	return len(e.Errors) > 0
}

// Validate validates the configuration and layers
func Validate(cfg *Config, layers map[string]*Layer) error {
	errs := &ValidationError{}

	// Validate pkg values
	validatePkgValues(cfg, errs)

	// Validate layers referenced in images
	validateLayerReferences(cfg, layers, errs)

	// Validate layer contents
	validateLayerContents(layers, errs)

	// Validate env files
	validateEnvFiles(layers, errs)

	// Validate copr.repo usage
	validateCoprUsage(cfg, layers, errs)

	// Validate image base references
	validateBaseReferences(cfg, errs)

	// Validate no circular dependencies in images
	validateImageDAG(cfg, errs)

	// Validate ports
	validatePorts(cfg, layers, errs)

	// Validate routes
	validateRoutes(cfg, layers, errs)

	// Validate merge config
	validateMergeConfig(cfg, errs)

	// Validate no circular dependencies in layers
	validateLayerDAG(cfg, layers, errs)

	if errs.HasErrors() {
		return errs
	}
	return nil
}

// validatePkgValues ensures pkg is "rpm" or "deb"
func validatePkgValues(cfg *Config, errs *ValidationError) {
	if cfg.Defaults.Pkg != "" && cfg.Defaults.Pkg != "rpm" && cfg.Defaults.Pkg != "deb" {
		errs.Add("defaults: pkg must be \"rpm\" or \"deb\", got %q", cfg.Defaults.Pkg)
	}

	for name, img := range cfg.Images {
		if img.Pkg != "" && img.Pkg != "rpm" && img.Pkg != "deb" {
			errs.Add("image %q: pkg must be \"rpm\" or \"deb\", got %q", name, img.Pkg)
		}
	}
}

// validateLayerReferences ensures all layers referenced in images exist
func validateLayerReferences(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	for imageName, img := range cfg.Images {
		for _, layerName := range img.Layers {
			if _, ok := layers[layerName]; !ok {
				// Check for typo suggestions
				suggestion := findSimilarName(layerName, LayerNames(layers))
				if suggestion != "" {
					errs.Add("image %q: layer %q not found (did you mean %q?)", imageName, layerName, suggestion)
				} else {
					errs.Add("image %q: layer %q not found", imageName, layerName)
				}
			}
		}
	}
}

// validateLayerContents validates each layer has required files
func validateLayerContents(layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		// Layer must have at least one install file
		if !layer.HasInstallFiles() {
			errs.Add("layer %q: must have at least one install file (rpm.list, deb.list, root.yml, pixi.toml, pyproject.toml, environment.yml, package.json, Cargo.toml, or user.yml)", name)
		}

		// Cargo.toml requires src/ directory
		if layer.HasCargoToml && !layer.HasSrcDir {
			errs.Add("layer %q: Cargo.toml requires src/ directory", name)
		}

		// Validate depends references
		for _, dep := range layer.Depends {
			if _, ok := layers[dep]; !ok {
				suggestion := findSimilarName(dep, LayerNames(layers))
				if suggestion != "" {
					errs.Add("layer %q depends: unknown layer %q (did you mean %q?)", name, dep, suggestion)
				} else {
					errs.Add("layer %q depends: unknown layer %q", name, dep)
				}
			}
		}
	}
}

// validateEnvFiles validates env file syntax
func validateEnvFiles(layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		if !layer.HasEnv {
			continue
		}

		cfg, err := layer.EnvConfig()
		if err != nil {
			errs.Add("layer %q: error parsing env file: %v", name, err)
			continue
		}

		// Warn if PATH is set directly (should use PATH+=)
		if _, hasPath := cfg.Vars["PATH"]; hasPath {
			errs.Add("layer %q env: use PATH+=:/path instead of PATH=/path to append to PATH", name)
		}
	}
}

// validateCoprUsage validates copr.repo usage
func validateCoprUsage(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	for name, layer := range layers {
		// copr.repo without rpm.list is an error
		if layer.HasCoprRepo && !layer.HasRpmList {
			errs.Add("layer %q: copr.repo requires rpm.list", name)
		}
	}

	// Note: copr.repo with deb images is handled at generation time (ignored with warning)
	// No validation error needed here
}

// validateBaseReferences ensures base references resolve
func validateBaseReferences(cfg *Config, errs *ValidationError) {
	// Base references can be:
	// 1. External OCI images (always valid)
	// 2. Names of other images in build.json (validated by image DAG check)
	// No additional validation needed here
}

// validateImageDAG checks for circular image dependencies
func validateImageDAG(cfg *Config, errs *ValidationError) {
	calverTag := "test"
	images, err := cfg.ResolveAllImages(calverTag)
	if err != nil {
		errs.Add("resolving images: %v", err)
		return
	}

	_, err = ResolveImageOrder(images)
	if err != nil {
		if cycleErr, ok := err.(*CycleError); ok {
			errs.Add("image dependency cycle: %s", strings.Join(cycleErr.Cycle, " -> "))
		} else {
			errs.Add("image DAG error: %v", err)
		}
	}
}

// validateLayerDAG checks for circular layer dependencies
func validateLayerDAG(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Check each image's layers for cycles
	for imageName, img := range cfg.Images {
		_, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			if cycleErr, ok := err.(*CycleError); ok {
				errs.Add("image %q: layer dependency cycle: %s", imageName, strings.Join(cycleErr.Cycle, " -> "))
			} else {
				errs.Add("image %q: layer resolution error: %v", imageName, err)
			}
		}
	}
}

// validatePorts validates port declarations in layers and images
func validatePorts(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Validate layer ports files
	for name, layer := range layers {
		if !layer.HasPorts {
			continue
		}
		ports, err := layer.Ports()
		if err != nil {
			errs.Add("layer %q: error reading ports file: %v", name, err)
			continue
		}
		for _, port := range ports {
			if !isValidPort(port) {
				errs.Add("layer %q ports: %q is not a valid port number (1-65535)", name, port)
			}
		}
	}

	// Validate image port mappings
	validatePortMappings := func(name string, ports []string) {
		for _, mapping := range ports {
			parts := strings.Split(mapping, ":")
			switch len(parts) {
			case 1:
				if !isValidPort(parts[0]) {
					errs.Add("image %q ports: %q is not a valid port number (1-65535)", name, parts[0])
				}
			case 2:
				if !isValidPort(parts[0]) {
					errs.Add("image %q ports: host port %q in %q is not valid (1-65535)", name, parts[0], mapping)
				}
				if !isValidPort(parts[1]) {
					errs.Add("image %q ports: container port %q in %q is not valid (1-65535)", name, parts[1], mapping)
				}
			default:
				errs.Add("image %q ports: %q must be \"port\" or \"host:container\" format", name, mapping)
			}
		}
	}

	if len(cfg.Defaults.Ports) > 0 {
		validatePortMappings("defaults", cfg.Defaults.Ports)
	}
	for name, img := range cfg.Images {
		if len(img.Ports) > 0 {
			validatePortMappings(name, img.Ports)
		}
	}
}

// validateRoutes validates route file declarations in layers
func validateRoutes(cfg *Config, layers map[string]*Layer, errs *ValidationError) {
	// Validate route file contents
	for name, layer := range layers {
		if !layer.HasRoute {
			continue
		}
		route, err := layer.Route()
		if err != nil {
			errs.Add("layer %q: error reading route file: %v", name, err)
			continue
		}
		if route.Host == "" {
			errs.Add("layer %q route: missing required \"host\" field", name)
		}
		if route.Port == "" {
			errs.Add("layer %q route: missing required \"port\" field", name)
		} else if !isValidPort(route.Port) {
			errs.Add("layer %q route: %q is not a valid port number (1-65535)", name, route.Port)
		}
	}

	// For each image with route layers, traefik must be reachable
	for imageName, img := range cfg.Images {
		hasRoute := false
		hasTraefik := false

		// Resolve full layer order for this image (includes transitive deps)
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			continue // layer DAG validation will catch this
		}

		for _, layerName := range resolved {
			if layer, ok := layers[layerName]; ok {
				if layer.HasRoute {
					hasRoute = true
				}
				if layerName == "traefik" {
					hasTraefik = true
				}
			}
		}

		if hasRoute && !hasTraefik {
			errs.Add("image %q: has layers with route files but traefik layer is not reachable", imageName)
		}
	}
}

// validateMergeConfig validates merge configuration
func validateMergeConfig(cfg *Config, errs *ValidationError) {
	check := func(name string, m *MergeConfig) {
		if m == nil {
			return
		}
		if m.MaxMB < 0 {
			errs.Add("%s: merge max_mb must be > 0, got %d", name, m.MaxMB)
		}
	}

	check("defaults", cfg.Defaults.Merge)
	for name, img := range cfg.Images {
		check(fmt.Sprintf("image %q", name), img.Merge)
	}
}

// isValidPort checks if a string is a valid port number (1-65535)
func isValidPort(s string) bool {
	n, err := strconv.Atoi(s)
	if err != nil {
		return false
	}
	return n >= 1 && n <= 65535
}

// findSimilarName finds a similar name for typo suggestions
func findSimilarName(target string, candidates []string) string {
	// Simple Levenshtein-like check for close matches
	for _, candidate := range candidates {
		if levenshteinDistance(target, candidate) <= 2 {
			return candidate
		}
	}
	return ""
}

// levenshteinDistance calculates the edit distance between two strings
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Create matrix
	matrix := make([][]int, len(a)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(b)+1)
		matrix[i][0] = i
	}
	for j := range matrix[0] {
		matrix[0][j] = j
	}

	// Fill matrix
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			matrix[i][j] = min(
				matrix[i-1][j]+1,      // deletion
				matrix[i][j-1]+1,      // insertion
				matrix[i-1][j-1]+cost, // substitution
			)
		}
	}

	return matrix[len(a)][len(b)]
}

func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
