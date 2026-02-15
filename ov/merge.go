package main

import (
	"archive/tar"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// MergeCmd merges small layers in a built container image
type MergeCmd struct {
	Image  string `arg:"" optional:"" help:"Image name from build.json"`
	All    bool   `long:"all" help:"Merge all images with merge.auto enabled"`
	MinMB  int    `long:"min-mb" help:"Layers smaller than this (MB) are merge candidates"`
	MaxMB  int    `long:"max-mb" help:"Maximum size of a merged layer (MB)"`
	Tag    string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	DryRun bool   `long:"dry-run" help:"Print merge plan without modifying the image"`
}

// MergeStep represents one step in the merge plan
type MergeStep struct {
	Keep   bool  // true = emit as-is, false = part of a merge group
	Layers []int // indices into the original layer list
}

const (
	defaultMinMB = 100
	defaultMaxMB = 1024
)

func (c *MergeCmd) Run() error {
	if c.Image == "" && !c.All {
		return fmt.Errorf("specify an image name or use --all")
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	if c.All {
		return c.runAll(cfg)
	}
	return c.runOne(cfg, c.Image)
}

// runAll merges all images that have merge.auto enabled.
func (c *MergeCmd) runAll(cfg *Config) error {
	images, err := cfg.ResolveAllImages("unused")
	if err != nil {
		return err
	}

	// Merge in dependency order so base images are merged before children
	order, err := ResolveImageOrder(images)
	if err != nil {
		return err
	}

	merged := 0
	for _, name := range order {
		resolved := images[name]
		if resolved.Merge == nil || !resolved.Merge.Auto {
			continue
		}
		fmt.Fprintf(os.Stderr, "\n--- %s ---\n", name)
		if err := c.runOne(cfg, name); err != nil {
			return fmt.Errorf("merging %s: %w", name, err)
		}
		merged++
	}

	if merged == 0 {
		fmt.Fprintf(os.Stderr, "No images have merge.auto enabled\n")
	}
	return nil
}

// runOne merges a single image.
func (c *MergeCmd) runOne(cfg *Config, imageName string) error {
	resolved, err := cfg.ResolveImage(imageName, "unused")
	if err != nil {
		return err
	}

	// Determine min/max: CLI flags -> build.json -> defaults
	minMB := defaultMinMB
	maxMB := defaultMaxMB
	if resolved.Merge != nil {
		if resolved.Merge.MinMB > 0 {
			minMB = resolved.Merge.MinMB
		}
		if resolved.Merge.MaxMB > 0 {
			maxMB = resolved.Merge.MaxMB
		}
	}
	if c.MinMB > 0 {
		minMB = c.MinMB
	}
	if c.MaxMB > 0 {
		maxMB = c.MaxMB
	}

	minBytes := int64(minMB) * 1024 * 1024
	maxBytes := int64(maxMB) * 1024 * 1024

	imageRef := resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)

	img, cleanup, err := loadImageFromDaemon(imageRef)
	if err != nil {
		return err
	}
	defer cleanup()

	layers, err := img.Layers()
	if err != nil {
		return fmt.Errorf("reading layers: %w", err)
	}

	sizes := make([]int64, len(layers))
	for i, layer := range layers {
		size, err := layer.Size()
		if err != nil {
			return fmt.Errorf("reading layer %d size: %w", i, err)
		}
		sizes[i] = size
	}

	steps := planMerge(sizes, minBytes, maxBytes)

	if c.DryRun {
		printMergePlan(sizes, steps)
		return nil
	}

	// Check if any merging is needed
	mergeCount := 0
	for _, step := range steps {
		if !step.Keep {
			mergeCount++
		}
	}
	if mergeCount == 0 {
		fmt.Fprintf(os.Stderr, "No layers to merge (%d layers, all >= %d MB)\n", len(layers), minMB)
		return nil
	}

	newImg, err := executeMerge(img, layers, steps)
	if err != nil {
		return err
	}

	if err := saveImageToDaemon(newImg, imageRef); err != nil {
		return err
	}

	newLayers, _ := newImg.Layers()
	fmt.Fprintf(os.Stderr, "Merged: %d layers -> %d layers\n", len(layers), len(newLayers))
	fmt.Fprintf(os.Stderr, "Saved %s\n", imageRef)
	return nil
}

// planMerge groups consecutive small layers for merging.
func planMerge(sizes []int64, minBytes, maxBytes int64) []MergeStep {
	var steps []MergeStep
	var group []int
	var groupSize int64

	flushGroup := func() {
		if len(group) >= 2 {
			steps = append(steps, MergeStep{Keep: false, Layers: group})
		} else {
			for _, idx := range group {
				steps = append(steps, MergeStep{Keep: true, Layers: []int{idx}})
			}
		}
		group = nil
		groupSize = 0
	}

	for i, size := range sizes {
		if size < minBytes {
			if groupSize+size <= maxBytes {
				group = append(group, i)
				groupSize += size
			} else {
				flushGroup()
				group = []int{i}
				groupSize = size
			}
		} else {
			flushGroup()
			steps = append(steps, MergeStep{Keep: true, Layers: []int{i}})
		}
	}
	flushGroup()

	return steps
}

// tarEntry holds a tar header and its content for deduplication.
type tarEntry struct {
	Header  *tar.Header
	Content []byte
}

// mergeLayers combines multiple layers into one, deduplicating paths (last writer wins).
func mergeLayers(layers []v1.Layer) (v1.Layer, error) {
	// Collect all entries, tracking insertion order and deduplicating by path.
	entries := make(map[string]*tarEntry)
	var order []string

	for _, layer := range layers {
		rc, err := layer.Uncompressed()
		if err != nil {
			return nil, fmt.Errorf("reading uncompressed layer: %w", err)
		}

		tr := tar.NewReader(rc)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				rc.Close()
				return nil, fmt.Errorf("reading tar entry: %w", err)
			}

			var content []byte
			if hdr.Size > 0 {
				content, err = io.ReadAll(tr)
				if err != nil {
					rc.Close()
					return nil, fmt.Errorf("reading tar content for %s: %w", hdr.Name, err)
				}
			}

			if _, seen := entries[hdr.Name]; !seen {
				order = append(order, hdr.Name)
			}
			entries[hdr.Name] = &tarEntry{Header: hdr, Content: content}
		}
		rc.Close()
	}

	// Write deduplicated entries in order.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, name := range order {
		entry := entries[name]
		if err := tw.WriteHeader(entry.Header); err != nil {
			return nil, fmt.Errorf("writing tar header for %s: %w", name, err)
		}
		if len(entry.Content) > 0 {
			if _, err := tw.Write(entry.Content); err != nil {
				return nil, fmt.Errorf("writing tar content for %s: %w", name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		return nil, fmt.Errorf("closing tar writer: %w", err)
	}

	data := buf.Bytes()
	return tarball.LayerFromOpener(func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data)), nil
	})
}

// executeMerge rebuilds the image with merged layers and aligned history.
func executeMerge(img v1.Image, layers []v1.Layer, steps []MergeStep) (v1.Image, error) {
	cfgFile, err := img.ConfigFile()
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	history := cfgFile.History

	// Map layer indices to history entries.
	// History entries with EmptyLayer=true don't correspond to actual layers.
	layerToHistory := make(map[int]int) // layer index -> history index
	layerIdx := 0
	for histIdx, h := range history {
		if !h.EmptyLayer {
			if layerIdx < len(layers) {
				layerToHistory[layerIdx] = histIdx
			}
			layerIdx++
		}
	}

	var newAddenda []mutate.Addendum

	// Process steps: for each step, emit the corresponding layer + history.
	// Also emit any empty-layer history entries that fall between steps.
	prevMaxHistIdx := -1

	for _, step := range steps {
		// Find the range of history indices covered by this step
		minHistIdx := len(history)
		maxHistIdx := -1
		for _, li := range step.Layers {
			if hi, ok := layerToHistory[li]; ok {
				if hi < minHistIdx {
					minHistIdx = hi
				}
				if hi > maxHistIdx {
					maxHistIdx = hi
				}
			}
		}

		// Emit empty-layer history entries between previous step and this one
		for hi := prevMaxHistIdx + 1; hi < minHistIdx; hi++ {
			if history[hi].EmptyLayer {
				newAddenda = append(newAddenda, mutate.Addendum{
					History: history[hi],
				})
			}
		}

		if step.Keep {
			li := step.Layers[0]
			h := v1.History{}
			if hi, ok := layerToHistory[li]; ok {
				h = history[hi]
			}
			newAddenda = append(newAddenda, mutate.Addendum{
				Layer:   layers[li],
				History: h,
			})
			// Emit empty-layer entries between this layer's history and maxHistIdx
			if hi, ok := layerToHistory[step.Layers[0]]; ok {
				for ei := hi + 1; ei <= maxHistIdx; ei++ {
					if history[ei].EmptyLayer {
						newAddenda = append(newAddenda, mutate.Addendum{
							History: history[ei],
						})
					}
				}
			}
		} else {
			// Merge group
			groupLayers := make([]v1.Layer, len(step.Layers))
			var createdByParts []string
			for i, li := range step.Layers {
				groupLayers[i] = layers[li]
				if hi, ok := layerToHistory[li]; ok {
					if history[hi].CreatedBy != "" {
						createdByParts = append(createdByParts, history[hi].CreatedBy)
					}
				}
			}

			merged, err := mergeLayers(groupLayers)
			if err != nil {
				return nil, fmt.Errorf("merging layers %v: %w", step.Layers, err)
			}

			mergedSize, _ := merged.Size()
			fmt.Fprintf(os.Stderr, "Merging layers %d-%d (%.1f MB)\n",
				step.Layers[0], step.Layers[len(step.Layers)-1],
				float64(mergedSize)/(1024*1024))

			h := v1.History{
				CreatedBy: "ov merge: " + strings.Join(createdByParts, " && "),
			}
			newAddenda = append(newAddenda, mutate.Addendum{
				Layer:   merged,
				History: h,
			})

			// Emit empty-layer history entries that fall within the merge range
			for hi := minHistIdx + 1; hi <= maxHistIdx; hi++ {
				if history[hi].EmptyLayer {
					newAddenda = append(newAddenda, mutate.Addendum{
						History: history[hi],
					})
				}
			}
		}

		if maxHistIdx > prevMaxHistIdx {
			prevMaxHistIdx = maxHistIdx
		}
	}

	// Emit any trailing empty-layer history entries
	for hi := prevMaxHistIdx + 1; hi < len(history); hi++ {
		if history[hi].EmptyLayer {
			newAddenda = append(newAddenda, mutate.Addendum{
				History: history[hi],
			})
		}
	}

	// Reconstruct image from empty base + config + layers
	newImg := empty.Image
	newImg, err = mutate.ConfigFile(newImg, cfgFile)
	if err != nil {
		return nil, fmt.Errorf("setting config: %w", err)
	}

	// Clear history and diff IDs since addenda will rebuild them
	cf, _ := newImg.ConfigFile()
	cf.History = nil
	cf.RootFS.DiffIDs = nil
	newImg, err = mutate.ConfigFile(newImg, cf)
	if err != nil {
		return nil, fmt.Errorf("clearing config history: %w", err)
	}

	newImg, err = mutate.Append(newImg, newAddenda...)
	if err != nil {
		return nil, fmt.Errorf("appending layers: %w", err)
	}

	return newImg, nil
}

// loadImageFromDaemon loads an image from the Docker daemon via docker save.
// The caller must call cleanup() when done with the image to remove the temp file.
func loadImageFromDaemon(ref string) (v1.Image, func(), error) {
	tmpFile, err := os.CreateTemp("", "ov-merge-*.tar")
	if err != nil {
		return nil, nil, fmt.Errorf("creating temp file: %w", err)
	}

	cleanup := func() { os.Remove(tmpFile.Name()) }

	cmd := exec.Command("docker", "save", ref)
	cmd.Stdout = tmpFile
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		tmpFile.Close()
		cleanup()
		return nil, nil, fmt.Errorf("docker save %s: %w", ref, err)
	}

	tmpFile.Close()

	img, err := tarball.ImageFromPath(tmpFile.Name(), nil)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("reading saved image: %w", err)
	}

	return img, cleanup, nil
}

// saveImageToDaemon saves an image to the Docker daemon via docker load.
func saveImageToDaemon(img v1.Image, ref string) error {
	tmpFile, err := os.CreateTemp("", "ov-merge-*.tar")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	tag, err := name.NewTag(ref)
	if err != nil {
		return fmt.Errorf("parsing image ref %q: %w", ref, err)
	}

	if err := tarball.WriteToFile(tmpFile.Name(), tag, img); err != nil {
		return fmt.Errorf("writing image tarball: %w", err)
	}

	cmd := exec.Command("docker", "load", "-i", tmpFile.Name())
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("docker load: %w", err)
	}

	return nil
}

// printMergePlan displays the dry-run merge plan.
func printMergePlan(sizes []int64, steps []MergeStep) {
	for _, step := range steps {
		if step.Keep {
			idx := step.Layers[0]
			fmt.Fprintf(os.Stderr, "Layer %2d: %7.1f MB  [keep]\n", idx, float64(sizes[idx])/(1024*1024))
		} else {
			var total int64
			for _, idx := range step.Layers {
				total += sizes[idx]
			}
			for i, idx := range step.Layers {
				prefix := " "
				switch {
				case i == 0:
					prefix = "\\"
				case i == len(step.Layers)-1:
					prefix = "/"
				}
				suffix := ""
				if i == len(step.Layers)-1 {
					suffix = fmt.Sprintf("  > merge (%.1f MB)", float64(total)/(1024*1024))
				}
				fmt.Fprintf(os.Stderr, "Layer %2d: %7.1f MB  %s%s\n", idx, float64(sizes[idx])/(1024*1024), prefix, suffix)
			}
		}
	}

	resultLayers := len(steps)
	fmt.Fprintf(os.Stderr, "\n%d layers -> %d layers\n", len(sizes), resultLayers)
}
