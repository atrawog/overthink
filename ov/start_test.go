package main

import (
	"reflect"
	"testing"
)

func TestBuildStartArgs(t *testing.T) {
	args := buildStartArgs("docker", "ghcr.io/overthinkos/fedora-test:latest", "/home/user/project", nil, "ov-fedora-test", nil, false)
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "ov-fedora-test",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"ghcr.io/overthinkos/fedora-test:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsPodman(t *testing.T) {
	args := buildStartArgs("podman", "ghcr.io/overthinkos/fedora-test:latest", "/home/user/project", nil, "ov-fedora-test", nil, false)
	want := []string{
		"podman", "run", "-d", "--rm",
		"--name", "ov-fedora-test",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"ghcr.io/overthinkos/fedora-test:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(podman) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithPorts(t *testing.T) {
	args := buildStartArgs("docker", "ghcr.io/overthinkos/fedora-test:latest", "/home/user/project", []string{"9090:9090", "8080:8080"}, "ov-fedora-test", nil, false)
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "ov-fedora-test",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"-p", "127.0.0.1:9090:9090",
		"-p", "127.0.0.1:8080:8080",
		"ghcr.io/overthinkos/fedora-test:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithVolumes(t *testing.T) {
	volumes := []VolumeMount{
		{VolumeName: "ov-ollama-models", ContainerPath: "/home/user/.ollama/models"},
	}
	args := buildStartArgs("docker", "ghcr.io/overthinkos/ollama:latest", "/home/user/project", nil, "ov-ollama", volumes, false)
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "ov-ollama",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"-v", "ov-ollama-models:/home/user/.ollama/models",
		"ghcr.io/overthinkos/ollama:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithGPU(t *testing.T) {
	args := buildStartArgs("docker", "ghcr.io/overthinkos/ollama:latest", "/home/user/project", nil, "ov-ollama", nil, true)
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "ov-ollama",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--gpus", "all",
		"ghcr.io/overthinkos/ollama:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(gpu=true) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithGPUPodman(t *testing.T) {
	args := buildStartArgs("podman", "ghcr.io/overthinkos/ollama:latest", "/home/user/project", nil, "ov-ollama", nil, true)
	want := []string{
		"podman", "run", "-d", "--rm",
		"--name", "ov-ollama",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--device", "nvidia.com/gpu=all",
		"ghcr.io/overthinkos/ollama:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs(podman+gpu) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestContainerName(t *testing.T) {
	tests := []struct {
		image string
		want  string
	}{
		{"fedora-test", "ov-fedora-test"},
		{"fedora", "ov-fedora"},
		{"ubuntu", "ov-ubuntu"},
	}
	for _, tt := range tests {
		got := containerName(tt.image)
		if got != tt.want {
			t.Errorf("containerName(%q) = %q, want %q", tt.image, got, tt.want)
		}
	}
}
