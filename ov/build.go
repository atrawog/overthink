package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// BuildCmd builds container images
type BuildCmd struct {
	Images   []string `arg:"" optional:"" help:"Images to build (default: all enabled)"`
	Push     bool     `long:"push" help:"Push to registry after building"`
	Tag      string   `long:"tag" help:"Override tag (default: CalVer)"`
	Platform string   `long:"platform" help:"Target platform (default: host platform)"`
}

func (c *BuildCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	// Generate Containerfiles
	gen, err := NewGenerator(dir, c.Tag)
	if err != nil {
		return err
	}
	if err := gen.Generate(); err != nil {
		return fmt.Errorf("generating build files: %w", err)
	}

	// Resolve runtime config for build engine
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	engine := EngineBinary(rt.BuildEngine)

	// Determine build order
	order, err := ResolveImageOrder(gen.Images, gen.Layers)
	if err != nil {
		return err
	}

	// Filter to requested images (or all)
	if len(c.Images) > 0 {
		order, err = filterImages(order, c.Images, gen.Images)
		if err != nil {
			return err
		}
	}

	// Determine platform
	platform := c.Platform
	if platform == "" && !c.Push {
		platform = hostPlatform()
	}

	// Build each image in order, piping Containerfile content via stdin
	// to avoid race conditions with concurrent ov generate overwrites
	for _, name := range order {
		img := gen.Images[name]
		content := gen.Containerfiles[name]
		if err := c.buildImage(engine, dir, name, img, gen.Config, platform, rt.BuildEngine, content); err != nil {
			return fmt.Errorf("building %s: %w", name, err)
		}
	}

	// Auto-merge if enabled
	if !c.Push {
		mergeCmd := &MergeCmd{All: true, Tag: "latest"}
		if err := mergeCmd.Run(); err != nil {
			// Non-fatal: log and continue
			fmt.Fprintf(os.Stderr, "Warning: merge --all: %v\n", err)
		}
	}

	return nil
}

// buildImage builds a single image with the configured engine.
// containerfileContent is piped via stdin (-f -) to avoid race conditions
// with concurrent ov generate overwrites on disk.
func (c *BuildCmd) buildImage(engine, dir, name string, img *ResolvedImage, cfg *Config, platform, engineName, containerfileContent string) error {
	// Compute tags
	tags := []string{img.FullTag}
	origCfg := cfg.Images[name]
	if origCfg.Tag == "" || origCfg.Tag == "auto" {
		if img.Registry != "" {
			tags = append(tags, fmt.Sprintf("%s/%s:latest", img.Registry, name))
		} else {
			tags = append(tags, fmt.Sprintf("%s:latest", name))
		}
	}

	var args []string

	if c.Push {
		args = c.buildPushArgs(engine, tags, img.Platforms, engineName)
	} else {
		args = c.buildLocalArgs(engine, tags, platform)
	}

	fmt.Fprintf(os.Stderr, "\n--- Building %s ---\n", name)

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(containerfileContent)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s build failed: %w", engine, err)
	}

	return nil
}

// buildLocalArgs constructs args for a local (single-platform, load into store) build.
// Uses -f - to read the Containerfile from stdin.
func (c *BuildCmd) buildLocalArgs(engine string, tags []string, platform string) []string {
	args := []string{engine, "build", "-f", "-"}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, ".")
	return args
}

// buildPushArgs constructs args for a multi-platform push build.
func (c *BuildCmd) buildPushArgs(engine string, tags []string, platforms []string, engineName string) []string {
	if engineName == "podman" {
		return c.buildPodmanPushArgs(tags, platforms)
	}
	return c.buildDockerPushArgs(tags, platforms)
}

func (c *BuildCmd) buildDockerPushArgs(tags []string, platforms []string) []string {
	args := []string{"docker", "buildx", "build", "--push", "-f", "-"}
	for _, tag := range tags {
		args = append(args, "-t", tag)
	}
	if len(platforms) > 0 {
		args = append(args, "--platform", strings.Join(platforms, ","))
	}
	args = append(args, ".")
	return args
}

func (c *BuildCmd) buildPodmanPushArgs(tags []string, platforms []string) []string {
	// Podman uses --manifest for multi-platform builds
	args := []string{"podman", "build", "-f", "-"}
	if len(tags) > 0 {
		args = append(args, "--manifest", tags[0])
	}
	if len(platforms) > 0 {
		args = append(args, "--platform", strings.Join(platforms, ","))
	}
	args = append(args, ".")
	return args
}

// hostPlatform returns the host platform in OCI format.
func hostPlatform() string {
	arch := runtime.GOARCH
	return "linux/" + arch
}

// filterImages filters the build order to only include the requested images
// and their dependencies.
func filterImages(order []string, requested []string, images map[string]*ResolvedImage) ([]string, error) {
	// Validate requested images exist
	for _, name := range requested {
		if _, ok := images[name]; !ok {
			return nil, fmt.Errorf("unknown image %q", name)
		}
	}

	// Collect requested images and their transitive base + builder dependencies
	needed := make(map[string]bool)
	var addDeps func(name string)
	addDeps = func(name string) {
		if needed[name] {
			return
		}
		needed[name] = true
		img := images[name]
		if !img.IsExternalBase {
			addDeps(img.Base)
		}
		if img.Builder != "" && img.Builder != name {
			if _, ok := images[img.Builder]; ok {
				addDeps(img.Builder)
			}
		}
	}
	for _, name := range requested {
		addDeps(name)
	}

	// Filter order preserving dependency order
	var filtered []string
	for _, name := range order {
		if needed[name] {
			filtered = append(filtered, name)
		}
	}
	return filtered, nil
}
