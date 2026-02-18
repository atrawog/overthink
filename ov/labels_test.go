package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestExtractMetadataFromLabels(t *testing.T) {
	// Mock the engine inspect to return known labels
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			LabelVersion:  "1",
			LabelImage:    "openclaw",
			LabelRegistry: "ghcr.io/overthinkos",
			LabelUID:      "1000",
			LabelGID:      "1000",
			LabelUser:     "user",
			LabelHome:     "/home/user",
			LabelPorts:    `["18789:18789"]`,
			LabelVolumes:  `[{"name":"data","path":"/home/user/.openclaw"}]`,
			LabelAliases:  `[{"name":"openclaw","command":"openclaw"}]`,
		}, nil
	}

	meta, err := ExtractMetadata("docker", "ghcr.io/overthinkos/openclaw:latest")
	if err != nil {
		t.Fatalf("ExtractMetadata() error = %v", err)
	}
	if meta == nil {
		t.Fatal("ExtractMetadata() returned nil")
	}

	if meta.Image != "openclaw" {
		t.Errorf("Image = %q, want %q", meta.Image, "openclaw")
	}
	if meta.Registry != "ghcr.io/overthinkos" {
		t.Errorf("Registry = %q, want %q", meta.Registry, "ghcr.io/overthinkos")
	}
	if meta.UID != 1000 {
		t.Errorf("UID = %d, want 1000", meta.UID)
	}
	if meta.GID != 1000 {
		t.Errorf("GID = %d, want 1000", meta.GID)
	}
	if meta.User != "user" {
		t.Errorf("User = %q, want %q", meta.User, "user")
	}
	if meta.Home != "/home/user" {
		t.Errorf("Home = %q, want %q", meta.Home, "/home/user")
	}

	wantPorts := []string{"18789:18789"}
	if !reflect.DeepEqual(meta.Ports, wantPorts) {
		t.Errorf("Ports = %v, want %v", meta.Ports, wantPorts)
	}

	wantVolumes := []VolumeMount{
		{VolumeName: "ov-openclaw-data", ContainerPath: "/home/user/.openclaw"},
	}
	if !reflect.DeepEqual(meta.Volumes, wantVolumes) {
		t.Errorf("Volumes = %v, want %v", meta.Volumes, wantVolumes)
	}

	wantAliases := []CollectedAlias{
		{Name: "openclaw", Command: "openclaw"},
	}
	if !reflect.DeepEqual(meta.Aliases, wantAliases) {
		t.Errorf("Aliases = %v, want %v", meta.Aliases, wantAliases)
	}
}

func TestExtractMetadataNoLabels(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{}, nil
	}

	meta, err := ExtractMetadata("docker", "ubuntu:24.04")
	if err != nil {
		t.Fatalf("ExtractMetadata() error = %v", err)
	}
	if meta != nil {
		t.Errorf("expected nil for image without org.overthink labels, got %+v", meta)
	}
}

func TestExtractMetadataMinimalLabels(t *testing.T) {
	orig := InspectLabels
	defer func() { InspectLabels = orig }()

	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return map[string]string{
			LabelVersion: "1",
			LabelImage:   "fedora",
			LabelUID:     "1000",
			LabelGID:     "1000",
			LabelUser:    "user",
			LabelHome:    "/home/user",
		}, nil
	}

	meta, err := ExtractMetadata("docker", "fedora:latest")
	if err != nil {
		t.Fatalf("ExtractMetadata() error = %v", err)
	}
	if meta == nil {
		t.Fatal("ExtractMetadata() returned nil")
	}

	if meta.Image != "fedora" {
		t.Errorf("Image = %q, want %q", meta.Image, "fedora")
	}
	if len(meta.Ports) != 0 {
		t.Errorf("Ports = %v, want empty", meta.Ports)
	}
	if len(meta.Volumes) != 0 {
		t.Errorf("Volumes = %v, want empty", meta.Volumes)
	}
	if len(meta.Aliases) != 0 {
		t.Errorf("Aliases = %v, want empty", meta.Aliases)
	}
}

func TestWriteLabelsEmitsLabels(t *testing.T) {
	g := &Generator{
		Config: &Config{
			Images: map[string]ImageConfig{
				"myapp": {
					Layers: []string{"svc"},
					Ports:  []string{"8080:8080"},
				},
			},
		},
		Layers: map[string]*Layer{
			"svc": {
				Name:       "svc",
				HasUserYml: true,
				HasVolumes: true,
				volumes: []VolumeYAML{
					{Name: "data", Path: "/home/user/.myapp"},
				},
				HasAliases: true,
				aliases: []AliasYAML{
					{Name: "myapp-cli", Command: "myapp"},
				},
			},
		},
	}

	img := &ResolvedImage{
		Name:     "myapp",
		Registry: "ghcr.io/test",
		UID:      1000,
		GID:      1000,
		User:     "user",
		Home:     "/home/user",
		Ports:    []string{"8080:8080"},
	}

	var b strings.Builder
	g.writeLabels(&b, "myapp", []string{"svc"}, img)
	output := b.String()

	// Check all expected labels are present
	checks := []struct {
		label string
		value string
	}{
		{LabelVersion, `"1"`},
		{LabelImage, `"myapp"`},
		{LabelRegistry, `"ghcr.io/test"`},
		{LabelUID, `"1000"`},
		{LabelGID, `"1000"`},
		{LabelUser, `"user"`},
		{LabelHome, `"/home/user"`},
	}

	for _, c := range checks {
		want := "LABEL " + c.label + "=" + c.value
		if !strings.Contains(output, want) {
			t.Errorf("missing label: %s\nin output:\n%s", want, output)
		}
	}

	// Check ports JSON
	if !strings.Contains(output, `LABEL org.overthink.ports='["8080:8080"]'`) {
		t.Errorf("missing or wrong ports label in:\n%s", output)
	}

	// Check volumes JSON
	if !strings.Contains(output, `LABEL org.overthink.volumes='[{"name":"data","path":"/home/user/.myapp"}]'`) {
		t.Errorf("missing or wrong volumes label in:\n%s", output)
	}

	// Check aliases JSON
	if !strings.Contains(output, `LABEL org.overthink.aliases='[{"name":"myapp-cli","command":"myapp"}]'`) {
		t.Errorf("missing or wrong aliases label in:\n%s", output)
	}
}

func TestWriteLabelsOmitsEmptyArrays(t *testing.T) {
	g := &Generator{
		Config: &Config{
			Images: map[string]ImageConfig{
				"minimal": {Layers: []string{"base"}},
			},
		},
		Layers: map[string]*Layer{
			"base": {
				Name:       "base",
				HasRootYml: true,
			},
		},
	}

	img := &ResolvedImage{
		Name: "minimal",
		UID:  1000,
		GID:  1000,
		User: "user",
		Home: "/home/user",
	}

	var b strings.Builder
	g.writeLabels(&b, "minimal", []string{"base"}, img)
	output := b.String()

	// Empty ports, volumes, aliases should not be emitted
	if strings.Contains(output, LabelPorts) {
		t.Errorf("should not emit ports label for empty ports, got:\n%s", output)
	}
	if strings.Contains(output, LabelVolumes) {
		t.Errorf("should not emit volumes label for empty volumes, got:\n%s", output)
	}
	if strings.Contains(output, LabelAliases) {
		t.Errorf("should not emit aliases label for empty aliases, got:\n%s", output)
	}

	// Registry should not be emitted when empty
	if strings.Contains(output, LabelRegistry) {
		t.Errorf("should not emit registry label for empty registry, got:\n%s", output)
	}
}

func TestLabelRoundTrip(t *testing.T) {
	// Generate labels
	g := &Generator{
		Config: &Config{
			Images: map[string]ImageConfig{
				"roundtrip": {
					Layers:  []string{"svc"},
					Ports:   []string{"9090:9090", "8080"},
					Aliases: []AliasConfig{{Name: "extra", Command: "extra-cmd"}},
				},
			},
		},
		Layers: map[string]*Layer{
			"svc": {
				Name:       "svc",
				HasUserYml: true,
				HasVolumes: true,
				volumes: []VolumeYAML{
					{Name: "cache", Path: "/home/testuser/.cache/myapp"},
					{Name: "data", Path: "/home/testuser/.data"},
				},
				HasAliases: true,
				aliases: []AliasYAML{
					{Name: "svc-cli", Command: "svc-cmd"},
				},
			},
		},
	}

	img := &ResolvedImage{
		Name:     "roundtrip",
		Registry: "ghcr.io/test",
		UID:      1001,
		GID:      1002,
		User:     "testuser",
		Home:     "/home/testuser",
		Ports:    []string{"9090:9090", "8080"},
	}

	var b strings.Builder
	g.writeLabels(&b, "roundtrip", []string{"svc"}, img)
	output := b.String()

	// Parse labels from the generated output
	labels := parseLabelsFromContainerfile(output)

	// Mock InspectLabels to return parsed labels
	orig := InspectLabels
	defer func() { InspectLabels = orig }()
	InspectLabels = func(engine, imageRef string) (map[string]string, error) {
		return labels, nil
	}

	meta, err := ExtractMetadata("docker", "test:latest")
	if err != nil {
		t.Fatalf("ExtractMetadata() error = %v", err)
	}
	if meta == nil {
		t.Fatal("ExtractMetadata() returned nil")
	}

	if meta.Image != "roundtrip" {
		t.Errorf("Image = %q, want %q", meta.Image, "roundtrip")
	}
	if meta.Registry != "ghcr.io/test" {
		t.Errorf("Registry = %q, want %q", meta.Registry, "ghcr.io/test")
	}
	if meta.UID != 1001 {
		t.Errorf("UID = %d, want 1001", meta.UID)
	}
	if meta.GID != 1002 {
		t.Errorf("GID = %d, want 1002", meta.GID)
	}
	if meta.User != "testuser" {
		t.Errorf("User = %q, want %q", meta.User, "testuser")
	}
	if meta.Home != "/home/testuser" {
		t.Errorf("Home = %q, want %q", meta.Home, "/home/testuser")
	}

	wantPorts := []string{"9090:9090", "8080"}
	if !reflect.DeepEqual(meta.Ports, wantPorts) {
		t.Errorf("Ports = %v, want %v", meta.Ports, wantPorts)
	}

	// Volumes should have full ov-<image>-<name> names
	wantVolumes := []VolumeMount{
		{VolumeName: "ov-roundtrip-cache", ContainerPath: "/home/testuser/.cache/myapp"},
		{VolumeName: "ov-roundtrip-data", ContainerPath: "/home/testuser/.data"},
	}
	if !reflect.DeepEqual(meta.Volumes, wantVolumes) {
		t.Errorf("Volumes = %v, want %v", meta.Volumes, wantVolumes)
	}

	// Aliases: layer alias + image-level alias
	if len(meta.Aliases) != 2 {
		t.Fatalf("Aliases count = %d, want 2", len(meta.Aliases))
	}
}

// parseLabelsFromContainerfile extracts LABEL directives from generated Containerfile text
func parseLabelsFromContainerfile(content string) map[string]string {
	labels := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "LABEL ") {
			continue
		}
		rest := strings.TrimPrefix(line, "LABEL ")
		eqIdx := strings.Index(rest, "=")
		if eqIdx < 0 {
			continue
		}
		key := rest[:eqIdx]
		value := rest[eqIdx+1:]

		// Remove quoting: "value" or 'value'
		if len(value) >= 2 {
			if (value[0] == '"' && value[len(value)-1] == '"') ||
				(value[0] == '\'' && value[len(value)-1] == '\'') {
				value = value[1 : len(value)-1]
			}
		}
		labels[key] = value
	}
	return labels
}

func TestLabelVolumeJSON(t *testing.T) {
	vol := LabelVolume{Name: "data", Path: "/home/user/.myapp"}
	data, err := json.Marshal(vol)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	want := `{"name":"data","path":"/home/user/.myapp"}`
	if string(data) != want {
		t.Errorf("json.Marshal(LabelVolume) = %s, want %s", data, want)
	}
}
