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
	Image     string `arg:"" help:"Image name from images.yaml"`
	Workspace string `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag       string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
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

	imageRef := resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)
	args := buildShellArgs(imageRef, absWorkspace, resolved.UID, resolved.GID, resolved.Ports)

	// Find docker binary
	dockerPath, err := findExecutable("docker")
	if err != nil {
		return err
	}

	// Replace process with docker
	return syscall.Exec(dockerPath, args, os.Environ())
}

// resolveShellImageRef builds the full image reference from registry, name, and tag.
func resolveShellImageRef(registry, name, tag string) string {
	if registry != "" {
		return fmt.Sprintf("%s/%s:%s", registry, name, tag)
	}
	return fmt.Sprintf("%s:%s", name, tag)
}

// buildShellArgs constructs the docker run argument list.
func buildShellArgs(imageRef, workspace string, uid, gid int, ports []string) []string {
	args := []string{
		"docker", "run", "--rm", "-it",
		"-v", fmt.Sprintf("%s:/workspace", workspace),
		"-w", "/workspace",
		"--user", fmt.Sprintf("%d:%d", uid, gid),
	}
	for _, port := range ports {
		args = append(args, "-p", localizePort(port))
	}
	args = append(args, "--entrypoint", "bash", imageRef)
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
