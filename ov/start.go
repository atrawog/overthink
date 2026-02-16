package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StartCmd launches a container with supervisord in the background
type StartCmd struct {
	Image     string `arg:"" help:"Image name from images.yaml"`
	Workspace string `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag       string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
}

func (c *StartCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	resolved, err := cfg.ResolveImage(c.Image, "unused")
	if err != nil {
		return err
	}

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
	name := containerName(c.Image)
	args := buildStartArgs(imageRef, absWorkspace, resolved.Ports, name)

	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker run failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	containerID := strings.TrimSpace(string(output))
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}
	fmt.Println(containerID)
	fmt.Fprintf(os.Stderr, "Started %s as %s\n", name, containerID)
	return nil
}

// StopCmd stops a running container started by StartCmd
type StopCmd struct {
	Image string `arg:"" help:"Image name from images.yaml"`
}

func (c *StopCmd) Run() error {
	name := containerName(c.Image)

	cmd := exec.Command("docker", "stop", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker stop failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Stopped %s\n", name)
	return nil
}

// buildStartArgs constructs the docker run argument list for detached supervisord.
func buildStartArgs(imageRef, workspace string, ports []string, name string) []string {
	args := []string{
		"docker", "run", "-d", "--rm",
		"--name", name,
		"-v", fmt.Sprintf("%s:/workspace", workspace),
		"-w", "/workspace",
	}
	for _, port := range ports {
		args = append(args, "-p", localizePort(port))
	}
	args = append(args, imageRef, "supervisord", "-n", "-c", "/etc/supervisord.conf")
	return args
}

// containerName returns the deterministic container name for an image.
func containerName(imageName string) string {
	return "ov-" + imageName
}
