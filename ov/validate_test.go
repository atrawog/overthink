package main

import (
	"strings"
	"testing"
)

func TestValidateSuccess(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Pkg:       "rpm",
			Platforms: []string{"linux/amd64"},
		},
		Images: map[string]ImageConfig{
			"base": {Layers: []string{"pixi"}},
		},
	}

	layers := map[string]*Layer{
		"pixi": {
			Name:       "pixi",
			HasRootYml: true,
			HasUserYml: true,
		},
	}

	err := Validate(cfg, layers)
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateInvalidPkg(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Pkg: "invalid",
		},
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for invalid pkg")
	}
	if !strings.Contains(err.Error(), "pkg must be") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateMissingLayer(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"nonexistent"}},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for missing layer")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateMissingLayerWithTypo(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"pixie"}}, // typo for "pixi"
		},
	}
	layers := map[string]*Layer{
		"pixi": {Name: "pixi", HasRootYml: true},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for missing layer")
	}
	if !strings.Contains(err.Error(), "did you mean") {
		t.Errorf("expected typo suggestion, got: %v", err)
	}
}

func TestValidateLayerNoInstallFiles(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"empty": {Name: "empty"}, // no install files
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for layer without install files")
	}
	if !strings.Contains(err.Error(), "must have at least one install file") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCargoWithoutSrc(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"tool": {
			Name:         "tool",
			HasCargoToml: true,
			HasSrcDir:    false,
		},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for Cargo.toml without src/")
	}
	if !strings.Contains(err.Error(), "requires src/") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateCoprWithoutRpmList(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"layer": {
			Name:        "layer",
			HasCoprRepo: true,
			HasRpmList:  false,
			HasRootYml:  true, // needs some install file
		},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for copr.repo without rpm.list")
	}
	if !strings.Contains(err.Error(), "copr.repo requires rpm.list") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateUnknownDependency(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"layer": {
			Name:       "layer",
			HasRootYml: true,
			Depends:    []string{"unknown"},
		},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for unknown dependency")
	}
	if !strings.Contains(err.Error(), "unknown layer") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateImageCycle(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"a": {Base: "b", Layers: []string{}},
			"b": {Base: "c", Layers: []string{}},
			"c": {Base: "a", Layers: []string{}},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for image cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateLayerCycle(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"a"}},
		},
	}
	layers := map[string]*Layer{
		"a": {Name: "a", HasRootYml: true, Depends: []string{"b"}},
		"b": {Name: "b", HasRootYml: true, Depends: []string{"c"}},
		"c": {Name: "c", HasRootYml: true, Depends: []string{"a"}},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for layer cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateMultipleErrors(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{Pkg: "invalid"},
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"missing1", "missing2"}},
		},
	}
	layers := map[string]*Layer{}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected errors")
	}

	valErr, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected ValidationError, got %T", err)
	}

	// Should have at least 3 errors: invalid pkg, two missing layers
	if len(valErr.Errors) < 3 {
		t.Errorf("expected at least 3 errors, got %d: %v", len(valErr.Errors), valErr.Errors)
	}
}

func TestValidateLayerPortsValid(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"web": {
			Name:       "web",
			HasUserYml: true,
			HasPorts:   true,
			ports:      []string{"8080", "9090"},
		},
	}

	err := Validate(cfg, layers)
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateLayerPortsInvalid(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"web": {
			Name:       "web",
			HasUserYml: true,
			HasPorts:   true,
			ports:      []string{"99999"},
		},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for invalid port number")
	}
	if !strings.Contains(err.Error(), "not a valid port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateImagePortsValid(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Pkg:       "rpm",
			Platforms: []string{"linux/amd64"},
		},
		Images: map[string]ImageConfig{
			"test": {
				Layers: []string{"web"},
				Ports:  []string{"8080:8080", "9090"},
			},
		},
	}
	layers := map[string]*Layer{
		"web": {Name: "web", HasUserYml: true},
	}

	err := Validate(cfg, layers)
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestValidateImagePortsInvalid(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {
				Layers: []string{"web"},
				Ports:  []string{"abc:8080"},
			},
		},
	}
	layers := map[string]*Layer{
		"web": {Name: "web", HasUserYml: true},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for invalid port mapping")
	}
	if !strings.Contains(err.Error(), "not valid") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateImagePortsBadFormat(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {
				Layers: []string{"web"},
				Ports:  []string{"8080:9090:1234"},
			},
		},
	}
	layers := map[string]*Layer{
		"web": {Name: "web", HasUserYml: true},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for bad port format")
	}
	if !strings.Contains(err.Error(), "host:container") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRouteMissingHost(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:     "svc",
			HasRoute: true,
			HasUserYml: true,
			route:    &RouteConfig{Host: "", Port: "8080"},
		},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for route missing host")
	}
	if !strings.Contains(err.Error(), "missing required \"host\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRouteMissingPort(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:     "svc",
			HasRoute: true,
			HasUserYml: true,
			route:    &RouteConfig{Host: "svc.localhost", Port: ""},
		},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for route missing port")
	}
	if !strings.Contains(err.Error(), "missing required \"port\"") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRouteInvalidPort(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:     "svc",
			HasRoute: true,
			HasUserYml: true,
			route:    &RouteConfig{Host: "svc.localhost", Port: "99999"},
		},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for route invalid port")
	}
	if !strings.Contains(err.Error(), "not a valid port") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRouteWithoutTraefik(t *testing.T) {
	cfg := &Config{
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"svc"}},
		},
	}
	layers := map[string]*Layer{
		"svc": {
			Name:     "svc",
			HasRoute: true,
			HasUserYml: true,
			route:    &RouteConfig{Host: "svc.localhost", Port: "8080"},
		},
	}

	err := Validate(cfg, layers)
	if err == nil {
		t.Error("expected error for route without traefik")
	}
	if !strings.Contains(err.Error(), "traefik layer is not reachable") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateRouteWithTraefik(t *testing.T) {
	cfg := &Config{
		Defaults: ImageConfig{
			Registry:  "ghcr.io/test",
			Pkg:       "rpm",
			Platforms: []string{"linux/amd64"},
		},
		Images: map[string]ImageConfig{
			"test": {Layers: []string{"traefik", "svc"}},
		},
	}
	layers := map[string]*Layer{
		"traefik": {
			Name:       "traefik",
			HasRootYml: true,
		},
		"svc": {
			Name:     "svc",
			HasRoute: true,
			HasUserYml: true,
			route:    &RouteConfig{Host: "svc.localhost", Port: "8080"},
		},
	}

	err := Validate(cfg, layers)
	if err != nil {
		t.Errorf("Validate() unexpected error: %v", err)
	}
}

func TestIsValidPort(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"80", true},
		{"8080", true},
		{"65535", true},
		{"1", true},
		{"0", false},
		{"65536", false},
		{"abc", false},
		{"", false},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isValidPort(tt.input)
			if got != tt.want {
				t.Errorf("isValidPort(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestLevenshteinDistance(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"a", "", 1},
		{"", "a", 1},
		{"pixi", "pixi", 0},
		{"pixi", "pixie", 1},
		{"pixi", "pxi", 1},
		{"pixi", "python", 5},
	}

	for _, tt := range tests {
		t.Run(tt.a+"-"+tt.b, func(t *testing.T) {
			got := levenshteinDistance(tt.a, tt.b)
			if got != tt.want {
				t.Errorf("levenshteinDistance(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
			}
		})
	}
}
