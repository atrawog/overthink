package main

import (
	"reflect"
	"testing"
)

func TestBuildShellArgs(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/overthinkos/fedora:latest", "/home/user/project", 1000, 1000, nil, nil, false, "")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsCustomUIDGID(t *testing.T) {
	args := buildShellArgs("docker", "fedora:latest", "/tmp", 1001, 1002, nil, nil, false, "")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-v", "/tmp:/workspace",
		"-w", "/workspace",
		"--user", "1001:1002",
		"--entrypoint", "bash",
		"fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithPorts(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/overthinkos/fedora:latest", "/home/user/project", 1000, 1000, []string{"9090:9090", "8080:8080"}, nil, false, "")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--user", "1000:1000",
		"-p", "127.0.0.1:9090:9090",
		"-p", "127.0.0.1:8080:8080",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithSinglePort(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/overthinkos/fedora:latest", "/home/user/project", 1000, 1000, []string{"8080"}, nil, false, "")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--user", "1000:1000",
		"-p", "127.0.0.1:8080:8080",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/fedora:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithVolumes(t *testing.T) {
	volumes := []VolumeMount{
		{VolumeName: "ov-openclaw-data", ContainerPath: "/home/user/.openclaw"},
	}
	args := buildShellArgs("docker", "ghcr.io/overthinkos/openclaw:latest", "/home/user/project", 1000, 1000, nil, volumes, false, "")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--user", "1000:1000",
		"-v", "ov-openclaw-data:/home/user/.openclaw",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/openclaw:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithGPU(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/overthinkos/ollama:latest", "/home/user/project", 1000, 1000, nil, nil, true, "")
	want := []string{
		"docker", "run", "--rm", "-it",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--gpus", "all",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/ollama:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(gpu=true) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithGPUPodman(t *testing.T) {
	args := buildShellArgs("podman", "ghcr.io/overthinkos/ollama:latest", "/home/user/project", 1000, 1000, nil, nil, true, "")
	want := []string{
		"podman", "run", "--rm", "-it",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--device", "nvidia.com/gpu=all",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/ollama:latest",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(podman+gpu) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithoutGPU(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/overthinkos/ollama:latest", "/home/user/project", 1000, 1000, nil, nil, false, "")
	for _, arg := range args {
		if arg == "--gpus" {
			t.Error("buildShellArgs(gpu=false) should not contain --gpus")
		}
	}
}

func TestLocalizePort(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"80:8000", "127.0.0.1:80:8000"},
		{"8080:8080", "127.0.0.1:8080:8080"},
		{"8080", "127.0.0.1:8080:8080"},
		{"9090", "127.0.0.1:9090:9090"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := localizePort(tt.input)
			if got != tt.want {
				t.Errorf("localizePort(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildShellArgsWithCommand(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/overthinkos/fedora:latest", "/home/user/project", 1000, 1000, nil, nil, false, "echo hello")
	want := []string{
		"docker", "run", "--rm", "-i",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/fedora:latest",
		"-c", "echo hello",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(command) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildShellArgsWithCommandAndGPU(t *testing.T) {
	args := buildShellArgs("docker", "ghcr.io/overthinkos/ollama:latest", "/home/user/project", 1000, 1000, nil, nil, true, "nvidia-smi")
	want := []string{
		"docker", "run", "--rm", "-i",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"--user", "1000:1000",
		"--gpus", "all",
		"--entrypoint", "bash",
		"ghcr.io/overthinkos/ollama:latest",
		"-c", "nvidia-smi",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildShellArgs(command+gpu) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestResolveShellImageRef(t *testing.T) {
	tests := []struct {
		name     string
		registry string
		image    string
		tag      string
		want     string
	}{
		{
			name:     "with registry",
			registry: "ghcr.io/overthinkos",
			image:    "fedora",
			tag:      "latest",
			want:     "ghcr.io/overthinkos/fedora:latest",
		},
		{
			name:     "without registry",
			registry: "",
			image:    "fedora",
			tag:      "latest",
			want:     "fedora:latest",
		},
		{
			name:     "custom tag",
			registry: "ghcr.io/overthinkos",
			image:    "ubuntu",
			tag:      "2026.46.1415",
			want:     "ghcr.io/overthinkos/ubuntu:2026.46.1415",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveShellImageRef(tt.registry, tt.image, tt.tag)
			if got != tt.want {
				t.Errorf("resolveShellImageRef() = %q, want %q", got, tt.want)
			}
		})
	}
}
