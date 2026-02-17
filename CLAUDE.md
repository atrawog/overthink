# Overthink Build System

Compose container images from a library of fully configurable layers.
Built on `docker buildx bake` (HCL), `supervisord`, and `task` ([taskfile.dev](https://taskfile.dev)).

---

## Architecture Overview

Two components with a clean split:

**`ov` (Go CLI)** -- all computation. Parses `images.yml`, scans `layers/`, resolves dependency graphs, validates, generates Containerfiles and HCL. Source: `ov/`. Registry inspection via go-containerregistry. Exception: `ov shell`/`ov start`/`ov stop`/`ov merge`/`ov pod` exec into `docker run`/`docker stop`/`docker save`/`docker load`/`systemctl`/`journalctl` as developer conveniences (not part of the build pipeline).

**`task` (Taskfile)** -- all execution. Thin wrappers that call `ov` for generation, `docker`/`podman` for builds. No YAML parsing, no graph logic. Source: `Taskfile.yml` + `taskfiles/{Build,Run,Setup}.yml`.

**What gets generated** (`ov generate`):
- `.build/docker-bake.hcl` -- one explicit target per image, fully expanded (no HCL variables/matrices)
- `.build/<image>/Containerfile` -- one per image, unconditional `RUN` steps only
- `.build/<image>/traefik-routes.yml` -- traefik dynamic config (only for images with `route` layers)
- `.build/<image>/fragments/*.conf` -- supervisord service fragments (only for images with `service` layers)

Generation is idempotent. `.build/` is disposable and gitignored.

---

## Layer Definition

A **layer** is a directory under `layers/<name>/` that installs a single concern. It must contain at least one install file.

### Install Files (processed in this order)

| File | Runs as | Purpose |
|---|---|---|
| `layer.yml` `rpm`/`deb` | root | System packages declared in `layer.yml`. See [Layer Config](#layer-config-layeryml). |
| `root.yml` | root | Custom root install logic (Taskfile). Binary downloads, system config. |
| `pixi.toml` / `pyproject.toml` / `environment.yml` | user | Python/conda packages. Multi-stage build (see Pixi section). Only one per layer. |
| `package.json` | user | npm packages -- installed globally via `npm install -g`. |
| `Cargo.toml` | user | Rust crate -- built via `cargo install --path`. Requires `src/` directory. |
| `user.yml` | user | Custom user install logic (Taskfile). Post-install config, workspace setup. |

### Layer Config (`layer.yml`)

Optional YAML file consolidating all layer metadata. Parsed by `ov/layers.go:parseLayerYAML()`.

| Field | Type | Purpose |
|---|---|---|
| `depends` | `[]string` | Layer dependencies. Resolved transitively; topologically sorted. |
| `env` | `map[string]string` | Environment variables (`KEY: "value"`). Merged across layers, emitted as `ENV` directives. See [ENV from layer.yml](#env-from-layeryml). |
| `path_append` | `[]string` | Paths to append to `$PATH`. Accumulated across layers. |
| `ports` | `[]int` | Exposed ports (1-65535). Collected across layers, deduplicated, emitted as `EXPOSE` directives. |
| `route` | `{host: string, port: int}` | Traefik reverse proxy route. Generates dynamic traefik config. Requires traefik layer. |
| `service` | multiline string (`\|`) | Supervisord service fragment (`[program:<name>]`). Triggers supervisord assembly in images. |
| `rpm` | `RpmConfig` | RPM package config. See [System Packages](#system-packages-rpmdeb). |
| `deb` | `DebConfig` | Debian package config. See [System Packages](#system-packages-rpmdeb). |
| `volumes` | `[]VolumeYAML` | Persistent named volumes. Each entry has `name` + `path` fields. `~`/`$HOME` expanded. See [Volume Management](#volume-management). |

**`rpm` section fields:**

| Field | Type | Purpose |
|---|---|---|
| `packages` | `[]string` | Package names to install via `dnf install` |
| `copr` | `[]string` | COPR repos (`owner/project`). Enabled before install, disabled after. |
| `repos` | `[]RpmRepo` | External repos with `name`, `url`, `gpgkey` fields. Added disabled, enabled per-install. |
| `exclude` | `[]string` | `--exclude` patterns passed to dnf |
| `options` | `[]string` | Extra dnf flags (e.g. `--setopt=tsflags=noscripts`) |

**`deb` section fields:**

| Field | Type | Purpose |
|---|---|---|
| `packages` | `[]string` | Package names to install via `apt-get install` |

### Root vs User Rule

System packages in `layer.yml` and `root.yml` run as root. Everything else (`pixi.toml`, `package.json`, `Cargo.toml`, `user.yml`) runs as user. pixi, npm, and cargo must never run as root.

### Layer Dependencies

Layers declare dependencies via the `depends` field in `layer.yml`. The generator resolves transitively, topologically sorts, and pulls in missing dependencies automatically. Circular dependencies are a validation error. Layers already installed by a parent image (via `base` chain) are skipped.

---

## Image Definition

An **image** is a named build target in `images.yml`. Example configuration:

```yaml
defaults:
  registry: ghcr.io/atrawog
  tag: auto
  base: "quay.io/fedora/fedora:43"
  platforms:
    - linux/amd64
    - linux/arm64
  pkg: rpm
  merge:
    auto: true
    max_mb: 128

images:
  fedora:
    layers:
      - pixi
      - python
      - nodejs
      - rust
      - supervisord

  ubuntu:
    base: "ubuntu:24.04"
    layers:
      - pixi
      - python
      - nodejs
      - rust
      - supervisord
    pkg: deb

  fedora-test:
    base: fedora
    layers:
      - traefik
      - testapi
    ports:
      - "8000:8000"
      - "8080:8080"

  openclaw:
    base: fedora
    layers:
      - openclaw
    ports:
      - "18789:18789"
```

### Inheritance Chain

Every setting resolves through: **image -> defaults -> hardcoded fallback** (first non-null wins).

| Field | Default | Description |
|---|---|---|
| `enabled` | `true` | Set to `false` to disable (skipped by generate, validate, list) |
| `base` | `quay.io/fedora/fedora:43` | External OCI image or name of another image in `images.yml` |
| `bootc` | `false` | Adds `bootc container lint` and enables disk image builds |
| `platforms` | `["linux/amd64", "linux/arm64"]` | Target architectures |
| `tag` | `"auto"` | Image tag. `"auto"` for CalVer. |
| `registry` | `""` | Container registry prefix |
| `pkg` | `"rpm"` | System package manager: `"rpm"` or `"deb"` |
| `layers` | (required) | Layer list (image-specific, not inherited) |
| `ports` | `[]` | Runtime port mappings (`"host:container"` or `"port"`). Used by `ov shell` for `-p` flags. |
| `user` | `"user"` | Username for non-root operations |
| `uid` | `1000` | User ID |
| `gid` | `1000` | Group ID |
| `merge` | `null` | Layer merge settings (`auto: true, max_mb: 128`). See [Layer Merging](#layer-merging). |

When `base` references another image in `images.yml`, the generator resolves it to the full registry/tag and creates a build dependency. The referenced image must be built first.

---

## Generated Containerfile Structure

The actual order emitted by `ov/generate.go:generateContainerfile()`:

1. **Header** -- `# .build/<image>/Containerfile (generated -- do not edit)`
2. **`ARG BASE_IMAGE=<resolved base>`**
3. **Scratch stages** -- `FROM scratch AS <layer>` + `COPY layers/<layer>/ /` (one per layer)
4. **Pixi build stages** -- `FROM ghcr.io/prefix-dev/pixi:latest AS <layer>-pixi-build` (one per pixi layer). Install command varies by manifest type:
   - `pixi.toml`: `pixi install` (or `pixi install --frozen` if `pixi.lock` exists)
   - `pyproject.toml`: `pixi install --manifest-path pyproject.toml`
   - `environment.yml`: `pixi project import environment.yml && pixi install`
5. **npm build stages** -- `FROM node:lts-slim AS <layer>-npm-build` (one per npm layer). Parses `package.json` dependencies, installs globally to `/npm-global`.
6. **Traefik routes stage** -- `FROM scratch AS traefik-routes` + `COPY .build/<image>/traefik-routes.yml` (only if image has layers with `route` files). Generated YAML maps hostnames to backend ports.
7. **Supervisord config stage** -- `FROM scratch AS supervisord-conf` (only if image has service layers). Gathers header + service fragments from `.build/<image>/fragments/` (written at generate time from `layer.yml` `service` fields).
8. **`FROM ${BASE_IMAGE}`**
9. **Bootstrap** (external base only) -- install `task`, create user/group if not exists at configured UID/GID, set `WORKDIR`. For internal base: just `USER root`.
10. **Layer ENV** -- consolidated `ENV` directives from all layers' `layer.yml` `env` and `path_append` fields
11. **EXPOSE** -- deduplicated, sorted port numbers from all layers' `layer.yml` `ports` fields
12. **COPY pixi environments** -- `COPY --from=<layer>-pixi-build --chown=<UID>:<GID>` for each pixi layer
13. **COPY pixi binary** -- from first pixi build stage
14. **COPY npm packages** -- `COPY --from=<layer>-npm-build --chown=<UID>:<GID> /npm-global <home>/.npm-global` for each npm layer
15. **Per-layer steps** -- for each layer in order: rpm/deb install (from `layer.yml`), root.yml, Cargo.toml, user.yml (only steps for files that exist)
16. **Supervisord assembly** -- `cat /fragments/*.conf > /etc/supervisord.conf` (if services)
17. **Traefik routes COPY** -- `COPY --from=traefik-routes /routes.yml /etc/traefik/dynamic/routes.yml` (if routes)
18. **`USER <UID>`** -- final directive (uses numeric UID, not username)
19. **`RUN bootc container lint`** -- (bootc images only)

Within per-layer steps, `USER <UID>` is emitted before the first user-mode step, and `USER root` resets after the last user-mode step for the next layer.

---

## User Resolution

Configurable via `user`, `uid`, `gid` fields in `images.yml` (defaults: `"user"`, 1000, 1000).

For external base images, `ov` calls `registry.go:InspectImageUser()` which:
1. Pulls the base image via go-containerregistry
2. Extracts `/etc/passwd` from image layers (top layer first)
3. Finds user at configured UID
4. If found: overrides username, home directory, and GID (e.g., `ubuntu` with home `/home/ubuntu` for `ubuntu:24.04`)
5. If not found: uses configured defaults, bootstrap creates the user

For internal base images, user context is inherited from the parent image.

The bootstrap conditionally creates the user:
```dockerfile
RUN getent passwd <UID> >/dev/null 2>&1 || \
    (getent group <GID> >/dev/null 2>&1 || groupadd -g <GID> <user> && \
     useradd -m -u <UID> -g <GID> -s /bin/bash <user>)
```

---

## ENV from layer.yml

Layers declare environment variables in `layer.yml` using two fields:

```yaml
env:
  PIXI_CACHE_DIR: "~/.cache/pixi"
  RATTLER_CACHE_DIR: "~/.cache/rattler"

path_append:
  - "~/.pixi/bin"
  - "~/.pixi/envs/default/bin"
```

- `env` -- key-value map. Later layers override earlier for the same key.
- `path_append` -- list of paths appended to `$PATH`. Accumulated across layers.
- `~` and `$HOME` are expanded to the resolved home directory at generation time.
- Setting `PATH` directly in `env` is a validation error (use `path_append`).

Source: `ov/env.go`. The generator collects env configs from all layers via `writeLayerEnv()`, merges them (`MergeEnvConfigs`), expands paths (`ExpandEnvConfig`), and emits consolidated `ENV` directives.

---

## Package Managers

### System Packages (rpm/deb)

Controlled by the `pkg` field. Packages are declared in `layer.yml` under `rpm:` or `deb:` sections.

| `pkg` | Config section | Install command | Cache mount |
|---|---|---|---|
| `"rpm"` | `rpm.packages` | `dnf install -y` | `/var/cache/libdnf5` |
| `"deb"` | `deb.packages` | `apt-get update && apt-get install -y --no-install-recommends` | `/var/cache/apt` + `/var/lib/apt` |

**COPR repos** (`rpm.copr`): rpm-only. Each `owner/project` entry is enabled before install and disabled after. **External repos** (`rpm.repos`): added disabled via `dnf5 config-manager addrepo`, enabled per-install with `--enable-repo`. GPG keys imported if specified. **Excludes** (`rpm.exclude`): passed as `--exclude` patterns. **Options** (`rpm.options`): extra dnf flags like `--setopt=tsflags=noscripts`.

### Pixi (Python/Conda)

Multi-stage build: dedicated `FROM ghcr.io/prefix-dev/pixi:latest` build stage per layer. Environment installed to `<home>/.pixi/envs/default`, then `COPY`'d into the final image. Pixi binary also copied. No rattler cache mount in the final image.

Supported manifests: `pixi.toml`, `pyproject.toml`, `environment.yml` (checked in that priority order by `layers.go:PixiManifest()`). Only one per layer.

Rules: never `pip install`, `conda install`, or `dnf install python3-*`. Pixi is the only Python package manager.

### npm

Multi-stage build: dedicated `FROM node:lts-slim` build stage per npm layer. Packages from `package.json` `dependencies` are installed globally via `npm install -g` into `/npm-global`, then `COPY`'d into the final image at `<home>/.npm-global/`. Requires `nodejs` layer earlier in the image (for PATH/env setup).

### Cargo

`cargo install --path /ctx` from bind-mounted layer context. Binaries go to `<home>/.cargo/bin/`. Requires `rust` layer earlier (or `depends`).

---

## Cache Mounts

| Step type | Cache path | Options |
|---|---|---|
| `rpm.packages`, `root.yml` (rpm) | `/var/cache/libdnf5` | `sharing=locked` |
| `deb.packages`, `root.yml` (deb) | `/var/cache/apt` + `/var/lib/apt` | `sharing=locked` |
| `user.yml` | `<home>/.cache/npm` | `uid=<UID>,gid=<GID>` |
| `Cargo.toml` | `<home>/.cargo/registry` | `uid=<UID>,gid=<GID>` |

UID/GID in cache mounts are dynamic (from resolved image config, not hardcoded 1000). Pixi builds happen in separate stages; pixi/rattler cache dirs are set via `layer.yml` `env` fields, not cache mounts.

---

## Volume Management

Layers can declare persistent named volumes in `layer.yml`:

```yaml
volumes:
  - name: data
    path: "~/.openclaw"
```

- **Name**: lowercase alphanumeric with hyphens (`^[a-z0-9]+(-[a-z0-9]+)*$`). Must be unique within a layer.
- **Path**: container mount path. `~` and `$HOME` expanded to resolved home directory.
- **Naming convention**: Docker/podman volume names are `ov-<image>-<name>` (e.g. `ov-openclaw-data`).
- **Collection**: `CollectImageVolumes()` traverses the full image base chain (image -> base -> base's base), collecting volumes from all layers. Deduplicated by name (first declaration wins -- outermost image takes priority).
- **Integration**: volumes are automatically mounted by `ov shell`, `ov start`, and `ov pod install` via `-v <volume>:<path>` flags.

Source: `ov/volumes.go` (collection, expansion), `ov/layers.go` (`VolumeYAML` struct, `HasVolumes`, `Volumes()`).

---

## GPU Passthrough

`ov shell`, `ov start`, and `ov pod install` support GPU passthrough via `--gpu` / `--no-gpu` flags.

| Mode | Behavior |
|---|---|
| `--gpu` | Force GPU passthrough |
| `--no-gpu` | Disable GPU passthrough |
| (neither) | Auto-detect via `nvidia-smi` |

- **Docker** (`ov shell`, `ov start`): passes `--gpus all`
- **Podman quadlet** (`ov pod install`): adds `AddDevice=nvidia.com/gpu=all` to the `.container` file

Source: `ov/gpu.go`. `GPUFlags` struct is embedded in `ShellCmd`, `StartCmd`, and `PodInstallCmd`.

---

## Versioning

CalVer in semver format: `YYYY.DDD.HHMM` (year, day-of-year, UTC time). Computed once per `ov generate` invocation. Source: `ov/version.go`.

| `tag` value | Generated tag(s) | Example |
|---|---|---|
| `"auto"` | `YYYY.DDD.HHMM` + `latest` | `ghcr.io/atrawog/fedora:2026.46.1415`, `...:latest` |
| `"nightly"` | `nightly` only | No `latest` alias |
| `"1.2.3"` | `1.2.3` only | Pinned release |

Override: `ov generate --tag <value>` replaces all `"auto"` resolutions.

---

## ov CLI Reference

```
ov generate [--tag TAG]                # Write .build/ (Containerfiles + HCL)
ov validate                            # Check images.yml + layers, exit 0 or 1
ov inspect <image> [--format FIELD]    # Print resolved config (JSON) or single field
ov list images                         # Images from images.yml
ov list layers                         # Layers from filesystem
ov list targets                        # Bake targets from generated HCL
ov list services                       # Layers with service in layer.yml
ov list routes                         # Layers with route in layer.yml (host + port)
ov list volumes                        # Layers with volumes in layer.yml
ov merge <image> [--max-mb N] [--tag TAG] [--dry-run]
                                       # Merge small layers in a built image
ov merge --all [--dry-run]             # Merge all images with merge.auto enabled
ov new layer <name>                    # Scaffold a layer directory
ov shell <image> [-w PATH] [-c CMD] [--tag TAG] [--gpu|--no-gpu]
                                       # Bash shell in a container (mounts cwd at /workspace)
ov start <image> [-w PATH] [--tag TAG] [--gpu|--no-gpu]
                                       # Start service container with supervisord (detached)
ov stop <image>                        # Stop a running service container
ov pod install <image> [-w PATH] [--tag TAG] [--gpu|--no-gpu]
                                       # Generate quadlet .container file, daemon-reload
                                       # Auto-transfers image from Docker if missing in podman
ov pod update <image> [--tag TAG]      # Re-transfer image from Docker + restart service
ov pod uninstall <image>               # Remove quadlet file, daemon-reload
ov pod start <image>                   # systemctl --user start
ov pod stop <image>                    # systemctl --user stop
ov pod status <image>                  # systemctl --user status
ov pod logs <image> [-f]               # journalctl --user -u
ov version                             # Print computed CalVer tag
```

**Output conventions:** `generate`/`validate`/`new`/`merge` write to stderr. `inspect`/`list`/`version` write to stdout (pipeable). `inspect --format <field>` outputs bare value for shell substitution (`tag`, `base`, `pkg`, `registry`, `platforms`, `layers`, `ports`, `volumes`).

**Error handling:** validation collects all errors at once. Exit codes: 0 = success, 1 = validation/user error, 2 = internal error.

**Validation rules:** layers must have install files, `Cargo.toml` requires `src/`, `rpm.copr` requires `rpm.packages`, `rpm.repos` requires `rpm.packages`, `pkg` is `"rpm"` or `"deb"`, no circular deps in layers or images, `layer.yml` `env` must not set `PATH` directly (use `path_append`), `layer.yml` `ports` must be valid port numbers (1-65535), image `ports` must be `"port"` or `"host:container"` format, `layer.yml` `route` must have both `host` and `port` (valid number), images with route layers must include traefik, `merge.max_mb` must be > 0, volume names must match `^[a-z0-9]+(-[a-z0-9]+)*$`, volume entries require both `name` and `path`, duplicate volume names within a layer rejected.

---

## Directory Structure

```
project/
+-- bin/ov                              # Built by `task build:ov` (gitignored)
+-- ov/                                 # Go module (go 1.25.6)
|   +-- go.mod                          # kong v1.14.0, go-containerregistry v0.20.7
|   +-- main.go                         # CLI (Kong)
|   +-- config.go                       # images.yml parsing, inheritance resolution
|   +-- layers.go                       # Layer scanning, file detection
|   +-- env.go                          # env config merging, path expansion
|   +-- graph.go                        # Topological sort (layers + images)
|   +-- generate.go                     # Containerfile + HCL generation
|   +-- validate.go                     # All validation rules
|   +-- version.go                      # CalVer computation
|   +-- scaffold.go                     # `new layer` scaffolding
|   +-- merge.go                        # `merge` command (post-build layer merging)
|   +-- registry.go                     # Remote image inspection (go-containerregistry)
|   +-- shell.go                        # `shell` command (execs docker run)
|   +-- start.go                        # `start`/`stop` commands (docker run -d)
|   +-- pod.go                          # `pod` command (podman quadlet systemd services)
|   +-- gpu.go                          # GPU auto-detection + passthrough flags
|   +-- volumes.go                      # Named volume collection + mounting
|   +-- *_test.go                       # Tests for each file
+-- .build/                             # Generated (gitignored)
|   +-- docker-bake.hcl
|   +-- <image>/Containerfile
|   +-- <image>/fragments/*.conf        # Supervisord fragments (from layer.yml service)
+-- images.yml                          # Configuration
+-- Taskfile.yml                        # Root: includes + PATH setup
+-- taskfiles/
|   +-- Build.yml                       # ov, all, local, push, merge, iso, qcow2, raw
|   +-- Run.yml                         # container, shell, vm
|   +-- Setup.yml                       # builder, all
+-- layers/<name>/                      # Layer directories (see Layer Definition)
+-- templates/
|   +-- supervisord.header.conf
+-- config/
    +-- disk.toml                       # QCOW2/RAW disk layout
    +-- iso-gnome.toml                  # ISO installer config (GNOME/Anaconda)
```

---

## Shipped Layers

| Layer | Files | Purpose |
|---|---|---|
| `pixi` | `layer.yml` (env, path_append), `pixi.toml` | Pixi binary + empty default environment. Sets `PIXI_CACHE_DIR`, `RATTLER_CACHE_DIR`, PATH. |
| `python` | `layer.yml` (depends: pixi), `pixi.toml` | Python 3.13 via pixi. |
| `nodejs` | `layer.yml` (rpm/deb packages, env, path_append) | Node.js + npm. Sets `NPM_CONFIG_PREFIX`, `npm_config_cache`, PATH. |
| `rust` | `layer.yml` (rpm/deb packages, path_append) | Rust + Cargo via system packages. Sets PATH for `~/.cargo/bin`. |
| `supervisord` | `layer.yml` (depends: python), `pixi.toml` | supervisor package via pixi. |
| `traefik` | `layer.yml` (depends, ports, service), `root.yml`, `traefik.yml` | Traefik reverse proxy. Web on :8000, dashboard on :8080. Serves routes from `route` configs. |
| `testapi` | `layer.yml` (depends, ports, route, service), `pixi.toml`, `app.py`, `user.yml` | Minimal FastAPI test service on port 9090. Routed via `testapi.localhost`. |
| `openclaw` | `layer.yml` (depends, env, ports, volumes, service), `package.json` | OpenClaw gateway on port 18789. Persistent `data` volume at `~/.openclaw`. |

---

## Task Commands

| Command | What it does |
|---|---|
| `task setup:all` | Build `ov` + create buildx builder |
| `task build:ov` | `go build` -> `bin/ov` |
| `task build:all` | `ov generate` -> `docker buildx bake` -> `ov merge --all` |
| `task build:local -- <image>` | Build for host platform only -> `ov merge --all` |
| `task build:push` | Build and push all images |
| `task build:merge -- <image>` | Merge small layers in a built image |
| `task run:container -- <image>` | `docker run` |
| `task run:shell -- <image>` | Delegates to `ov shell` |
| `task run:pod:install -- <image>` | Install quadlet service (auto-transfers image from Docker) |
| `task run:pod:update -- <image>` | Re-transfer image from Docker, restart service |
| `task run:pod:uninstall -- <image>` | Uninstall quadlet service |
| `task run:pod:start -- <image>` | Start quadlet service |
| `task run:pod:stop -- <image>` | Stop quadlet service |
| `task run:pod:status -- <image>` | Show quadlet service status |
| `task run:pod:logs -- <image>` | Show quadlet service logs |
| `task build:iso -- <image> [tag]` | Build ISO via Bootc Image Builder (bootc only) |
| `task build:qcow2 -- <image> [tag]` | Build QCOW2 VM image (bootc only) |
| `task build:raw -- <image> [tag]` | Build RAW disk image (bootc only) |
| `task run:vm -- <image> [tag]` | Run QCOW2 in QEMU |

Direct `ov` commands (`ov list images`, `ov validate`, etc.) don't need `task`.

---

## Workflows

**Add a layer:** `ov new layer <name>` -> edit `layer.yml` (rpm/deb packages, depends, env, ports, route, service) -> add install files -> add to an image in `images.yml` -> `task build:local -- <image>`

**Add an image:** add entry to `images.yml` -> `task build:local -- <image>`

**Layer images:** set `base` to another image name in `images.yml`. The generator handles dependency ordering and tag resolution.

**Host bootstrap (first time):** requires `task`, `go`, `docker` with buildx. Run `task setup:all` then `task build:all`.

---

## Style Guide

### General Rules
- Lowercase-hyphenated names for layers and images
- No shell scripts -- Taskfiles for automation, Go for logic
- No Docker layer cleanup -- cache mounts handle it
- No cosign -- image signing is external to this build system
- `.build/` is disposable; all generated files start with `# <path> (generated -- do not edit)`

### Layer Taskfiles (root.yml / user.yml)
- Single task: `install`. No other tasks, no parameters. Idempotent.
- `root.yml`: binary downloads, post-install system config. Never `dnf clean all`.
- `user.yml`: post-install config, workspace setup. Never `sudo`.
- System packages, repos, COPR repos belong in `layer.yml` `rpm:`/`deb:` sections. Python in `pixi.toml`. npm in `package.json`. Rust in `Cargo.toml`.
- Binary downloads: detect arch with `uname -m`, map via `case`, fail on unsupported.

### Containerfile Conventions
- Pixi builds in multi-stage before `FROM base`; per-layer steps handle everything else
- `USER <UID>` (numeric) not `USER <name>` -- emitted by generator
- No conditionals in generated Containerfiles

### Task Conventions
- Tasks are thin wrappers. Complex logic belongs in `ov`.
- Every public task has `desc:`. Arguments via `{{.CLI_ARGS}}`.
- Preconditions check for required tools.

---

## Layer Merging

Post-build optimization: `ov merge` takes an already-built image, inspects Docker layer sizes, and merges consecutive small layers into fewer larger ones. No rebuild needed.

### Configuration

Add `merge` to `images.yml` defaults or per-image:

```yaml
defaults:
  merge:
    auto: true
    max_mb: 128
```

- **`auto`**: Enable automatic merging after builds via `ov merge --all` (default: false)
- **`max_mb`**: Maximum size of a merged layer (MB) (default: 128)

CLI flag `--max-mb` overrides `images.yml`. The `auto` field is only used by `ov merge --all` to select which images to merge; `ov merge <image>` always merges regardless.

### Algorithm

1. Load image from Docker daemon via `docker save` -> `tarball.ImageFromPath()`
2. Get compressed sizes via `layer.Size()`
3. Group consecutive layers into groups totaling <= `max_mb`
4. Single-layer "groups" are kept as-is (need 2+ layers to merge)
5. For each merge group: read uncompressed tarballs, deduplicate entries by path (last writer wins), write combined tar into a single new layer
6. Reconstruct image with `mutate.Append()`, preserving OCI history alignment (empty-layer entries for ENV/USER/EXPOSE kept in correct positions)
7. Save via `tarball.WriteToFile()` -> `docker load`

Source: `ov/merge.go`. Uses `docker save`/`docker load` via `os/exec` (same pattern as `shell.go`). No new Go dependencies -- uses `pkg/v1/tarball`, `pkg/v1/mutate`, `pkg/v1/empty` from go-containerregistry.

### Usage

```
# Preview what would be merged
ov merge fedora --dry-run

# Merge a single image
ov merge fedora

# Merge all images with merge.auto enabled (used by build tasks)
ov merge --all

# Custom threshold
ov merge fedora --max-mb 512

# Specific tag
ov merge fedora --tag 2026.46.1415
```

When `merge.auto` is set in `images.yml` defaults, `task build:all` and `task build:local` automatically run `ov merge --all` after the build completes.

Merge is idempotent -- running again after merging shows all layers as `[keep]`.

---

## Bootc / Disk Images

Set `"bootc": true` on an image. The Containerfile ends with `RUN bootc container lint`. Package installation is identical to regular images.

Disk images (ISO, QCOW2, RAW) use Bootc Image Builder (BIB). Requires `podman` (rootful). Config files: `config/disk.toml` (QCOW2/RAW layout), `config/iso-gnome.toml` (ISO/Anaconda). The container image must be built first.

Commands: `task build:iso -- <image> [tag]`, `task build:qcow2 -- <image> [tag]`, `task build:raw -- <image> [tag]`, `task run:vm -- <image> [tag]`.

---

## Podman Quadlets

`ov pod` manages containers as systemd user services via podman quadlet `.container` files. Unlike `ov start` (ephemeral `docker run -d`), quadlet services auto-restart on failure, integrate with `journalctl`, and can start at boot via `WantedBy=default.target`.

User-level quadlets only (`~/.config/containers/systemd/`). No root required. Requires `podman` and a systemd user session (`loginctl enable-linger <user>`).

### Image Transfer

Docker and podman have separate image stores. Images built with `docker buildx bake` exist only in Docker's store. `ov pod install` automatically detects if the image is missing from podman and transfers it via `docker save | podman load`. `ov pod update` always re-transfers (e.g., after a rebuild) and restarts the service if active.

### Workflow

```
ov pod install fedora-test -w ~/project   # Generate .container file, transfer image, daemon-reload
ov pod start fedora-test                   # systemctl --user start
ov pod status fedora-test                  # systemctl --user status
ov pod logs fedora-test -f                 # journalctl --user -u (follow)
ov pod update fedora-test                  # Re-transfer image after rebuild, restart service
ov pod stop fedora-test                    # systemctl --user stop
ov pod uninstall fedora-test               # Remove .container file, daemon-reload
```

### Generated File

`ov pod install` writes `~/.config/containers/systemd/ov-<image>.container`. The systemd service name is `ov-<image>.service`. Container name is `ov-<image>` (same as `ov start`). Ports are bound to `127.0.0.1` only. The container runs `supervisord -n -c /etc/supervisord.conf` as its entrypoint.

Source: `ov/pod.go`.
