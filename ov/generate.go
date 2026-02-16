package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Generator holds state for generating build artifacts
type Generator struct {
	Dir      string
	Config   *Config
	Layers   map[string]*Layer
	Tag      string
	Images   map[string]*ResolvedImage
	BuildDir string
}

// resolveUserContext detects existing user in base image or uses configured values
func (g *Generator) resolveUserContext(img *ResolvedImage) error {
	if !img.IsExternalBase {
		// Internal base - inherit from parent, but respect explicit overrides
		parentImg := g.Images[img.Base]
		origCfg := g.Config.Images[img.Name]

		if origCfg.User == "" {
			img.User = parentImg.User
		}
		if origCfg.UID == nil {
			img.UID = parentImg.UID
		}
		if origCfg.GID == nil {
			img.GID = parentImg.GID
		}

		// Resolve home directory
		if img.User == "root" {
			img.Home = "/root"
		} else if origCfg.User == "" && origCfg.UID == nil {
			img.Home = parentImg.Home
		} else {
			img.Home = fmt.Sprintf("/home/%s", img.User)
		}
		return nil
	}

	// External base - try to detect existing user at configured UID
	userInfo, err := InspectImageUser(img.Base, img.UID)
	if err != nil {
		// Can't inspect, use configured defaults
		return nil
	}

	if userInfo != nil {
		// Found existing user - use their info
		img.User = userInfo.Name
		img.Home = userInfo.Home
		img.GID = userInfo.GID
	}
	// else: no user found at UID, will create with configured values

	return nil
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

// cleanStaleBuildDirs removes image directories in .build/ that don't correspond
// to any enabled image. This prevents leftovers from renamed/removed images.
func (g *Generator) cleanStaleBuildDirs() error {
	entries, err := os.ReadDir(g.BuildDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if _, exists := g.Images[name]; !exists {
			path := filepath.Join(g.BuildDir, name)
			if err := os.RemoveAll(path); err != nil {
				return fmt.Errorf("removing stale dir %s: %w", path, err)
			}
			fmt.Fprintf(os.Stderr, "Removed stale build dir: .build/%s\n", name)
		}
	}
	return nil
}

// Generate generates all build artifacts
func (g *Generator) Generate() error {
	// Clean stale image directories from .build/ (leftovers from removed/renamed images)
	if err := g.cleanStaleBuildDirs(); err != nil {
		return fmt.Errorf("cleaning stale build dirs: %w", err)
	}

	// Create .build directory
	if err := os.MkdirAll(g.BuildDir, 0755); err != nil {
		return fmt.Errorf("creating .build directory: %w", err)
	}

	// Resolve image build order
	order, err := ResolveImageOrder(g.Images)
	if err != nil {
		return fmt.Errorf("resolving image order: %w", err)
	}

	// Resolve user context for each image (in order, so parents are resolved first)
	for _, name := range order {
		if err := g.resolveUserContext(g.Images[name]); err != nil {
			return fmt.Errorf("resolving user context for %s: %w", name, err)
		}
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

	// ARG for base image must come first (before any FROM)
	resolvedBase := g.resolveBaseImage(img)
	b.WriteString(fmt.Sprintf("ARG BASE_IMAGE=%s\n\n", resolvedBase))

	// Emit scratch stages for each layer
	for _, layerName := range layerOrder {
		b.WriteString(fmt.Sprintf("FROM scratch AS %s\n", layerName))
		b.WriteString(fmt.Sprintf("COPY layers/%s/ /\n\n", layerName))
	}

	// Emit per-layer pixi build stages
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		manifest := layer.PixiManifest()
		if manifest != "" {
			b.WriteString(fmt.Sprintf("FROM ghcr.io/prefix-dev/pixi:latest AS %s-pixi-build\n", layerName))
			if layer.NeedsGit() || layer.HasPypiDeps() {
				var aptPkgs []string
				if layer.HasPypiDeps() {
					aptPkgs = append(aptPkgs, "build-essential", "cmake", "ca-certificates")
				}
				if layer.NeedsGit() {
					aptPkgs = append(aptPkgs, "git")
					if !layer.HasPypiDeps() {
						aptPkgs = append(aptPkgs, "ca-certificates")
					}
				}
				// Deduplicate (ca-certificates may appear twice)
				seen := make(map[string]bool)
				var uniquePkgs []string
				for _, p := range aptPkgs {
					if !seen[p] {
						seen[p] = true
						uniquePkgs = append(uniquePkgs, p)
					}
				}
				b.WriteString(fmt.Sprintf("RUN apt-get update && apt-get install -y --no-install-recommends %s && rm -rf /var/lib/apt/lists/*\n", strings.Join(uniquePkgs, " ")))
			}
			b.WriteString(fmt.Sprintf("WORKDIR %s\n", img.Home))
			if layer.HasPixiLock {
				b.WriteString(fmt.Sprintf("COPY layers/%s/pixi.lock pixi.lock\n", layerName))
			}
			b.WriteString(fmt.Sprintf("COPY layers/%s/%s %s\n", layerName, manifest, manifest))
			if manifest == "environment.yml" {
				b.WriteString(fmt.Sprintf("RUN pixi project import %s && pixi install\n", manifest))
			} else if manifest == "pyproject.toml" {
				b.WriteString("RUN pixi install --manifest-path pyproject.toml\n")
			} else if layer.HasPixiLock {
				b.WriteString("RUN pixi install --frozen\n")
			} else {
				b.WriteString("RUN pixi install\n")
			}
			b.WriteString("\n")
		}
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

	// Check if this image has route layers
	hasRoutes := false
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasRoute {
			hasRoutes = true
			break
		}
	}

	// Generate traefik routes file and scratch stage if needed
	if hasRoutes {
		if err := g.generateTraefikRoutes(imageName, layerOrder); err != nil {
			return err
		}
		b.WriteString("FROM scratch AS traefik-routes\n")
		b.WriteString(fmt.Sprintf("COPY .build/%s/traefik-routes.yml /routes.yml\n\n", imageName))
	}

	// Emit supervisord config stage if needed
	if hasServices {
		if err := g.generateSupervisordFragments(imageName, layerOrder); err != nil {
			return err
		}
		b.WriteString("FROM scratch AS supervisord-conf\n")
		b.WriteString("COPY templates/supervisord.header.conf /fragments/00-header.conf\n")
		for i, layerName := range layerOrder {
			layer := g.Layers[layerName]
			if layer.HasSupervisord {
				b.WriteString(fmt.Sprintf("COPY .build/%s/fragments/%02d-%s.conf /fragments/%02d-%s.conf\n", imageName, i+1, layerName, i+1, layerName))
			}
		}
		b.WriteString("\n")
	}

	// Main image
	b.WriteString("FROM ${BASE_IMAGE}\n\n")

	// Bootstrap preamble (only for external base images)
	if img.IsExternalBase {
		g.writeBootstrap(&b, img)
	} else {
		// Internal base - reset to root for layer processing
		b.WriteString("USER root\n\n")
	}

	// Collect and write environment variables from layers
	g.writeLayerEnv(&b, layerOrder, img)

	// Emit EXPOSE directives for layer ports
	g.writeExpose(&b, layerOrder)

	// Copy pixi environments
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.PixiManifest() != "" {
			b.WriteString(fmt.Sprintf("# Copy pixi environment: %s\n", layerName))
			b.WriteString(fmt.Sprintf("COPY --from=%s-pixi-build --chown=%d:%d %s/.pixi/envs/default %s/.pixi/envs/default\n", layerName, img.UID, img.GID, img.Home, img.Home))
			// Also copy the binary if it's the first time or just overwrite (pixi is self-contained?)
			// Wait, the pixi binary itself:
			// "please use ghcr.io/prefix-dev/pixi:latest as the build image for all pixi build layers"
			// The final image needs the pixi binary.
			// We can copy it from the first pixi layer, or just any.
		}
	}
	
	// Ensure pixi binary is present if any pixi layer exists
	hasPixi := false
	for _, layerName := range layerOrder {
		if g.Layers[layerName].PixiManifest() != "" {
			hasPixi = true
			break
		}
	}
	if hasPixi {
		// Just take it from the first one available or explicitly from the image if we could refer to it.
		// Since we have build stages, we can pick the last one.
		// Or better, just COPY --from=ghcr.io/prefix-dev/pixi:latest if we could.
		// But we don't have that alias in the final stage context unless we define it.
		// We'll copy from the first pixi layer found.
		for _, layerName := range layerOrder {
			if g.Layers[layerName].PixiManifest() != "" {
				b.WriteString(fmt.Sprintf("COPY --from=%s-pixi-build /usr/local/bin/pixi /usr/local/bin/pixi\n\n", layerName))
				break
			}
		}
	}

	// Pre-create npm-global directory if any layer needs it (avoids duplicate mkdir per layer)
	hasNpm := false
	for _, layerName := range layerOrder {
		if g.Layers[layerName].HasPackageJson {
			hasNpm = true
			break
		}
	}
	if hasNpm {
		b.WriteString(fmt.Sprintf("RUN mkdir -p %s/.npm-global && chown -R %d:%d %s/.npm-global\n\n", img.Home, img.UID, img.GID, img.Home))
	}

	// Process each layer
	for _, layerName := range layerOrder {
		g.writeLayerSteps(&b, layerName, img)
	}

	// Assemble supervisord config if needed
	if hasServices {
		b.WriteString("# Assemble supervisord.conf\n")
		b.WriteString("RUN --mount=type=bind,from=supervisord-conf,source=/fragments,target=/fragments \\\n")
		b.WriteString("    cat /fragments/*.conf > /etc/supervisord.conf\n\n")
	}

	// Copy traefik dynamic routes if needed
	if hasRoutes {
		b.WriteString("# Traefik dynamic routes\n")
		b.WriteString("COPY --from=traefik-routes /routes.yml /etc/traefik/dynamic/routes.yml\n\n")
	}

	// Bootc lint if applicable (must run as root)
	if img.Bootc {
		b.WriteString("RUN bootc container lint\n\n")
	}

	// Final USER directive (use UID for robustness)
	b.WriteString(fmt.Sprintf("USER %d\n", img.UID))

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
	// Internal base - use stable :latest reference so Containerfile content
	// doesn't change on every generate (CalVer tag changes each minute).
	// The HCL contexts mapping redirects this to the actual target output.
	return g.stableBaseRef(img.Base)
}

// stableBaseRef returns a stable image reference for an internal base image.
// Uses :latest tag instead of CalVer to prevent unnecessary cache invalidation.
func (g *Generator) stableBaseRef(parentName string) string {
	parentImg := g.Images[parentName]
	if parentImg.Registry != "" {
		return fmt.Sprintf("%s/%s:latest", parentImg.Registry, parentName)
	}
	return fmt.Sprintf("%s:latest", parentName)
}

// writeBootstrap writes the bootstrap preamble for external base images
func (g *Generator) writeBootstrap(b *strings.Builder, img *ResolvedImage) {
	b.WriteString("# Bootstrap\n")

	// Install task
	b.WriteString("RUN ")
	if img.Pkg == "deb" {
		b.WriteString("--mount=type=cache,dst=/var/cache/apt,sharing=locked \\\n")
		b.WriteString("    --mount=type=cache,dst=/var/lib/apt,sharing=locked \\\n")
		b.WriteString("    apt-get update && apt-get install -y --no-install-recommends curl ca-certificates && \\\n    ")
	} else {
		b.WriteString("--mount=type=cache,dst=/var/cache/libdnf5,sharing=locked \\\n    ")
	}
	// Remove repos with corrupt zchunk metadata (dnf5 skip_if_unavailable doesn't handle this;
	// disabling via sed is insufficient because the shared libdnf5 cache mount retains stale metadata)
	if img.Pkg == "rpm" {
		b.WriteString("rm -f /etc/yum.repos.d/terra-mesa.repo 2>/dev/null || true && \\\n    ")
	}
	b.WriteString("{ [ -L /usr/local ] && mkdir -p \"$(readlink /usr/local)\"; mkdir -p /usr/local/bin; } && \\\n")
	b.WriteString("    ARCH=$(uname -m) && \\\n")
	b.WriteString("    case \"$ARCH\" in x86_64) ARCH=amd64;; aarch64) ARCH=arm64;; esac && \\\n")
	b.WriteString("    curl -fsSL \"https://github.com/go-task/task/releases/latest/download/task_linux_${ARCH}.tar.gz\" | tar -xzf - -C /usr/local/bin task\n\n")

	// Create user/group if they don't exist at configured UID/GID
	b.WriteString(fmt.Sprintf("RUN getent passwd %d >/dev/null 2>&1 || \\\n", img.UID))
	b.WriteString(fmt.Sprintf("    (getent group %d >/dev/null 2>&1 || groupadd -g %d %s && \\\n", img.GID, img.GID, img.User))
	b.WriteString(fmt.Sprintf("     useradd -m -u %d -g %d -s /bin/bash %s)\n\n", img.UID, img.GID, img.User))

	// WORKDIR only - ENV comes from layer env files
	b.WriteString(fmt.Sprintf("WORKDIR %s\n\n", img.Home))
}

// writeLayerEnv collects env configs from all layers and writes ENV directives
func (g *Generator) writeLayerEnv(b *strings.Builder, layerOrder []string, img *ResolvedImage) {
	var configs []*EnvConfig

	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if layer.HasEnv {
			cfg, err := layer.EnvConfig()
			if err == nil && cfg != nil {
				configs = append(configs, cfg)
			}
		}
	}

	if len(configs) == 0 {
		return
	}

	// Merge all configs
	merged := MergeEnvConfigs(configs)

	// Expand paths with home directory
	expanded := ExpandEnvConfig(merged, img.Home)

	// Write ENV directives
	if len(expanded.Vars) > 0 || len(expanded.PathAppend) > 0 {
		b.WriteString("# Layer environment variables\n")
	}

	// Sort keys for deterministic output (prevents Docker cache invalidation)
	keys := make([]string, 0, len(expanded.Vars))
	for key := range expanded.Vars {
		keys = append(keys, key)
	}
	sortStrings(keys)
	for _, key := range keys {
		b.WriteString(fmt.Sprintf("ENV %s=\"%s\"\n", key, expanded.Vars[key]))
	}

	// Append to PATH if there are path additions
	if len(expanded.PathAppend) > 0 {
		pathAdditions := strings.Join(expanded.PathAppend, ":")
		b.WriteString(fmt.Sprintf("ENV PATH=\"%s:${PATH}\"\n", pathAdditions))
	}

	if len(expanded.Vars) > 0 || len(expanded.PathAppend) > 0 {
		b.WriteString("\n")
	}
}

// writeExpose collects ports from all layers, deduplicates, sorts, and emits EXPOSE directives
func (g *Generator) writeExpose(b *strings.Builder, layerOrder []string) {
	seen := make(map[string]bool)
	var ports []string

	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if !layer.HasPorts {
			continue
		}
		layerPorts, err := layer.Ports()
		if err != nil {
			continue
		}
		for _, port := range layerPorts {
			if !seen[port] {
				seen[port] = true
				ports = append(ports, port)
			}
		}
	}

	if len(ports) == 0 {
		return
	}

	sortStrings(ports)
	b.WriteString("# Exposed ports\n")
	for _, port := range ports {
		b.WriteString(fmt.Sprintf("EXPOSE %s\n", port))
	}
	b.WriteString("\n")
}

// generateTraefikRoutes generates a traefik dynamic config YAML for route layers
func (g *Generator) generateTraefikRoutes(imageName string, layerOrder []string) error {
	var b strings.Builder

	b.WriteString("# .build/" + imageName + "/traefik-routes.yml (generated -- do not edit)\n")
	b.WriteString("http:\n")
	b.WriteString("  routers:\n")

	// Collect routes in layer order (deterministic)
	type routeEntry struct {
		name string
		cfg  *RouteConfig
	}
	var routes []routeEntry
	for _, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if !layer.HasRoute {
			continue
		}
		route, err := layer.Route()
		if err != nil || route == nil {
			continue
		}
		routes = append(routes, routeEntry{name: layerName, cfg: route})
	}

	for _, r := range routes {
		b.WriteString(fmt.Sprintf("    %s:\n", r.name))
		b.WriteString(fmt.Sprintf("      rule: \"Host(`%s`)\"\n", r.cfg.Host))
		b.WriteString(fmt.Sprintf("      service: %s\n", r.name))
		b.WriteString("      entryPoints:\n")
		b.WriteString("        - web\n")
	}

	b.WriteString("  services:\n")
	for _, r := range routes {
		b.WriteString(fmt.Sprintf("    %s:\n", r.name))
		b.WriteString("      loadBalancer:\n")
		b.WriteString("        servers:\n")
		b.WriteString(fmt.Sprintf("          - url: \"http://127.0.0.1:%s\"\n", r.cfg.Port))
	}

	imageDir := filepath.Join(g.BuildDir, imageName)
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		return err
	}

	return os.WriteFile(filepath.Join(imageDir, "traefik-routes.yml"), []byte(b.String()), 0644)
}

// generateSupervisordFragments writes service fragments from layer.yml to .build/<image>/fragments/
func (g *Generator) generateSupervisordFragments(imageName string, layerOrder []string) error {
	fragDir := filepath.Join(g.BuildDir, imageName, "fragments")
	if err := os.MkdirAll(fragDir, 0755); err != nil {
		return err
	}

	for i, layerName := range layerOrder {
		layer := g.Layers[layerName]
		if !layer.HasSupervisord {
			continue
		}
		content := layer.ServiceConf()
		if !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		fragFile := filepath.Join(fragDir, fmt.Sprintf("%02d-%s.conf", i+1, layerName))
		if err := os.WriteFile(fragFile, []byte(content), 0644); err != nil {
			return err
		}
	}
	return nil
}

// writeLayerSteps writes the RUN steps for a single layer
func (g *Generator) writeLayerSteps(b *strings.Builder, layerName string, img *ResolvedImage) {
	layer := g.Layers[layerName]

	b.WriteString(fmt.Sprintf("# Layer: %s\n", layerName))

	// Track if we've switched to user mode
	asUser := false

	// 1. rpm or deb packages from layer.yml (root)
	rpm := layer.RpmConfig()
	deb := layer.DebConfig()
	if img.Pkg == "rpm" && rpm != nil && len(rpm.Packages) > 0 {
		g.writeDnfInstall(b, rpm)
	} else if img.Pkg == "deb" && deb != nil && len(deb.Packages) > 0 {
		g.writeAptInstall(b, deb)
	}

	// 2. root.yml (root)
	if layer.HasRootYml {
		g.writeRootYml(b, layerName, img.Pkg)
	}

	// 4. package.json (user)
	if layer.HasPackageJson {
		if !asUser {
			b.WriteString(fmt.Sprintf("USER %d\n", img.UID))
			asUser = true
		}
		g.writePackageJson(b, layerName, img)
	}

	// 5. Cargo.toml (user)
	if layer.HasCargoToml {
		if !asUser {
			b.WriteString(fmt.Sprintf("USER %d\n", img.UID))
			asUser = true
		}
		g.writeCargoToml(b, layerName, img)
	}

	// 6. user.yml (user)
	if layer.HasUserYml {
		if !asUser {
			b.WriteString(fmt.Sprintf("USER %d\n", img.UID))
			asUser = true
		}
		g.writeUserYml(b, layerName, img)
	}

	// Reset to root for next layer
	if asUser {
		b.WriteString("USER root\n")
	}

	b.WriteString("\n")
}

func (g *Generator) writeDnfInstall(b *strings.Builder, rpm *RpmConfig) {
	b.WriteString("RUN --mount=type=cache,dst=/var/cache/libdnf5,sharing=locked \\\n")

	// External repos: add disabled, import GPG keys
	for _, repo := range rpm.Repos {
		b.WriteString(fmt.Sprintf("    dnf5 config-manager addrepo --from-repofile=%q 2>/dev/null || true && \\\n", repo.URL))
		b.WriteString(fmt.Sprintf("    dnf5 config-manager setopt %q && \\\n", repo.Name+".enabled=0"))
		if repo.GPGKey != "" {
			b.WriteString(fmt.Sprintf("    rpm --import %s || true && \\\n", repo.GPGKey))
		}
	}

	// COPR repos: enable first
	for _, repo := range rpm.Copr {
		b.WriteString(fmt.Sprintf("    dnf5 copr enable -y %s && \\\n", repo))
	}

	b.WriteString("    dnf install -y")

	// Extra options
	for _, opt := range rpm.Options {
		b.WriteString(fmt.Sprintf(" %s", opt))
	}

	// Enable repos for this install
	for _, repo := range rpm.Repos {
		b.WriteString(fmt.Sprintf(" --enable-repo=%q", repo.Name))
	}

	// Exclude patterns
	for _, excl := range rpm.Exclude {
		b.WriteString(fmt.Sprintf(" --exclude='%s'", excl))
	}

	// Packages
	for _, pkg := range rpm.Packages {
		b.WriteString(fmt.Sprintf(" \\\n      %s", pkg))
	}

	// Disable COPR repos after install
	for _, repo := range rpm.Copr {
		b.WriteString(fmt.Sprintf(" && \\\n    dnf5 config-manager setopt \"copr:copr.fedorainfracloud.org:%s.enabled=0\"", strings.ReplaceAll(repo, "/", ":")))
	}

	b.WriteString("\n")
}

func (g *Generator) writeAptInstall(b *strings.Builder, deb *DebConfig) {
	b.WriteString("RUN --mount=type=cache,dst=/var/cache/apt,sharing=locked \\\n")
	b.WriteString("    --mount=type=cache,dst=/var/lib/apt,sharing=locked \\\n")
	b.WriteString("    apt-get update && apt-get install -y --no-install-recommends")
	for _, pkg := range deb.Packages {
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

func (g *Generator) writePackageJson(b *strings.Builder, layerName string, img *ResolvedImage) {
	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	b.WriteString(fmt.Sprintf("    --mount=type=cache,dst=%s/.cache/npm,uid=%d,gid=%d \\\n", img.Home, img.UID, img.GID))
	b.WriteString("    npm install -g /ctx\n")
}

func (g *Generator) writeCargoToml(b *strings.Builder, layerName string, img *ResolvedImage) {
	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	b.WriteString(fmt.Sprintf("    --mount=type=cache,dst=%s/.cargo/registry,uid=%d,gid=%d \\\n", img.Home, img.UID, img.GID))
	b.WriteString("    cargo install --path /ctx\n")
}

func (g *Generator) writeUserYml(b *strings.Builder, layerName string, img *ResolvedImage) {
	b.WriteString(fmt.Sprintf("RUN --mount=type=bind,from=%s,source=/,target=/ctx \\\n", layerName))
	b.WriteString(fmt.Sprintf("    --mount=type=cache,dst=%s/.cache/npm,uid=%d,gid=%d \\\n", img.Home, img.UID, img.GID))
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
			stableRef := g.stableBaseRef(img.Base)
			b.WriteString(fmt.Sprintf("  contexts = {\n    %q = \"target:%s\"\n  }\n", stableRef, img.Base))
		}

		b.WriteString("}\n\n")
	}

	return os.WriteFile(filepath.Join(g.BuildDir, "docker-bake.hcl"), []byte(b.String()), 0644)
}
