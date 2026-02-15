package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Generator holds state for generating build artifacts
type Generator struct {
	Dir     string
	Config  *Config
	Layers  map[string]*Layer
	Tag     string
	Images  map[string]*ResolvedImage
	BuildDir string
}

// NewGenerator creates a new generator
func NewGenerator(dir string, tag string) (*Generator, error) {
	cfg, err := LoadConfig(dir)
	if err != nil {
		return nil, err
	}

	layers, err := ScanLayers(dir)
	if err != nil {
		return nil, err
	}

	if err := Validate(cfg, layers); err != nil {
		return nil, err
	}

	// Compute CalVer if tag not specified
	if tag == "" {
		tag = ComputeCalVer()
	}

	images, err := cfg.ResolveAllImages(tag)
	if err != nil {
		return nil, err
	}

	return &Generator{
		Dir:      dir,
		Config:   cfg,
		Layers:   layers,
		Tag:      tag,
		Images:   images,
		BuildDir: filepath.Join(dir, ".build"),
	}, nil
}

// Generate generates all build artifacts
func (g *Generator) Generate() error {
	// Create .build directory
	if err := os.MkdirAll(g.BuildDir, 0755); err != nil {
		return fmt.Errorf("creating .build directory: %w", err)
	}

	// Resolve image build order
	order, err := ResolveImageOrder(g.Images)
	if err != nil {
		return fmt.Errorf("resolving image order: %w", err)
	}

	// Generate Containerfile for each image
	for _, name := range order {
		if err := g.generateContainerfile(name); err != nil {
			return fmt.Errorf("generating Containerfile for %s: %w", name, err)
		}
	}

	// Generate docker-bake.hcl
	if err := g.generateBakeHCL(order); err != nil {
		return fmt.Errorf("generating docker-bake.hcl: %w", err)
	}

	return nil
}

// generateContainerfile generates a Containerfile for a single image
func (g *Generator) generateContainerfile(imageName string) error {
	img := g.Images[imageName]
	var b strings.Builder

	// Header
	b.WriteString(fmt.Sprintf("# .build/%s/Containerfile (generated -- do not edit)\n\n", imageName))

	// Resolve layer order for this image
	var parentLayers map[string]bool
	if !img.IsExternalBase {
		var err error
		parentLayers, err = LayersProvidedByImage(img.Base, g.Images, g.Layers)
		if err != nil {
			return err
		}
	}

	layerOrder, err := ResolveLayerOrder(img.Layers, g.Layers, parentLayers)
	if err != nil {
		return err
	}

	// Emit scratch stages for each layer
	for _, layerName := range layerOrder {
		b.WriteString(fmt.Sprintf("FROM scratch AS %s\n", layerName))
		b.WriteString(fmt.Sprintf("COPY layers/%s/ /\n\n", layerName))
	}

	// Check if this is a service image (has supervisord layers)
	hasServices := false
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasSupervisord {
			hasServices = true
			break
		}
	}

	// Emit supervisord config stage if needed
	if hasServices {
		b.WriteString("FROM scratch AS supervisord-conf\n")
		b.WriteString("COPY templates/supervisord.header.conf /fragments/00-header.conf\n")
		for i, layerName := range layerOrder {
			layer := g.Layers[layerName]
			if layer.HasSupervisord {
				b.WriteString(fmt.Sprintf("COPY layers/%s/supervisord.conf /fragments/%02d-%s.conf\n", layerName, i+1, layerName))
			}
		}
		b.WriteString("\n")
	}

	// Main image
	resolvedBase := g.resolveBaseImage(img)
	b.WriteString(fmt.Sprintf("ARG BASE_IMAGE=%s\n", resolvedBase))
	b.WriteString("FROM ${BASE_IMAGE}\n\n")

	// Bootstrap preamble (only for external base images)
	if img.IsExternalBase {
		g.writeBootstrap(&b, img.Pkg)
	}

	// Process each layer
	for _, layerName := range layerOrder {
		g.writeLayerSteps(&b, layerName, img.Pkg)
	}

	// Assemble supervisord config if needed
	if hasServices {
		b.WriteString("# Assemble supervisord.conf\n")
		b.WriteString("RUN --mount=type=bind,from=supervisord-conf,source=/fragments,target=/fragments \\\n")
		b.WriteString("    cat /fragments/*.conf > /etc/supervisord.conf\n\n")
	}

	// Final USER directive
	b.WriteString("USER user\n")

	// Bootc lint if applicable
	if img.Bootc {
		b.WriteString("\nRUN bootc container lint\n")
	}

	// Write to file
	imageDir := filepath.Join(g.BuildDir, imageName)
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		return err
	}

	containerfile := filepath.Join(imageDir, "Containerfile")
	return os.WriteFile(containerfile, []byte(b.String()), 0644)
}

// resolveBaseImage returns the full base image reference
func (g *Generator) resolveBaseImage(img *ResolvedImage) string {
	if img.IsExternalBase {
		return img.Base
	}
	// Internal base - resolve to full tag
	baseImg := g.Images[img.Base]
	return baseImg.FullTag
}

// writeBootstrap writes the bootstrap preamble for external base images
func (g *Generator) writeBootstrap(b *strings.Builder, pkg string) {
	b.WriteString("# Bootstrap\n")

	// Install task
	b.WriteString("RUN ")
	if pkg == "deb" {
		b.WriteString("--mount=type=cache,dst=/var/cache/apt,sharing=locked \\\n")
		b.WriteString("    --mount=type=cache,dst=/var/lib/apt,sharing=locked \\\n    ")
	} else {
		b.WriteString("--mount=type=cache,dst=/var/cache/libdnf5,sharing=locked \\\n    ")
	}
	b.WriteString("ARCH=$(uname -m) && \\\n")
	b.WriteString("    case \"$ARCH\" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac && \\\n")
	b.WriteString("    curl -fsSL \"https://github.com/go-task/task/releases/latest/download/task_linux_${ARCH}.tar.gz\" | tar -xzf - -C /usr/local/bin task\n\n")

	// Create user (skip if exists)
	b.WriteString("RUN id -u user >/dev/null 2>&1 || useradd -m -u 1000 -s /bin/bash user\n\n")

	// Environment
	b.WriteString("ENV NPM_CONFIG_PREFIX=\"/home/user/.npm-global\"\n")
	b.WriteString("ENV npm_config_cache=\"/home/user/.cache/npm\"\n")
	b.WriteString("ENV PATH=\"/home/user/.npm-global/bin:/home/user/.cargo/bin:/home/user/.pixi/envs/default/bin:${PATH}\"\n")
	b.WriteString("WORKDIR /home/user\n\n")
}

// writeLayerSteps writes the RUN steps for a single layer
func (g *Generator) writeLayerSteps(b *strings.Builder, layerName string, pkg string) {
	layer := g.Layers[layerName]

	b.WriteString(fmt.Sprintf("# Layer: %s\n", layerName))

	// Track if we've switched to user mode
	asUser := false

	// 1. rpm.list or deb.list (root)
	if pkg == "rpm" && layer.HasRpmList {
		pkgs, _ := layer.RpmPackages()
		if len(pkgs) > 0 {
			coprRepos, _ := layer.CoprRepos()
			g.writeDnfInstall(b, pkgs, coprRepos)
		}
	} else if pkg == "deb" && layer.HasDebList {
		pkgs, _ := layer.DebPackages()
		if len(pkgs) > 0 {
			g.writeAptInstall(b, pkgs)
		}
	}

	// 2. root.yml (root)
	if layer.HasRootYml {
		g.writeRootYml(b, layerName, pkg)
	}

	// 3. pixi.toml (user)
	if layer.HasPixiToml {
		if !asUser {
			b.WriteString("USER user\n")
			asUser = true
		}
		g.writePixiToml(b, layerName)
	}

	// 4. package.json (user)
	if layer.HasPackageJson {
		if !asUser {
			b.WriteString("USER user\n")
			asUser = true
		}
		g.writePackageJson(b, layerName)
	}

	// 5. Cargo.toml (user)
	if layer.HasCargoToml {
		if !asUser {
			b.WriteString("USER user\n")
			asUser = true
		}
		g.writeCargoToml(b, layerName)
	}

	// 6. user.yml (user)
	if layer.HasUserYml {
		if !asUser {
			b.WriteString("USER user\n")
			asUser = true
		}
		g.writeUserYml(b, layerName)
	}

	// Reset to root for next layer
	if asUser {
		b.WriteString("USER root\n")
	}

	b.WriteString("\n")
}

func (g *Generator) writeDnfInstall(b *strings.Builder, pkgs []string, coprRepos []string) {
	b.WriteString("RUN --mount=type=cache,dst=/var/cache/libdnf5,sharing=locked \\\n")
	b.WriteString("    dnf install")

	// Add COPR repos
	for _, repo := range coprRepos {
		// repo is "owner/project", expand to full URL
		parts := strings.SplitN(repo, "/", 2)
		if len(parts) == 2 {
			b.WriteString(fmt.Sprintf(" \\\n      --enable-repo=\"copr:copr.fedorainfracloud.org:%s:%s\"", parts[0], parts[1]))
		}
	}

	b.WriteString(" -y")
	for _, pkg := range pkgs {
		b.WriteString(fmt.Sprintf(" \\\n      %s", pkg))
	}
	b.WriteString("\n")
}

func (g *Generator) writeAptInstall(b *strings.Builder, pkgs []string) {
	b.WriteString("RUN --mount=type=cache,dst=/var/cache/apt,sharing=locked \\\n")
	b.WriteString("    --mount=type=cache,dst=/var/lib/apt,sharing=locked \\\n")
	b.WriteString("    apt-get update && apt-get install -y --no-install-recommends")
	for _, pkg := range pkgs {
		b.WriteString(fmt.Sprintf(" \\\n      %s", pkg))
	}
	b.WriteString("\n")
}

func (g *Generator) writeRootYml(b *strings.Builder, layerName string, pkg string) {
	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	if pkg == "deb" {
		b.WriteString("    --mount=type=cache,dst=/var/cache/apt,sharing=locked \\\n")
		b.WriteString("    --mount=type=cache,dst=/var/lib/apt,sharing=locked \\\n")
	} else {
		b.WriteString("    --mount=type=cache,dst=/var/cache/libdnf5,sharing=locked \\\n")
	}
	b.WriteString("    cd /ctx && task -t root.yml install\n")
}

func (g *Generator) writePixiToml(b *strings.Builder, layerName string) {
	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	b.WriteString("    --mount=type=cache,dst=/home/user/.cache/rattler,uid=1000,gid=1000 \\\n")
	b.WriteString("    cd /home/user && pixi add --manifest-path /ctx/pixi.toml\n")
}

func (g *Generator) writePackageJson(b *strings.Builder, layerName string) {
	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	b.WriteString("    --mount=type=cache,dst=/home/user/.cache/npm,uid=1000,gid=1000 \\\n")
	b.WriteString("    npm install -g /ctx\n")
}

func (g *Generator) writeCargoToml(b *strings.Builder, layerName string) {
	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	b.WriteString("    --mount=type=cache,dst=/home/user/.cargo/registry,uid=1000,gid=1000 \\\n")
	b.WriteString("    cargo install --path /ctx\n")
}

func (g *Generator) writeUserYml(b *strings.Builder, layerName string) {
	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	b.WriteString("    --mount=type=cache,dst=/home/user/.cache/npm,uid=1000,gid=1000 \\\n")
	b.WriteString("    cd /ctx && task -t user.yml install\n")
}

// generateBakeHCL generates the docker-bake.hcl file
func (g *Generator) generateBakeHCL(order []string) error {
	var b strings.Builder

	b.WriteString("# .build/docker-bake.hcl (generated -- do not edit)\n\n")

	// Group target for building all
	b.WriteString("group \"default\" {\n")
	b.WriteString("  targets = [")
	for i, name := range order {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(fmt.Sprintf("%q", name))
	}
	b.WriteString("]\n")
	b.WriteString("}\n\n")

	// Target for each image
	for _, name := range order {
		img := g.Images[name]
		b.WriteString(fmt.Sprintf("target %q {\n", name))
		b.WriteString("  context = \".\"\n")
		b.WriteString(fmt.Sprintf("  dockerfile = \".build/%s/Containerfile\"\n", name))

		// Tags
		b.WriteString("  tags = [")
		b.WriteString(fmt.Sprintf("%q", img.FullTag))
		// Add latest tag if using auto versioning
		if g.Config.Images[name].Tag == "" || g.Config.Images[name].Tag == "auto" {
			if img.Registry != "" {
				b.WriteString(fmt.Sprintf(", %q", fmt.Sprintf("%s/%s:latest", img.Registry, name)))
			} else {
				b.WriteString(fmt.Sprintf(", %q", fmt.Sprintf("%s:latest", name)))
			}
		}
		b.WriteString("]\n")

		// Platforms
		b.WriteString("  platforms = [")
		for i, p := range img.Platforms {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(fmt.Sprintf("%q", p))
		}
		b.WriteString("]\n")

		// Dependencies
		if !img.IsExternalBase {
			b.WriteString(fmt.Sprintf("  depends_on = [%q]\n", img.Base))
		}

		b.WriteString("}\n\n")
	}

	return os.WriteFile(filepath.Join(g.BuildDir, "docker-bake.hcl"), []byte(b.String()), 0644)
}
