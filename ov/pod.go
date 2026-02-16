package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PodCmd groups podman quadlet subcommands
type PodCmd struct {
	Install   PodInstallCmd   `cmd:"" help:"Generate quadlet file and reload systemd"`
	Uninstall PodUninstallCmd `cmd:"" help:"Remove quadlet file and reload systemd"`
	Start     PodStartCmd     `cmd:"" help:"Start the systemd service"`
	Stop      PodStopCmd      `cmd:"" help:"Stop the systemd service"`
	Status    PodStatusCmd    `cmd:"" help:"Show service status"`
	Logs      PodLogsCmd      `cmd:"" help:"Show service logs"`
	Update    PodUpdateCmd    `cmd:"" help:"Update image and restart service"`
}

// PodInstallCmd generates a quadlet .container file and reloads systemd
type PodInstallCmd struct {
	Image     string `arg:"" help:"Image name from images.yaml"`
	Workspace string `short:"w" long:"workspace" default:"." help:"Host path to mount at /workspace (default: current directory)"`
	Tag       string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
}

func (c *PodInstallCmd) Run() error {
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

	if err := ensurePodmanImage(imageRef); err != nil {
		return err
	}

	qcfg := QuadletConfig{
		ImageName: c.Image,
		ImageRef:  imageRef,
		Workspace: absWorkspace,
		Ports:     resolved.Ports,
	}

	content := generateQuadlet(qcfg)

	qdir, err := quadletDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(qdir, 0755); err != nil {
		return fmt.Errorf("creating quadlet directory: %w", err)
	}

	qpath := filepath.Join(qdir, quadletFilename(c.Image))
	if err := os.WriteFile(qpath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing quadlet file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Wrote %s\n", qpath)

	cmd := exec.Command("systemctl", "--user", "daemon-reload")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")
	return nil
}

// PodUninstallCmd removes a quadlet file and reloads systemd
type PodUninstallCmd struct {
	Image string `arg:"" help:"Image name from images.yaml"`
}

func (c *PodUninstallCmd) Run() error {
	svc := serviceName(c.Image)

	// Best-effort stop
	stop := exec.Command("systemctl", "--user", "stop", svc)
	_ = stop.Run()

	qdir, err := quadletDir()
	if err != nil {
		return err
	}

	qpath := filepath.Join(qdir, quadletFilename(c.Image))
	if err := os.Remove(qpath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing quadlet file: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Removed %s\n", qpath)

	cmd := exec.Command("systemctl", "--user", "daemon-reload")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl daemon-reload failed: %w\n%s", err, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Reloaded systemd user daemon\n")
	return nil
}

// PodStartCmd starts the systemd service
type PodStartCmd struct {
	Image string `arg:"" help:"Image name from images.yaml"`
}

func (c *PodStartCmd) Run() error {
	svc := serviceName(c.Image)
	cmd := exec.Command("systemctl", "--user", "start", svc)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("starting %s: %w", svc, err)
	}
	fmt.Fprintf(os.Stderr, "Started %s\n", svc)
	return nil
}

// PodStopCmd stops the systemd service
type PodStopCmd struct {
	Image string `arg:"" help:"Image name from images.yaml"`
}

func (c *PodStopCmd) Run() error {
	svc := serviceName(c.Image)
	cmd := exec.Command("systemctl", "--user", "stop", svc)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("stopping %s: %w", svc, err)
	}
	fmt.Fprintf(os.Stderr, "Stopped %s\n", svc)
	return nil
}

// PodStatusCmd shows the systemd service status
type PodStatusCmd struct {
	Image string `arg:"" help:"Image name from images.yaml"`
}

func (c *PodStatusCmd) Run() error {
	svc := serviceName(c.Image)
	cmd := exec.Command("systemctl", "--user", "status", svc)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	// systemctl status exits non-zero for inactive services — not an error
	_ = cmd.Run()
	return nil
}

// PodLogsCmd shows the service logs via journalctl
type PodLogsCmd struct {
	Image  string `arg:"" help:"Image name from images.yaml"`
	Follow bool   `short:"f" long:"follow" help:"Follow log output"`
}

func (c *PodLogsCmd) Run() error {
	svc := serviceName(c.Image)
	args := []string{"--user", "-u", svc}
	if c.Follow {
		args = append(args, "-f")
	}
	cmd := exec.Command("journalctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("journalctl failed: %w", err)
	}
	return nil
}

// QuadletConfig holds the parameters for generating a quadlet .container file
type QuadletConfig struct {
	ImageName string   // image name from images.yaml (e.g. "fedora-test")
	ImageRef  string   // full image reference (e.g. "ghcr.io/atrawog/fedora-test:latest")
	Workspace string   // absolute host path to mount at /workspace
	Ports     []string // port mappings from images.yaml (e.g. ["8000:8000", "8080:8080"])
}

// generateQuadlet produces the contents of a quadlet .container file.
func generateQuadlet(cfg QuadletConfig) string {
	name := containerName(cfg.ImageName)
	var b strings.Builder

	b.WriteString(fmt.Sprintf("# %s.container (generated by ov pod install)\n", name))
	b.WriteString("[Unit]\n")
	b.WriteString(fmt.Sprintf("Description=Overthink %s\n", cfg.ImageName))
	b.WriteString("After=network-online.target\n")

	b.WriteString("\n[Container]\n")
	b.WriteString(fmt.Sprintf("Image=%s\n", cfg.ImageRef))
	b.WriteString(fmt.Sprintf("ContainerName=%s\n", name))
	b.WriteString(fmt.Sprintf("Volume=%s:/workspace\n", cfg.Workspace))
	b.WriteString("WorkingDir=/workspace\n")
	for _, port := range cfg.Ports {
		b.WriteString(fmt.Sprintf("PublishPort=%s\n", localizePort(port)))
	}
	b.WriteString("Exec=supervisord -n -c /etc/supervisord.conf\n")

	b.WriteString("\n[Service]\n")
	b.WriteString("Restart=always\n")
	b.WriteString("TimeoutStartSec=900\n")

	b.WriteString("\n[Install]\n")
	b.WriteString("WantedBy=default.target\n")

	return b.String()
}

// quadletDir returns the user-level quadlet directory.
func quadletDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".config", "containers", "systemd"), nil
}

// quadletFilename returns the quadlet filename for an image.
func quadletFilename(imageName string) string {
	return containerName(imageName) + ".container"
}

// serviceName returns the systemd service name for an image.
func serviceName(imageName string) string {
	return containerName(imageName) + ".service"
}

// PodUpdateCmd re-transfers an image from Docker to Podman and restarts the service
type PodUpdateCmd struct {
	Image string `arg:"" help:"Image name from images.yaml"`
	Tag   string `long:"tag" default:"latest" help:"Image tag to use (default: latest)"`
}

func (c *PodUpdateCmd) Run() error {
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

	imageRef := resolveShellImageRef(resolved.Registry, resolved.Name, c.Tag)

	if err := transferDockerToPodman(imageRef); err != nil {
		return err
	}

	svc := serviceName(c.Image)
	check := exec.Command("systemctl", "--user", "is-active", svc)
	if err := check.Run(); err == nil {
		fmt.Fprintf(os.Stderr, "Restarting %s\n", svc)
		restart := exec.Command("systemctl", "--user", "restart", svc)
		restart.Stdout = os.Stdout
		restart.Stderr = os.Stderr
		if err := restart.Run(); err != nil {
			return fmt.Errorf("restarting %s: %w", svc, err)
		}
		fmt.Fprintf(os.Stderr, "Restarted %s\n", svc)
	} else {
		fmt.Fprintf(os.Stderr, "Service %s is not active, skipping restart\n", svc)
	}

	return nil
}

// ensurePodmanImage checks if an image exists in podman, and if not, transfers it from Docker.
func ensurePodmanImage(imageRef string) error {
	check := exec.Command("podman", "image", "exists", imageRef)
	if err := check.Run(); err == nil {
		fmt.Fprintf(os.Stderr, "Image %s already in podman\n", imageRef)
		return nil
	}

	return transferDockerToPodman(imageRef)
}

// transferDockerToPodman transfers an image from Docker to Podman via docker save | podman load.
func transferDockerToPodman(imageRef string) error {
	// Verify image exists in Docker
	inspect := exec.Command("docker", "image", "inspect", imageRef)
	if output, err := inspect.CombinedOutput(); err != nil {
		return fmt.Errorf("image %s not found in docker or podman:\n%s", imageRef, strings.TrimSpace(string(output)))
	}

	fmt.Fprintf(os.Stderr, "Transferring %s from docker to podman\n", imageRef)

	save := exec.Command("docker", "save", imageRef)
	load := exec.Command("podman", "load")

	pipe, err := save.StdoutPipe()
	if err != nil {
		return fmt.Errorf("creating pipe: %w", err)
	}
	load.Stdin = pipe
	load.Stderr = os.Stderr

	if err := load.Start(); err != nil {
		return fmt.Errorf("starting podman load: %w", err)
	}
	if err := save.Run(); err != nil {
		return fmt.Errorf("docker save failed: %w", err)
	}
	if err := load.Wait(); err != nil {
		return fmt.Errorf("podman load failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Transferred %s to podman\n", imageRef)
	return nil
}
