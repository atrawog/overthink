package main

import (
	"os"
	"strings"
	"testing"
)

func TestGenerateBakeHCL_InternalBaseContexts(t *testing.T) {
	tmpDir := t.TempDir()

	g := &Generator{
		BuildDir: tmpDir,
		Config: &Config{
			Images: map[string]ImageConfig{
				"fedora":      {Tag: "auto"},
				"fedora-test": {Tag: "auto"},
			},
		},
		Images: map[string]*ResolvedImage{
			"fedora": {
				Name:           "fedora",
				Base:           "fedora:43",
				IsExternalBase: true,
				Registry:       "ghcr.io/atrawog",
				Tag:            "2026.46.1415",
				FullTag:        "ghcr.io/atrawog/fedora:2026.46.1415",
				Platforms:      []string{"linux/amd64", "linux/arm64"},
			},
			"fedora-test": {
				Name:           "fedora-test",
				Base:           "fedora",
				IsExternalBase: false,
				Registry:       "ghcr.io/atrawog",
				Tag:            "2026.46.1415",
				FullTag:        "ghcr.io/atrawog/fedora-test:2026.46.1415",
				Platforms:      []string{"linux/amd64", "linux/arm64"},
			},
		},
	}

	err := g.generateBakeHCL([]string{"fedora", "fedora-test"})
	if err != nil {
		t.Fatalf("generateBakeHCL() error = %v", err)
	}

	data, err := os.ReadFile(tmpDir + "/docker-bake.hcl")
	if err != nil {
		t.Fatalf("reading generated HCL: %v", err)
	}
	hcl := string(data)

	// fedora-test should have depends_on and contexts
	if !strings.Contains(hcl, `depends_on = ["fedora"]`) {
		t.Error("missing depends_on for fedora-test")
	}
	if !strings.Contains(hcl, `"ghcr.io/atrawog/fedora:2026.46.1415" = "target:fedora"`) {
		t.Error("missing contexts block for fedora-test")
	}

	// fedora (external base) should NOT have depends_on or contexts
	// Extract the fedora target block
	fedoraIdx := strings.Index(hcl, `target "fedora" {`)
	fedoraTestIdx := strings.Index(hcl, `target "fedora-test" {`)
	if fedoraIdx == -1 || fedoraTestIdx == -1 {
		t.Fatal("could not find both target blocks in HCL")
	}
	fedoraBlock := hcl[fedoraIdx:fedoraTestIdx]
	if strings.Contains(fedoraBlock, "depends_on") {
		t.Error("fedora target should not have depends_on")
	}
	if strings.Contains(fedoraBlock, "contexts") {
		t.Error("fedora target should not have contexts")
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
