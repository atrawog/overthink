package main

import (
	"os"
	"strings"
	"testing"
)

func TestResolveBaseImage_InternalUseCalVer(t *testing.T) {
	g := &Generator{
		Images: map[string]*ResolvedImage{
			"fedora": {
				Name:           "fedora",
				Base:           "quay.io/fedora/fedora:43",
				IsExternalBase: true,
				Registry:       "ghcr.io/overthinkos",
				Tag:            "2026.46.1415",
				FullTag:        "ghcr.io/overthinkos/fedora:2026.46.1415",
			},
			"fedora-test": {
				Name:           "fedora-test",
				Base:           "fedora",
				IsExternalBase: false,
				Registry:       "ghcr.io/overthinkos",
				Tag:            "2026.46.1415",
				FullTag:        "ghcr.io/overthinkos/fedora-test:2026.46.1415",
			},
		},
	}

	// External base should return the base as-is
	got := g.resolveBaseImage(g.Images["fedora"])
	if got != "quay.io/fedora/fedora:43" {
		t.Errorf("resolveBaseImage(fedora) = %q, want external base", got)
	}

	// Internal base should return the parent's full CalVer tag
	got = g.resolveBaseImage(g.Images["fedora-test"])
	want := "ghcr.io/overthinkos/fedora:2026.46.1415"
	if got != want {
		t.Errorf("resolveBaseImage(fedora-test) = %q, want %q", got, want)
	}
}

func TestGenerateTraefikRoutes(t *testing.T) {
	tmpDir := t.TempDir()

	g := &Generator{
		BuildDir: tmpDir,
		Layers: map[string]*Layer{
			"traefik": {
				Name:       "traefik",
				HasRootYml: true,
			},
			"svc": {
				Name:     "svc",
				HasRoute: true,
				HasUserYml: true,
				route:    &RouteConfig{Host: "svc.localhost", Port: "9090"},
			},
		},
	}

	err := g.generateTraefikRoutes("test-image", []string{"traefik", "svc"})
	if err != nil {
		t.Fatalf("generateTraefikRoutes() error = %v", err)
	}

	data, err := os.ReadFile(tmpDir + "/test-image/traefik-routes.yml")
	if err != nil {
		t.Fatalf("reading generated routes YAML: %v", err)
	}
	yaml := string(data)

	// Check structure
	if !strings.Contains(yaml, "http:") {
		t.Error("missing http: key")
	}
	if !strings.Contains(yaml, "routers:") {
		t.Error("missing routers: key")
	}
	if !strings.Contains(yaml, "services:") {
		t.Error("missing services: key")
	}

	// Check route entry
	if !strings.Contains(yaml, "svc:") {
		t.Error("missing svc router/service entry")
	}
	if !strings.Contains(yaml, `Host(`+"`"+`svc.localhost`+"`"+`)`) {
		t.Error("missing Host rule")
	}
	if !strings.Contains(yaml, "http://127.0.0.1:9090") {
		t.Error("missing backend URL")
	}
	if !strings.Contains(yaml, "- web") {
		t.Error("missing entryPoints web")
	}
}

func TestGenerateSupervisordFragments(t *testing.T) {
	tmpDir := t.TempDir()

	g := &Generator{
		BuildDir: tmpDir,
		Layers: map[string]*Layer{
			"python": {
				Name:       "python",
				HasRootYml: true,
			},
			"svc": {
				Name:           "svc",
				HasSupervisord: true,
				HasUserYml:     true,
				serviceConf:    "[program:svc]\ncommand=svc serve\nautostart=true\n",
			},
			"other": {
				Name:           "other",
				HasSupervisord: true,
				HasUserYml:     true,
				serviceConf:    "[program:other]\ncommand=other run",
			},
		},
	}

	err := g.generateSupervisordFragments("test-image", []string{"python", "svc", "other"})
	if err != nil {
		t.Fatalf("generateSupervisordFragments() error = %v", err)
	}

	// svc fragment should be at position 02 (index 1 + 1)
	data, err := os.ReadFile(tmpDir + "/test-image/fragments/02-svc.conf")
	if err != nil {
		t.Fatalf("reading svc fragment: %v", err)
	}
	if !strings.Contains(string(data), "[program:svc]") {
		t.Error("svc fragment should contain [program:svc]")
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("fragment should end with newline")
	}

	// other fragment should be at position 03
	data, err = os.ReadFile(tmpDir + "/test-image/fragments/03-other.conf")
	if err != nil {
		t.Fatalf("reading other fragment: %v", err)
	}
	if !strings.Contains(string(data), "[program:other]") {
		t.Error("other fragment should contain [program:other]")
	}
	if !strings.HasSuffix(string(data), "\n") {
		t.Error("fragment without trailing newline should get one added")
	}

	// python has no supervisord, should not have a fragment
	_, err = os.ReadFile(tmpDir + "/test-image/fragments/01-python.conf")
	if err == nil {
		t.Error("python should not have a supervisord fragment")
	}
}
