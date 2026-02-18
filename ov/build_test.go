package main

import (
	"reflect"
	"testing"
)

func TestBuildLocalArgs(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/overthinkos/fedora:2026.46.1415", "ghcr.io/overthinkos/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "build", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:2026.46.1415",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildLocalArgsPodman(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildLocalArgs("podman",
		[]string{"ghcr.io/overthinkos/fedora:2026.46.1415"},
		"linux/arm64", "fedora", "ghcr.io/overthinkos")
	want := []string{
		"podman", "build", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:2026.46.1415",
		"--platform", "linux/arm64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(podman) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgs(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildDockerPushArgs(
		[]string{"ghcr.io/overthinkos/fedora:2026.46.1415", "ghcr.io/overthinkos/fedora:latest"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "buildx", "build", "--push", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:2026.46.1415",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64,linux/arm64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildLocalArgsWithGHACache(t *testing.T) {
	cmd := &BuildCmd{Cache: "gha"}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "build", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64",
		"--cache-from", "type=gha,scope=fedora",
		"--cache-to", "type=gha,mode=max,scope=fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(gha) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgsWithGHACache(t *testing.T) {
	cmd := &BuildCmd{Cache: "gha"}
	args := cmd.buildDockerPushArgs(
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "buildx", "build", "--push", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64,linux/arm64",
		"--cache-from", "type=gha,scope=fedora",
		"--cache-to", "type=gha,mode=max,scope=fedora",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs(gha) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildLocalArgsWithRegistryCache(t *testing.T) {
	cmd := &BuildCmd{Cache: "registry"}
	args := cmd.buildLocalArgs("docker",
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		"linux/amd64", "fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "build", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64",
		"--cache-from", "type=registry,ref=ghcr.io/overthinkos/cache:fedora",
		"--cache-to", "type=registry,ref=ghcr.io/overthinkos/cache:fedora,mode=max",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildDockerPushArgsWithRegistryCache(t *testing.T) {
	cmd := &BuildCmd{Cache: "registry"}
	args := cmd.buildDockerPushArgs(
		[]string{"ghcr.io/overthinkos/fedora:latest"},
		[]string{"linux/amd64", "linux/arm64"},
		"fedora", "ghcr.io/overthinkos")
	want := []string{
		"docker", "buildx", "build", "--push", "-f", "-",
		"-t", "ghcr.io/overthinkos/fedora:latest",
		"--platform", "linux/amd64,linux/arm64",
		"--cache-from", "type=registry,ref=ghcr.io/overthinkos/cache:fedora",
		"--cache-to", "type=registry,ref=ghcr.io/overthinkos/cache:fedora,mode=max",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildDockerPushArgs(registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildRegistryCacheNoRegistry(t *testing.T) {
	cmd := &BuildCmd{Cache: "registry"}
	args := cmd.buildLocalArgs("docker",
		[]string{"fedora:latest"},
		"linux/amd64", "fedora", "")
	want := []string{
		"docker", "build", "-f", "-",
		"-t", "fedora:latest",
		"--platform", "linux/amd64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildLocalArgs(registry, no registry) =\n  %v\nwant\n  %v", args, want)
	}
}

func TestBuildPodmanPushArgs(t *testing.T) {
	cmd := &BuildCmd{}
	args := cmd.buildPodmanPushArgs(
		[]string{"ghcr.io/overthinkos/fedora:2026.46.1415"},
		[]string{"linux/amd64", "linux/arm64"})
	want := []string{
		"podman", "build", "-f", "-",
		"--manifest", "ghcr.io/overthinkos/fedora:2026.46.1415",
		"--platform", "linux/amd64,linux/arm64",
		".",
	}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("buildPodmanPushArgs() =\n  %v\nwant\n  %v", args, want)
	}
}

func TestFilterImages(t *testing.T) {
	images := map[string]*ResolvedImage{
		"fedora": {
			Name:           "fedora",
			IsExternalBase: true,
		},
		"fedora-test": {
			Name:           "fedora-test",
			Base:           "fedora",
			IsExternalBase: false,
		},
		"ubuntu": {
			Name:           "ubuntu",
			IsExternalBase: true,
		},
	}

	order := []string{"fedora", "ubuntu", "fedora-test"}

	// Request only fedora-test — should pull in fedora as dependency
	filtered, err := filterImages(order, []string{"fedora-test"}, images)
	if err != nil {
		t.Fatalf("filterImages() error: %v", err)
	}
	want := []string{"fedora", "fedora-test"}
	if !reflect.DeepEqual(filtered, want) {
		t.Errorf("filterImages() = %v, want %v", filtered, want)
	}
}

func TestFilterImagesUnknown(t *testing.T) {
	images := map[string]*ResolvedImage{
		"fedora": {Name: "fedora", IsExternalBase: true},
	}
	_, err := filterImages([]string{"fedora"}, []string{"nonexistent"}, images)
	if err == nil {
		t.Error("expected error for unknown image")
	}
}

func TestFilterImagesIncludesBuilder(t *testing.T) {
	images := map[string]*ResolvedImage{
		"builder": {
			Name:           "builder",
			IsExternalBase: true,
		},
		"fedora": {
			Name:           "fedora",
			IsExternalBase: true,
			Builder:        "builder",
		},
		"app": {
			Name:           "app",
			Base:           "fedora",
			IsExternalBase: false,
			Builder:        "builder",
		},
	}

	order := []string{"builder", "fedora", "app"}

	// Request only app — should pull in fedora (base) and builder
	filtered, err := filterImages(order, []string{"app"}, images)
	if err != nil {
		t.Fatalf("filterImages() error: %v", err)
	}
	want := []string{"builder", "fedora", "app"}
	if !reflect.DeepEqual(filtered, want) {
		t.Errorf("filterImages() = %v, want %v", filtered, want)
	}
}

func TestHostPlatform(t *testing.T) {
	p := hostPlatform()
	// Should start with linux/
	if p != "linux/amd64" && p != "linux/arm64" {
		t.Logf("hostPlatform() = %q (non-standard arch, that's OK)", p)
	}
}
