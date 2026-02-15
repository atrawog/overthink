package main

import (
	"reflect"
	"testing"
)

func TestBuildStartArgs(t *testing.T) {
	args := buildStartArgs("ghcr.io/atrawog/fedora-test:latest", "/home/user/project", nil, "ov-fedora-test")
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "ov-fedora-test",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"ghcr.io/atrawog/fedora-test:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildStartArgsWithPorts(t *testing.T) {
	args := buildStartArgs("ghcr.io/atrawog/fedora-test:latest", "/home/user/project", []string{"9090:9090", "8080:8080"}, "ov-fedora-test")
	want := []string{
		"docker", "run", "-d", "--rm",
		"--name", "ov-fedora-test",
		"-v", "/home/user/project:/workspace",
		"-w", "/workspace",
		"-p", "127.0.0.1:9090:9090",
		"-p", "127.0.0.1:8080:8080",
		"ghcr.io/atrawog/fedora-test:latest",
		"supervisord", "-n", "-c", "/etc/supervisord.conf",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildStartArgs() =\n  %v\nwant\n  %v", args, want)
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
