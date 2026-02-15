# Overthink Build System

Composable container images from a library of layers. Built on `docker buildx bake` (HCL), `supervisord`, and `task` ([taskfile.dev](https://taskfile.dev)).

---

## Architecture Overview

Two components with a clean split:

**`ov` (Go CLI)** -- all computation. Parses `build.json`, scans `layers/`, resolves dependency graphs, validates, generates Containerfiles and HCL. Source: `ov/`. Registry inspection via go-containerregistry. Exception: `ov shell`/`ov start`/`ov stop`/`ov merge` exec into `docker run`/`docker stop`/`docker save`/`docker load` as developer conveniences (not part of the build pipeline).

**`task` (Taskfile)** -- all execution. Thin wrappers that call `ov` for generation, `docker`/`podman` for builds. No JSON parsing, no graph logic. Source: `Taskfile.yml` + `taskfiles/{Build,Run,Setup}.yml`.

**What gets generated** (`ov generate`):
- `.build/docker-bake.hcl` -- one explicit target per image, fully expanded (no HCL variables/matrices)
- `.build/<image>/Containerfile` -- one per image, unconditional `RUN` steps only
- `.build/<image>/traefik-routes.yml` -- traefik dynamic config (only for images with `route` layers)

Generation is idempotent. `.build/` is disposable and gitignored.

---

## Layer Definition

A **layer** is a directory under `layers/<name>/` that installs a single concern. It must contain at least one install file.

### Install Files (processed in this order)

| File | Runs as | Purpose |
|---|---|---|
| `rpm.list` / `deb.list` | root | System packages -- one per line, `#` comments. Inlined by `ov` at generation time. |
| `copr.repo` | root | COPR repos (`owner/project` per line). Expands to `--enable-repo` flags. Requires `rpm.list`. |
| `root.yml` | root | Custom root install logic (Taskfile). Binary downloads, system config. |
| `pixi.toml` / `pyproject.toml` / `environment.yml` | user | Python/conda packages. Multi-stage build (see Pixi section). Only one per layer. |
| `package.json` | user | npm packages -- installed globally via `npm install -g`. |
| `Cargo.toml` | user | Rust crate -- built via `cargo install --path`. Requires `src/` directory. |
| `user.yml` | user | Custom user install logic (Taskfile). Post-install config, workspace setup. |

### Additional Files

| File | Purpose |
|---|---|
| `depends` | Layer dependencies, one per line. Resolved transitively; topologically sorted. |
| `env` | Environment variables. Merged across layers, emitted as `ENV` directives. See [ENV from Layer env Files](#env-from-layer-env-files). |
| `ports` | Exposed ports, one per line (1-65535). Collected across layers, deduplicated, emitted as `EXPOSE` directives. |
| `route` | Traefik reverse proxy route. Key=value: `host=<hostname>`, `port=<backend port>`. Generates dynamic traefik config. Requires traefik layer. |
| `supervisord.conf` | Service fragment (`[program:<name>]`). Triggers supervisord assembly in images. |

### Root vs User Rule

Only `rpm.list`/`deb.list` and `root.yml` run as root. Everything else (`pixi.toml`, `package.json`, `Cargo.toml`, `user.yml`) runs as user. pixi, npm, and cargo must never run as root.

### Layer Dependencies

Layers declare dependencies via a `depends` file. The generator resolves transitively, topologically sorts, and pulls in missing dependencies automatically. Circular dependencies are a validation error. Layers already installed by a parent image (via `base` chain) are skipped.

---

## Image Definition

An **image** is a named build target in `build.json`. The current configuration:

```json
{
  "defaults": {
    "registry": "ghcr.io/atrawog",
    "tag": "auto",
    "base": "fedora:43",
    "platforms": ["linux/amd64", "linux/arm64"],
    "pkg": "rpm",
    "merge": {
      "auto": true,
      "min_mb": 256,
      "max_mb": 512
    }
  },
  "images": {
    "fedora": {
      "layers": ["pixi", "python", "nodejs", "rust", "supervisord"]
    },
    "ubuntu": {
      "base": "ubuntu:24.04",
      "layers": ["pixi", "python", "nodejs", "rust", "supervisord"],
      "pkg": "deb"
    },
    "debian": {
      "base": "debian:13",
      "layers": ["pixi", "python", "nodejs", "rust", "supervisord"],
      "pkg": "deb"
    },
    "fedora-test": {
      "base": "fedora",
      "layers": ["traefik", "testapi"],
      "ports": ["8000:8000", "8080:8080"]
    }
  }
}
```

### Inheritance Chain

Every setting resolves through: **image -> defaults -> hardcoded fallback** (first non-null wins).

| Field | Default | Description |
|---|---|---|
| `base` | `fedora:43` | External OCI image or name of another image in `build.json` |
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
| `merge` | `null` | Layer merge settings (`{"auto": true, "min_mb": 100, "max_mb": 1024}`). See [Layer Merging](#layer-merging). |

When `base` references another image in `build.json`, the generator resolves it to the full registry/tag and creates a build dependency. The referenced image must be built first.

---

## Generated Containerfile Structure

The actual order emitted by `ov/generate.go:generateContainerfile()`:

1. **Header** -- `# .build/<image>/Containerfile (generated -- do not edit)`
2. **`ARG BASE_IMAGE=<resolved base>`**
3. **Scratch stages** -- `FROM scratch AS <layer>` + `COPY layers/<layer>/ /` (one per layer)
4. **Pixi build stages** -- `FROM ghcr.io/prefix-dev/pixi:latest AS <layer>-pixi-build` (one per pixi layer). Install command varies by manifest type:
   - `pixi.toml`: `pixi install`
   - `pyproject.toml`: `pixi install --manifest-path pyproject.toml`
   - `environment.yml`: `pixi project import environment.yml && pixi install`
5. **Traefik routes stage** -- `FROM scratch AS traefik-routes` + `COPY .build/<image>/traefik-routes.yml` (only if image has layers with `route` files). Generated YAML maps hostnames to backend ports.
6. **Supervisord config stage** -- `FROM scratch AS supervisord-conf` (only if image has service layers). Gathers header + service fragments.
7. **`FROM ${BASE_IMAGE}`**
8. **Bootstrap** (external base only) -- install `task`, create user/group if not exists at configured UID/GID, set `WORKDIR`. For internal base: just `USER root`.
9. **Layer ENV** -- consolidated `ENV` directives from all layers' `env` files
10. **EXPOSE** -- deduplicated, sorted port numbers from all layers' `ports` files
11. **COPY pixi environments** -- `COPY --from=<layer>-pixi-build --chown=<UID>:<GID>` for each pixi layer
12. **COPY pixi binary** -- from first pixi build stage
13. **Per-layer steps** -- for each layer in order: rpm/deb install, root.yml, package.json, Cargo.toml, user.yml (only steps for files that exist)
14. **Supervisord assembly** -- `cat /fragments/*.conf > /etc/supervisord.conf` (if services)
15. **Traefik routes COPY** -- `COPY --from=traefik-routes /routes.yml /etc/traefik/dynamic/routes.yml` (if routes)
16. **`USER <UID>`** -- final directive (uses numeric UID, not username)
17. **`RUN bootc container lint`** -- (bootc images only)

Within per-layer steps, `USER <UID>` is emitted before the first user-mode step, and `USER root` resets after the last user-mode step for the next layer.

---

## User Resolution

Configurable via `user`, `uid`, `gid` fields in `build.json` (defaults: `"user"`, 1000, 1000).

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

## ENV from Layer env Files

Layers can include an `env` file to declare environment variables. Format:

```
# Comment
KEY=value
KEY="quoted value"
PATH+=:~/some/path
```

- `KEY=value` -- standard variable assignment (later layers override earlier)
- `PATH+=:path` -- appends to PATH (accumulates across layers)
- `~` and `$HOME` are expanded to the resolved home directory at generation time
- Setting `PATH=` directly is a validation error (use `PATH+=`)

Source: `ov/env.go`. The generator collects env configs from all layers via `writeLayerEnv()`, merges them (`MergeEnvConfigs`), expands paths (`ExpandEnvConfig`), and emits consolidated `ENV` directives.

Example (`layers/pixi/env`):
```
PIXI_CACHE_DIR=~/.cache/pixi
RATTLER_CACHE_DIR=~/.cache/rattler
PATH+=:~/.pixi/bin
PATH+=:~/.pixi/envs/default/bin
```

---

## Package Managers

### System Packages (rpm/deb)

Controlled by the `pkg` field. `ov` inlines packages from `rpm.list` or `deb.list` at generation time.

| `pkg` | List file | Install command | Cache mount |
|---|---|---|---|
| `"rpm"` | `rpm.list` | `dnf install -y` | `/var/cache/libdnf5` |
| `"deb"` | `deb.list` | `apt-get update && apt-get install -y --no-install-recommends` | `/var/cache/apt` + `/var/lib/apt` |

COPR repos (`copr.repo`): rpm-only. Each `owner/project` line becomes `--enable-repo="copr:copr.fedorainfracloud.org:<owner>:<project>"`. Ignored with warning for deb images.

### Pixi (Python/Conda)

Multi-stage build: dedicated `FROM ghcr.io/prefix-dev/pixi:latest` build stage per layer. Environment installed to `<home>/.pixi/envs/default`, then `COPY`'d into the final image. Pixi binary also copied. No rattler cache mount in the final image.

Supported manifests: `pixi.toml`, `pyproject.toml`, `environment.yml` (checked in that priority order by `layers.go:PixiManifest()`). Only one per layer.

Rules: never `pip install`, `conda install`, or `dnf install python3-*`. Pixi is the only Python package manager.

### npm

`npm install -g /ctx` from bind-mounted layer context. Installs to `$NPM_CONFIG_PREFIX` (`<home>/.npm-global/`). Requires `nodejs` layer earlier in the image.

### Cargo

`cargo install --path /ctx` from bind-mounted layer context. Binaries go to `<home>/.cargo/bin/`. Requires `rust` layer earlier (or `depends`).

---

## Cache Mounts

| Step type | Cache path | Options |
|---|---|---|
| `rpm.list`, `root.yml` (rpm) | `/var/cache/libdnf5` | `sharing=locked` |
| `deb.list`, `root.yml` (deb) | `/var/cache/apt` + `/var/lib/apt` | `sharing=locked` |
| `package.json`, `user.yml` | `<home>/.cache/npm` | `uid=<UID>,gid=<GID>` |
| `Cargo.toml` | `<home>/.cargo/registry` | `uid=<UID>,gid=<GID>` |

UID/GID in cache mounts are dynamic (from resolved image config, not hardcoded 1000). Pixi builds happen in separate stages; pixi/rattler cache dirs are set via `env` files, not cache mounts.

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
ov validate                            # Check build.json + layers, exit 0 or 1
ov inspect <image> [--format FIELD]    # Print resolved config (JSON) or single field
ov list images                         # Images from build.json
ov list layers                         # Layers from filesystem
ov list targets                        # Bake targets from generated HCL
ov list services                       # Layers with supervisord.conf
ov list routes                         # Layers with route files (host + port)
ov merge <image> [--min-mb N] [--max-mb N] [--tag TAG] [--dry-run]
                                       # Merge small layers in a built image
ov merge --all [--dry-run]             # Merge all images with merge.auto enabled
ov new layer <name>                    # Scaffold a layer directory
ov shell <image> [-w PATH] [--tag TAG] # Bash shell in a container (mounts cwd at /workspace)
ov start <image> [-w PATH] [--tag TAG] # Start service container with supervisord (detached)
ov stop <image>                        # Stop a running service container
ov version                             # Print computed CalVer tag
```

**Output conventions:** `generate`/`validate`/`new`/`merge` write to stderr. `inspect`/`list`/`version` write to stdout (pipeable). `inspect --format <field>` outputs bare value for shell substitution (`tag`, `base`, `pkg`, `registry`, `platforms`, `layers`, `ports`).

**Error handling:** validation collects all errors at once. Exit codes: 0 = success, 1 = validation/user error, 2 = internal error.

**Validation rules:** layers must have install files, `Cargo.toml` requires `src/`, `copr.repo` requires `rpm.list`, `pkg` is `"rpm"` or `"deb"`, no circular deps in layers or images, `env` files must use `PATH+=` not `PATH=`, `ports` files must contain valid port numbers (1-65535), image `ports` must be `"port"` or `"host:container"` format, `route` files must have both `host` and `port` (valid number), images with route layers must include traefik, `merge.min_mb` must be > 0, `merge.max_mb` must be >= `merge.min_mb`.

---

## Directory Structure

```
project/
+-- bin/ov                              # Built by `task build:ov` (gitignored)
+-- ov/                                 # Go module (go 1.25.6)
|   +-- go.mod                          # kong v1.14.0, go-containerregistry v0.20.7
|   +-- main.go                         # CLI (Kong)
|   +-- config.go                       # build.json parsing, inheritance resolution
|   +-- layers.go                       # Layer scanning, file detection
|   +-- env.go                          # env file parsing, merging, expansion
|   +-- graph.go                        # Topological sort (layers + images)
|   +-- generate.go                     # Containerfile + HCL generation
|   +-- validate.go                     # All validation rules
|   +-- version.go                      # CalVer computation
|   +-- scaffold.go                     # `new layer` scaffolding
|   +-- merge.go                        # `merge` command (post-build layer merging)
|   +-- registry.go                     # Remote image inspection (go-containerregistry)
|   +-- shell.go                        # `shell` command (execs docker run)
|   +-- *_test.go                       # Tests for each file
+-- .build/                             # Generated (gitignored)
|   +-- docker-bake.hcl
|   +-- <image>/Containerfile
+-- build.json                          # Configuration
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
| `pixi` | `env`, `pixi.toml` | Pixi binary + empty default environment. Sets `PIXI_CACHE_DIR`, `RATTLER_CACHE_DIR`, PATH. |
| `python` | `depends` (pixi), `pixi.toml` | Python 3.13 via pixi. |
| `nodejs` | `rpm.list`, `deb.list`, `env` | Node.js + npm. Sets `NPM_CONFIG_PREFIX`, `npm_config_cache`, PATH. |
| `rust` | `rpm.list`, `deb.list`, `env` | Rust + Cargo via system packages. Sets PATH for `~/.cargo/bin`. |
| `supervisord` | `depends` (python), `pixi.toml` | supervisor package via pixi. |
| `traefik` | `depends` (supervisord), `root.yml`, `traefik.yml`, `supervisord.conf`, `ports` | Traefik reverse proxy. Web on :8000, dashboard on :8080. Serves routes from `route` files. |
| `testapi` | `depends` (supervisord), `pixi.toml`, `app.py`, `user.yml`, `supervisord.conf`, `ports`, `route` | Minimal FastAPI test service on port 9090. Routed via `testapi.localhost`. |

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
| `task build:iso -- <image> [tag]` | Build ISO via Bootc Image Builder (bootc only) |
| `task build:qcow2 -- <image> [tag]` | Build QCOW2 VM image (bootc only) |
| `task build:raw -- <image> [tag]` | Build RAW disk image (bootc only) |
| `task run:vm -- <image> [tag]` | Run QCOW2 in QEMU |

Direct `ov` commands (`ov list images`, `ov validate`, etc.) don't need `task`.

---

## Workflows

**Add a layer:** `ov new layer <name>` -> add install files + optional `depends`/`env`/`supervisord.conf` -> add to an image in `build.json` -> `task build:local -- <image>`

**Add an image:** add entry to `build.json` -> `task build:local -- <image>`

**Layer images:** set `base` to another image name in `build.json`. The generator handles dependency ordering and tag resolution.

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
- `root.yml`: binary downloads, non-COPR repos, system config. Never `dnf clean all`.
- `user.yml`: post-install config, workspace setup. Never `sudo`.
- System packages belong in `rpm.list`/`deb.list`. COPR repos in `copr.repo`. Python in `pixi.toml`. npm in `package.json`. Rust in `Cargo.toml`.
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

Add `merge` to `build.json` defaults or per-image:

```json
{
  "defaults": {
    "merge": {
      "auto": true,
      "min_mb": 100,
      "max_mb": 1024
    }
  }
}
```

- **`auto`**: Enable automatic merging after builds via `ov merge --all` (default: false)
- **`min_mb`**: Layers smaller than this (MB) are merge candidates (default: 100)
- **`max_mb`**: Maximum size of a merged layer (MB) (default: 1024)

CLI flags (`--min-mb`, `--max-mb`) override `build.json` values. The `auto` field is only used by `ov merge --all` to select which images to merge; `ov merge <image>` always merges regardless.

### Algorithm

1. Load image from Docker daemon via `docker save` -> `tarball.ImageFromPath()`
2. Get compressed sizes via `layer.Size()`
3. Group consecutive layers where each is < `min_mb` into groups totaling <= `max_mb`
4. Single-layer "groups" are not merged (need 2+ consecutive candidates)
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

# Custom thresholds
ov merge fedora --min-mb 50 --max-mb 512

# Specific tag
ov merge fedora --tag 2026.46.1415
```

When `merge.auto` is set in `build.json` defaults, `task build:all` and `task build:local` automatically run `ov merge --all` after the build completes.

Merge is idempotent -- running again after merging shows all layers as `[keep]`.

---

## Bootc / Disk Images

Set `"bootc": true` on an image. The Containerfile ends with `RUN bootc container lint`. Package installation is identical to regular images.

Disk images (ISO, QCOW2, RAW) use Bootc Image Builder (BIB). Requires `podman` (rootful). Config files: `config/disk.toml` (QCOW2/RAW layout), `config/iso-gnome.toml` (ISO/Anaconda). The container image must be built first.

Commands: `task build:iso -- <image> [tag]`, `task build:qcow2 -- <image> [tag]`, `task build:raw -- <image> [tag]`, `task run:vm -- <image> [tag]`.
