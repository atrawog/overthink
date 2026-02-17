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
	Image     string `arg:"" help:"Image name from images.yml"`
	Workspace string `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag       string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
	GPUFlags  `embed:""`
}

func (c *StartCmd) Run() error {
	// Check run_mode — if quadlet, delegate to pod install+start
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode == "quadlet" {
		return c.runQuadlet()
	}

	return c.runDirect(rt)
}

func (c *StartCmd) runDirect(rt *ResolvedRuntime) error {
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

	engine := rt.RunEngine
	imageRef := resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)

	if err := EnsureImage(imageRef, rt); err != nil {
		return err
	}

	name := containerName(c.Image)
	args := buildStartArgs(engine, imageRef, absWorkspace, resolved.Ports, name, volumes, gpu)

	cmd := exec.Command(args[0], args[1:]...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s run failed: %w\n%s", EngineBinary(engine), err, strings.TrimSpace(string(output)))
	}

	containerID := strings.TrimSpace(string(output))
	if len(containerID) > 12 {
		containerID = containerID[:12]
	}
	fmt.Println(containerID)
	fmt.Fprintf(os.Stderr, "Started %s as %s\n", name, containerID)
	return nil
}

func (c *StartCmd) runQuadlet() error {
	// Delegate to pod install + start
	install := &PodInstallCmd{
		Image:     c.Image,
		Workspace: c.Workspace,
		Tag:       c.Tag,
		GPUFlags:  c.GPUFlags,
	}
	if err := install.Run(); err != nil {
		return err
	}
	start := &PodStartCmd{Image: c.Image}
	return start.Run()
}

// StopCmd stops a running container started by StartCmd
type StopCmd struct {
	Image string `arg:"" help:"Image name from images.yml"`
}

func (c *StopCmd) Run() error {
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	if rt.RunMode == "quadlet" {
		stop := &PodStopCmd{Image: c.Image}
		return stop.Run()
	}

	engine := EngineBinary(rt.RunEngine)
	name := containerName(c.Image)

	cmd := exec.Command(engine, "stop", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s stop failed: %w\n%s", engine, err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Stopped %s\n", name)
	return nil
}

// buildStartArgs constructs the container run argument list for detached supervisord.
func buildStartArgs(engine, imageRef, workspace string, ports []string, name string, volumes []VolumeMount, gpu bool) []string {
	binary := EngineBinary(engine)
	args := []string{
		binary, "run", "-d", "--rm",
		"--name", name,
		"-v", fmt.Sprintf("%s:/workspace", workspace),
		"-w", "/workspace",
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
	args = append(args, imageRef, "supervisord", "-n", "-c", "/etc/supervisord.conf")
	return args
}

// containerName returns the deterministic container name for an image.
func containerName(imageName string) string {
	return "ov-" + imageName
}
