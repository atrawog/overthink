package main

import (
	"fmt"
)

// trieNode represents a node in the layer prefix trie.
type trieNode struct {
	layer    string                // layer at this position ("" for root)
	children map[string]*trieNode // layer name → child node
	images   []string             // user-defined images terminating here
}

func newTrieNode(layer string) *trieNode {
	return &trieNode{
		layer:    layer,
		children: make(map[string]*trieNode),
	}
}

// GlobalLayerOrder computes a global topological order of all layers across
// all enabled images, using popularity (number of images needing each layer)
// as the primary tie-breaker and lexicographic as secondary.
func GlobalLayerOrder(images map[string]*ResolvedImage, layers map[string]*Layer) ([]string, error) {
	// Count popularity: how many images need each layer (including transitive deps)
	popularity := make(map[string]int)
	for _, img := range images {
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			return nil, fmt.Errorf("resolving layers for image %q: %w", img.Name, err)
		}
		// Also include layers from the base chain
		allLayers := collectAllImageLayers(img.Name, images, layers)
		// Merge resolved with allLayers
		seen := make(map[string]bool)
		for _, l := range allLayers {
			seen[l] = true
		}
		for _, l := range resolved {
			if !seen[l] {
				allLayers = append(allLayers, l)
				seen[l] = true
			}
		}
		for _, l := range allLayers {
			popularity[l]++
		}
	}

	// Build dependency graph from layer depends
	// Only include layers that appear in at least one image
	graph := make(map[string][]string)
	for name := range popularity {
		layer, ok := layers[name]
		if !ok {
			continue
		}
		var deps []string
		for _, dep := range layer.Depends {
			if _, inUse := popularity[dep]; inUse {
				deps = append(deps, dep)
			}
		}
		graph[name] = deps
	}

	// Kahn's algorithm with popularity-based tie-breaking
	return topoSortByPopularity(graph, popularity)
}

// topoSortByPopularity performs topological sort with popularity tie-breaking.
// Higher popularity layers come first among zero-in-degree candidates.
func topoSortByPopularity(graph map[string][]string, popularity map[string]int) ([]string, error) {
	inDegree := make(map[string]int)
	reverseGraph := make(map[string][]string)

	for node := range graph {
		inDegree[node] = len(graph[node])
		for _, dep := range graph[node] {
			reverseGraph[dep] = append(reverseGraph[dep], node)
		}
	}

	// Find all nodes with no dependencies
	var queue []string
	for node, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, node)
		}
	}
	sortByPopularity(queue, popularity)

	var result []string
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		result = append(result, node)

		dependents := reverseGraph[node]
		for _, dep := range dependents {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
		sortByPopularity(queue, popularity)
	}

	if len(result) != len(graph) {
		return nil, fmt.Errorf("cycle detected in layer dependency graph")
	}
	return result, nil
}

// sortByPopularity sorts by descending popularity, then lexicographic ascending.
func sortByPopularity(s []string, popularity map[string]int) {
	for i := 0; i < len(s)-1; i++ {
		for j := i + 1; j < len(s); j++ {
			pi, pj := popularity[s[i]], popularity[s[j]]
			if pi < pj || (pi == pj && s[i] > s[j]) {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}

// collectAllImageLayers returns the complete set of layers for an image,
// including all layers inherited through the base chain.
func collectAllImageLayers(imageName string, images map[string]*ResolvedImage, layers map[string]*Layer) []string {
	seen := make(map[string]bool)
	var result []string

	var walk func(name string)
	walk = func(name string) {
		img, ok := images[name]
		if !ok {
			return
		}
		if !img.IsExternalBase {
			walk(img.Base)
		}
		resolved, err := ResolveLayerOrder(img.Layers, layers, nil)
		if err != nil {
			return
		}
		for _, l := range resolved {
			if !seen[l] {
				seen[l] = true
				result = append(result, l)
			}
		}
	}
	walk(imageName)
	return result
}

// AbsoluteLayerSequence returns an image's complete layer set (own + entire
// base chain) as a subsequence of the global order.
func AbsoluteLayerSequence(imageName string, images map[string]*ResolvedImage, layers map[string]*Layer, globalOrder []string) []string {
	allLayers := collectAllImageLayers(imageName, images, layers)
	layerSet := make(map[string]bool, len(allLayers))
	for _, l := range allLayers {
		layerSet[l] = true
	}

	// Filter global order to only include this image's layers
	var seq []string
	for _, l := range globalOrder {
		if layerSet[l] {
			seq = append(seq, l)
		}
	}
	return seq
}

// resolveExternalBase walks the base chain to find the ultimate external base.
func resolveExternalBase(imageName string, images map[string]*ResolvedImage) string {
	current := imageName
	for {
		img, ok := images[current]
		if !ok || img.IsExternalBase {
			if ok {
				return img.Base
			}
			return current
		}
		current = img.Base
	}
}

// ComputeIntermediates analyzes all images, builds a prefix trie of absolute
// layer sequences, creates intermediates at branching points, and returns
// updated images map with intermediates injected and existing images' Base updated.
func ComputeIntermediates(images map[string]*ResolvedImage, layers map[string]*Layer, cfg *Config, tag string) (map[string]*ResolvedImage, error) {
	// Filter to only non-disabled, non-empty-layer images that share external bases
	// Group images by their ultimate external base
	baseGroups := make(map[string][]string) // external base → image names
	for name, img := range images {
		if len(img.Layers) == 0 && img.IsExternalBase {
			// Images like "fedora" with no layers — they sit at root
		}
		extBase := resolveExternalBase(name, images)
		baseGroups[extBase] = append(baseGroups[extBase], name)
	}

	globalOrder, err := GlobalLayerOrder(images, layers)
	if err != nil {
		return nil, fmt.Errorf("computing global layer order: %w", err)
	}

	result := make(map[string]*ResolvedImage)
	// Copy all existing images
	for name, img := range images {
		cp := *img
		result[name] = &cp
	}

	// Process each base group independently
	for _, imageNames := range baseGroups {
		sortStrings(imageNames)
		if len(imageNames) <= 1 {
			continue // No branching possible with single image
		}

		// Build trie from absolute sequences
		root := newTrieNode("")

		for _, imgName := range imageNames {
			seq := AbsoluteLayerSequence(imgName, images, layers, globalOrder)
			node := root
			for _, layer := range seq {
				child, ok := node.children[layer]
				if !ok {
					child = newTrieNode(layer)
					node.children[layer] = child
				}
				node = child
			}
			node.images = append(node.images, imgName)
		}

		// Find the "root image" — an image with empty layers sitting at the trie root
		var rootImageName string
		for _, imgName := range imageNames {
			if len(images[imgName].Layers) == 0 {
				rootImageName = imgName
				break
			}
		}

		// Walk the trie to create intermediates
		startParent := rootImageName
		if startParent == "" {
			// No root image — use the external base directly
			extBase := resolveExternalBase(imageNames[0], images)
			startParent = extBase
		}

		if err := walkTrie(root, startParent, result, images, layers, cfg, tag, globalOrder); err != nil {
			return nil, err
		}
	}

	return result, nil
}

// walkTrie walks the trie from a node, creating intermediates at branching points.
// parentName is the image/base to use as parent for the next intermediate or leaf.
func walkTrie(node *trieNode, parentName string, result map[string]*ResolvedImage, origImages map[string]*ResolvedImage, layers map[string]*Layer, cfg *Config, tag string, globalOrder []string) error {
	// Follow each child path
	for _, childLayerName := range sortedKeys(node.children) {
		child := node.children[childLayerName]

		// Collect the linear chain: keep walking as long as there's exactly
		// one child and no terminal images
		var pathLayers []string
		current := child
		pathLayers = append(pathLayers, childLayerName)

		for len(current.children) == 1 && len(current.images) == 0 {
			for layerName, next := range current.children {
				pathLayers = append(pathLayers, layerName)
				current = next
			}
		}

		// current is now at a branch point (2+ children), a leaf, or has terminal images
		isBranch := len(current.children) >= 2 || (len(current.children) >= 1 && len(current.images) > 0)
		isLeaf := len(current.children) == 0

		if isBranch {
			// Need an intermediate at this point
			intermediateName := pickIntermediateName(current, pathLayers, result, origImages)

			// Check if an existing image sits exactly here
			if len(current.images) == 1 && !isExistingImageReusable(current.images[0], pathLayers, origImages, layers, parentName, result, globalOrder) {
				// Create a new intermediate
				createIntermediate(intermediateName, parentName, pathLayers, result, origImages, cfg, tag, layers, globalOrder)
				// Update the terminal image to use this intermediate as base
				updateImageBase(current.images[0], intermediateName, result)
			} else if len(current.images) == 1 {
				// Reuse existing image as intermediate
				intermediateName = current.images[0]
				updateImageBase(intermediateName, parentName, result)
				assignLayersForIntermediate(intermediateName, parentName, pathLayers, result, origImages, layers, globalOrder)
			} else if len(current.images) > 1 {
				// Multiple images at same point — create intermediate, update all
				createIntermediate(intermediateName, parentName, pathLayers, result, origImages, cfg, tag, layers, globalOrder)
				for _, imgName := range current.images {
					updateImageBase(imgName, intermediateName, result)
				}
			} else {
				// No terminal images at branch — create pure intermediate
				createIntermediate(intermediateName, parentName, pathLayers, result, origImages, cfg, tag, layers, globalOrder)
			}

			// Recurse into children
			if err := walkTrie(current, intermediateName, result, origImages, layers, cfg, tag, globalOrder); err != nil {
				return err
			}
		} else if isLeaf {
			// Terminal image(s) at leaf
			for _, imgName := range current.images {
				updateImageBase(imgName, parentName, result)
			}
		}
	}
	return nil
}

// pickIntermediateName chooses a name for an auto-intermediate.
// Uses the last layer in the path. Appends -2, -3 etc. if name conflicts.
func pickIntermediateName(node *trieNode, pathLayers []string, result map[string]*ResolvedImage, origImages map[string]*ResolvedImage) string {
	// If there's exactly one terminal image, consider reusing it
	if len(node.images) == 1 {
		return node.images[0]
	}

	baseName := pathLayers[len(pathLayers)-1]
	name := baseName
	suffix := 2
	for {
		// Check if name conflicts with an existing image or already-created intermediate
		if _, exists := origImages[name]; exists {
			name = fmt.Sprintf("%s-%d", baseName, suffix)
			suffix++
			continue
		}
		if _, exists := result[name]; exists {
			name = fmt.Sprintf("%s-%d", baseName, suffix)
			suffix++
			continue
		}
		return name
	}
}

// isExistingImageReusable checks if an existing image can serve as an intermediate
// (its full layer set matches what the trie path provides).
func isExistingImageReusable(imgName string, pathLayers []string, origImages map[string]*ResolvedImage, layers map[string]*Layer, parentName string, result map[string]*ResolvedImage, globalOrder []string) bool {
	// An existing image is reusable if it already exists in origImages
	_, exists := origImages[imgName]
	return exists
}

// createIntermediate creates an auto-generated intermediate image in the result map.
func createIntermediate(name, parentName string, pathLayers []string, result map[string]*ResolvedImage, origImages map[string]*ResolvedImage, cfg *Config, tag string, layers map[string]*Layer, globalOrder []string) {
	// Determine what layers this intermediate provides
	// These are the pathLayers that aren't already provided by the parent
	ownLayers := computeOwnLayers(parentName, pathLayers, result, layers, globalOrder)

	// Determine if parent is external or internal
	isExternalBase := false
	if _, ok := result[parentName]; !ok {
		isExternalBase = true
	}

	// Inherit settings from defaults
	img := &ResolvedImage{
		Name:           name,
		Base:           parentName,
		IsExternalBase: isExternalBase,
		Layers:         ownLayers,
		Tag:            tag,
		Registry:       cfg.Defaults.Registry,
		Pkg:            cfg.Defaults.Pkg,
		Platforms:       resolvePlatforms(cfg),
		User:           cfg.Defaults.User,
		UID:            resolveIntPtr(cfg.Defaults.UID, nil, 1000),
		GID:            resolveIntPtr(cfg.Defaults.GID, nil, 1000),
		Merge:          cfg.Defaults.Merge,
		Auto:           true,
	}
	if img.Pkg == "" {
		img.Pkg = "rpm"
	}
	if img.User == "" {
		img.User = "user"
	}
	img.Home = fmt.Sprintf("/home/%s", img.User)
	if img.Registry != "" {
		img.FullTag = fmt.Sprintf("%s/%s:%s", img.Registry, name, tag)
	} else {
		img.FullTag = fmt.Sprintf("%s:%s", name, tag)
	}

	result[name] = img
}

// computeOwnLayers determines which layers an intermediate needs to install
// (pathLayers minus what the parent already provides).
func computeOwnLayers(parentName string, pathLayers []string, result map[string]*ResolvedImage, layers map[string]*Layer, globalOrder []string) []string {
	parentProvided := make(map[string]bool)
	if parentImg, ok := result[parentName]; ok {
		provided, err := LayersProvidedByImage(parentName, result, layers)
		if err == nil {
			parentProvided = provided
		}
		_ = parentImg
	}

	var own []string
	for _, l := range pathLayers {
		if !parentProvided[l] {
			own = append(own, l)
		}
	}

	// Also include transitive dependencies of these layers that aren't parent-provided
	needed := make(map[string]bool)
	for _, l := range own {
		needed[l] = true
		addTransitiveDeps(l, layers, needed, parentProvided)
	}

	// Return in global order
	var ordered []string
	for _, l := range globalOrder {
		if needed[l] && !parentProvided[l] {
			ordered = append(ordered, l)
		}
	}
	if len(ordered) == 0 {
		return own // fallback
	}
	return ordered
}

// addTransitiveDeps adds all transitive dependencies of a layer to the needed set.
func addTransitiveDeps(layerName string, layers map[string]*Layer, needed map[string]bool, excluded map[string]bool) {
	layer, ok := layers[layerName]
	if !ok {
		return
	}
	for _, dep := range layer.Depends {
		if excluded[dep] || needed[dep] {
			continue
		}
		needed[dep] = true
		addTransitiveDeps(dep, layers, needed, excluded)
	}
}

// updateImageBase updates an image's Base to point to the given parent.
func updateImageBase(imgName, parentName string, result map[string]*ResolvedImage) {
	img, ok := result[imgName]
	if !ok {
		return
	}
	img.Base = parentName
	// Check if parent is an internal image
	if _, isInternal := result[parentName]; isInternal {
		img.IsExternalBase = false
	} else {
		img.IsExternalBase = true
	}
}

// assignLayersForIntermediate updates an existing image being reused as intermediate
// to have the correct layers for its position.
func assignLayersForIntermediate(imgName, parentName string, pathLayers []string, result map[string]*ResolvedImage, origImages map[string]*ResolvedImage, layers map[string]*Layer, globalOrder []string) {
	// The image keeps its original Layers from images.yml
	// ResolveLayerOrder with parentLayers will handle exclusion
}

// resolvePlatforms returns platforms from config defaults.
func resolvePlatforms(cfg *Config) []string {
	if len(cfg.Defaults.Platforms) > 0 {
		return cfg.Defaults.Platforms
	}
	return []string{"linux/amd64", "linux/arm64"}
}

// sortedKeys returns sorted keys from a map.
func sortedKeys(m map[string]*trieNode) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}
