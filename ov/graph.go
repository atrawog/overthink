package main

import (
	"fmt"
	"strings"
)

// CycleError represents a circular dependency error
type CycleError struct {
	Cycle []string
}

func (e *CycleError) Error() string {
	return fmt.Sprintf("circular dependency: %s", strings.Join(e.Cycle, " -> "))
}

// ResolveLayerOrder resolves layer dependencies and returns them in topological order.
// It takes the explicitly requested layers and the full layer map, then:
// 1. Transitively resolves all dependencies
// 2. Topologically sorts the result
// 3. Returns layers in install order (dependencies before dependents)
//
// parentLayers contains layers already installed by parent images (via base chain).
// These are excluded from the returned order.
func ResolveLayerOrder(requested []string, layers map[string]*Layer, parentLayers map[string]bool) ([]string, error) {
	// Build the set of all layers we need (transitive closure)
	needed := make(map[string]bool)
	visiting := make(map[string]bool) // Track current path for cycle detection

	var addTransitive func(name string, path []string) error
	addTransitive = func(name string, path []string) error {
		if needed[name] {
			return nil
		}
		if parentLayers[name] {
			// Already provided by parent, skip
			return nil
		}

		// Check for cycle
		if visiting[name] {
			cycle := append(path, name)
			return &CycleError{Cycle: cycle}
		}

		layer, ok := layers[name]
		if !ok {
			return fmt.Errorf("unknown layer %q", name)
		}

		visiting[name] = true
		newPath := append(path, name)

		// Add dependencies first
		for _, dep := range layer.Depends {
			if err := addTransitive(dep, newPath); err != nil {
				return err
			}
		}

		visiting[name] = false
		needed[name] = true
		return nil
	}

	for _, name := range requested {
		if err := addTransitive(name, nil); err != nil {
			return nil, err
		}
	}

	// Build adjacency list for topological sort
	// Edge from A to B means A depends on B (B must come before A)
	graph := make(map[string][]string)
	for name := range needed {
		layer := layers[name]
		var deps []string
		for _, dep := range layer.Depends {
			if needed[dep] { // Only include deps that are in our needed set
				deps = append(deps, dep)
			}
		}
		graph[name] = deps
	}

	// Topological sort using Kahn's algorithm
	return topoSort(graph)
}

// ImageNeedsBuilder returns true if any of the image's own resolved layers
// (excluding parent-provided) have pixi.toml, package.json, or Cargo.toml.
// When layers is nil, falls back to unconditional builder dependency.
func ImageNeedsBuilder(img *ResolvedImage, images map[string]*ResolvedImage, layers map[string]*Layer, builderName string) bool {
	if layers == nil {
		return true // conservative fallback
	}

	// Get parent-provided layers
	var parentLayers map[string]bool
	if !img.IsExternalBase {
		var err error
		parentLayers, err = LayersProvidedByImage(img.Base, images, layers)
		if err != nil {
			return true // conservative
		}
	}

	// Resolve this image's own layers (excluding parent)
	resolved, err := ResolveLayerOrder(img.Layers, layers, parentLayers)
	if err != nil {
		return true // conservative
	}

	for _, layerName := range resolved {
		layer, ok := layers[layerName]
		if !ok {
			continue
		}
		if layer.PixiManifest() != "" || layer.HasPackageJson || layer.HasCargoToml {
			return true
		}
	}
	return false
}

// ResolveImageOrder resolves image dependencies and returns them in build order.
// Images that reference other images via `base` create dependencies.
// When builderName is non-empty, images that need multi-stage builds depend on the builder.
// Pass layers to enable conditional builder dependency; nil for unconditional.
func ResolveImageOrder(images map[string]*ResolvedImage, layers map[string]*Layer, builderName string) ([]string, error) {
	// Build adjacency list
	// Edge from A to B means A depends on B (B must be built before A)
	graph := make(map[string][]string)
	for name, img := range images {
		var deps []string
		if !img.IsExternalBase {
			// base is another image in images.yml
			deps = append(deps, img.Base)
		}
		// Only images that need multi-stage builds depend on the builder
		if builderName != "" && name != builderName {
			if _, ok := images[builderName]; ok {
				if ImageNeedsBuilder(img, images, layers, builderName) {
					deps = append(deps, builderName)
				}
			}
		}
		graph[name] = deps
	}

	return topoSort(graph)
}

// topoSort performs topological sort using Kahn's algorithm.
// Returns nodes in dependency order (dependencies before dependents).
func topoSort(graph map[string][]string) ([]string, error) {
	// Calculate in-degrees
	inDegree := make(map[string]int)
	for node := range graph {
		if _, ok := inDegree[node]; !ok {
			inDegree[node] = 0
		}
		for _, dep := range graph[node] {
			inDegree[dep] = inDegree[dep] // ensure dep exists in map
		}
	}

	// For each edge A -> B (A depends on B), increment in-degree of A
	// (because B must come before A)
	reverseGraph := make(map[string][]string)
	for node, deps := range graph {
		for _, dep := range deps {
			reverseGraph[dep] = append(reverseGraph[dep], node)
		}
	}

	// Recalculate in-degrees based on reverse graph
	for node := range graph {
		inDegree[node] = len(graph[node])
	}

	// Find all nodes with no dependencies (in-degree 0)
	var queue []string
	for node, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}

	// Sort queue for deterministic output
	sortStrings(queue)

	var result []string
	for len(queue) > 0 {
		// Take the first node (lexicographically smallest for determinism)
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		// For each node that depends on this one, decrement in-degree
		dependents := reverseGraph[node]
		sortStrings(dependents) // for determinism
		for _, dependent := range dependents {
			inDegree[dependent]--
			if inDegree[dependent] == 0 {
				queue = append(queue, dependent)
				sortStrings(queue) // maintain sorted order
			}
		}
	}

	// Check for cycles
	if len(result) != len(graph) {
		// Find a cycle for error reporting
		cycle := findCycle(graph, inDegree)
		return nil, &CycleError{Cycle: cycle}
	}

	return result, nil
}

// findCycle finds a cycle in the graph for error reporting
func findCycle(graph map[string][]string, inDegree map[string]int) []string {
	// Start from any node still in the graph (non-zero in-degree)
	var start string
	for node, degree := range inDegree {
		if degree > 0 {
			start = node
			break
		}
	}

	// DFS to find cycle
	visited := make(map[string]bool)
	path := make(map[string]bool)
	var cyclePath []string

	var dfs func(node string) bool
	dfs = func(node string) bool {
		visited[node] = true
		path[node] = true
		cyclePath = append(cyclePath, node)

		for _, dep := range graph[node] {
			if !visited[dep] {
				if dfs(dep) {
					return true
				}
			} else if path[dep] {
				// Found cycle
				cyclePath = append(cyclePath, dep)
				return true
			}
		}

		path[node] = false
		cyclePath = cyclePath[:len(cyclePath)-1]
		return false
	}

	if start != "" {
		dfs(start)
	}

	return cyclePath
}

// LayersProvidedByImage returns the set of layers installed by an image
// (including those inherited from parent images via base chain)
func LayersProvidedByImage(imageName string, images map[string]*ResolvedImage, layers map[string]*Layer) (map[string]bool, error) {
	provided := make(map[string]bool)

	var collect func(name string) error
	collect = func(name string) error {
		img, ok := images[name]
		if !ok {
			return fmt.Errorf("image %q not found", name)
		}

		// If base is internal, collect from parent first
		if !img.IsExternalBase {
			if err := collect(img.Base); err != nil {
				return err
			}
		}

		// Add this image's layers
		for _, layerName := range img.Layers {
			provided[layerName] = true
		}

		return nil
	}

	if err := collect(imageName); err != nil {
		return nil, err
	}

	return provided, nil
}
