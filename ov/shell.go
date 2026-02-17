package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// ShellCmd starts a bash shell in a container image
type ShellCmd struct {
	Image     string `arg:"" help:"Image name from images.yml"`
	Workspace string `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag       string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	Command   string `short:"c" help:"Command to execute instead of interactive shell"`
	GPUFlags  `embed:""`
}

func (c *ShellCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	// Resolve image to get registry, UID, GID (tag is unused here since we use --tag flag)
	resolved, err := cfg.ResolveImage(c.Image, "unused")
	if err != nil {
		return err
	}

	// Resolve workspace to absolute path
	absWorkspace, err := filepath.Abs(c.Workspace)
	if err != nil {
		return fmt.Errorf("resolving workspace path: %w", err)
	}

	info, err := os.Stat(absWorkspace)
	if err != nil {
		return fmt.Errorf("workspace path %q: %w", absWorkspace, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("workspace path %q is not a directory", absWorkspace)
	}

	layers, err := ScanLayers(dir)
	if err != nil {
		return err
	}

	volumes, err := CollectImageVolumes(cfg, layers, c.Image, resolved.Home)
	if err != nil {
		return err
	}

	gpu := ResolveGPU(c.GPUFlags.Mode())
	LogGPU(gpu)

	// Resolve runtime config
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}
	engine := rt.RunEngine

	imageRef := resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)

	if err := EnsureImage(imageRef, rt); err != nil {
		return err
	}

	args := buildShellArgs(engine, imageRef, absWorkspace, resolved.UID, resolved.GID, resolved.Ports, volumes, gpu, c.Command)

	// Find engine binary
	enginePath, err := findExecutable(EngineBinary(engine))
	if err != nil {
		return err
	}

	// Replace process with engine
	return syscall.Exec(enginePath, args, os.Environ())
}

// resolveShellImageRef builds the full image reference from registry, name, and tag.
func resolveShellImageRef(registry, name, tag string) string {
	if registry != "" {
		return fmt.Sprintf("%s/%s:%s", registry, name, tag)
	}
	return fmt.Sprintf("%s:%s", name, tag)
}

// buildShellArgs constructs the container run argument list.
func buildShellArgs(engine, imageRef, workspace string, uid, gid int, ports []string, volumes []VolumeMount, gpu bool, command string) []string {
	binary := EngineBinary(engine)
	interactive := "-it"
	if command != "" {
		interactive = "-i"
	}
	args := []string{
		binary, "run", "--rm", interactive,
		"-v", fmt.Sprintf("%s:/workspace", workspace),
		"-w", "/workspace",
		"--user", fmt.Sprintf("%d:%d", uid, gid),
	}
	if gpu {
		args = append(args, GPURunArgs(engine)...)
	}
	for _, port := range ports {
		args = append(args, "-p", localizePort(port))
	}
	for _, vol := range volumes {
		args = append(args, "-v", fmt.Sprintf("%s:%s", vol.VolumeName, vol.ContainerPath))
	}
	args = append(args, "--entrypoint", "bash", imageRef)
	if command != "" {
		args = append(args, "-c", command)
	}
	return args
}

// localizePort prefixes a port mapping with 127.0.0.1 to bind only to localhost.
// "80:8000" -> "127.0.0.1:80:8000", "8080" -> "127.0.0.1:8080:8080"
func localizePort(mapping string) string {
	if strings.Contains(mapping, ":") {
		return "127.0.0.1:" + mapping
	}
	return fmt.Sprintf("127.0.0.1:%s:%s", mapping, mapping)
}

// findExecutable locates an executable in PATH.
func findExecutable(name string) (string, error) {
	path, err := exec_LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%s not found in PATH", name)
	}
	return path, nil
}

// exec_LookPath wraps os/exec.LookPath to avoid importing os/exec in syscall code.
var exec_LookPath = defaultLookPath

func defaultLookPath(name string) (string, error) {
	pathEnv := os.Getenv("PATH")
	for _, dir := range filepath.SplitList(pathEnv) {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("executable not found: %s", name)
}
