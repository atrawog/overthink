package main

import (
	"reflect"
	"testing"
)

func TestScanLayers(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	expectedLayers := []string{"pixi", "python", "nodejs", "cargo-tool", "webservice"}
	for _, name := range expectedLayers {
		if _, ok := layers[name]; !ok {
			t.Errorf("missing layer %q", name)
		}
	}
}

func TestLayerPixi(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi == nil {
		t.Fatal("pixi layer not found")
	}

	if !pixi.HasRootYml {
		t.Error("pixi should have root.yml")
	}
	if !pixi.HasUserYml {
		t.Error("pixi should have user.yml")
	}
	if pixi.HasRpmList {
		t.Error("pixi should not have rpm.list")
	}
	if pixi.HasPixiToml {
		t.Error("pixi should not have pixi.toml")
	}
	if len(pixi.Depends) != 0 {
		t.Errorf("pixi should have no depends, got %v", pixi.Depends)
	}
}

func TestLayerPython(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	python := layers["python"]
	if python == nil {
		t.Fatal("python layer not found")
	}

	if !python.HasPixiToml {
		t.Error("python should have pixi.toml")
	}
	if !reflect.DeepEqual(python.Depends, []string{"pixi"}) {
		t.Errorf("python.Depends = %v, want [pixi]", python.Depends)
	}
}

func TestLayerNodejs(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	nodejs := layers["nodejs"]
	if nodejs == nil {
		t.Fatal("nodejs layer not found")
	}

	if !nodejs.HasRpmList {
		t.Error("nodejs should have rpm.list")
	}
	if !nodejs.HasDebList {
		t.Error("nodejs should have deb.list")
	}

	// Test package reading
	rpms, err := nodejs.RpmPackages()
	if err != nil {
		t.Fatalf("RpmPackages() error = %v", err)
	}
	if !reflect.DeepEqual(rpms, []string{"nodejs", "npm"}) {
		t.Errorf("RpmPackages() = %v, want [nodejs npm]", rpms)
	}

	debs, err := nodejs.DebPackages()
	if err != nil {
		t.Fatalf("DebPackages() error = %v", err)
	}
	if !reflect.DeepEqual(debs, []string{"nodejs", "npm"}) {
		t.Errorf("DebPackages() = %v, want [nodejs npm]", debs)
	}
}

func TestLayerCargoTool(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	cargoTool := layers["cargo-tool"]
	if cargoTool == nil {
		t.Fatal("cargo-tool layer not found")
	}

	if !cargoTool.HasCargoToml {
		t.Error("cargo-tool should have Cargo.toml")
	}
	if !cargoTool.HasSrcDir {
		t.Error("cargo-tool should have src/ directory")
	}
}

func TestHasInstallFiles(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	for name, layer := range layers {
		if !layer.HasInstallFiles() {
			t.Errorf("layer %q should have install files", name)
		}
	}
}

func TestLayerNames(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	names := LayerNames(layers)
	if len(names) != 5 {
		t.Errorf("LayerNames() returned %d names, want 5", len(names))
	}

	// Should be sorted
	for i := 0; i < len(names)-1; i++ {
		if names[i] > names[i+1] {
			t.Errorf("LayerNames() not sorted: %v", names)
			break
		}
	}
}

func TestLayerPorts(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice layer not found")
	}

	if !ws.HasPorts {
		t.Error("webservice should have ports")
	}

	ports, err := ws.Ports()
	if err != nil {
		t.Fatalf("Ports() error = %v", err)
	}
	if !reflect.DeepEqual(ports, []string{"8080", "9090"}) {
		t.Errorf("Ports() = %v, want [8080 9090]", ports)
	}

	// Test caching
	ports2, err := ws.Ports()
	if err != nil {
		t.Fatalf("Ports() second call error = %v", err)
	}
	if !reflect.DeepEqual(ports, ports2) {
		t.Error("Ports() should return cached result")
	}
}

func TestLayerPortsNone(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi.HasPorts {
		t.Error("pixi should not have ports")
	}

	ports, err := pixi.Ports()
	if err != nil {
		t.Fatalf("Ports() error = %v", err)
	}
	if ports != nil {
		t.Errorf("Ports() = %v, want nil", ports)
	}
}

func TestLayerRoute(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	ws := layers["webservice"]
	if ws == nil {
		t.Fatal("webservice layer not found")
	}

	if !ws.HasRoute {
		t.Error("webservice should have route")
	}

	route, err := ws.Route()
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if route.Host != "webservice.localhost" {
		t.Errorf("Route().Host = %q, want %q", route.Host, "webservice.localhost")
	}
	if route.Port != "8080" {
		t.Errorf("Route().Port = %q, want %q", route.Port, "8080")
	}

	// Test caching
	route2, err := ws.Route()
	if err != nil {
		t.Fatalf("Route() second call error = %v", err)
	}
	if route != route2 {
		t.Error("Route() should return cached result")
	}
}

func TestLayerRouteNone(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	pixi := layers["pixi"]
	if pixi.HasRoute {
		t.Error("pixi should not have route")
	}

	route, err := pixi.Route()
	if err != nil {
		t.Fatalf("Route() error = %v", err)
	}
	if route != nil {
		t.Errorf("Route() = %v, want nil", route)
	}
}

func TestRouteLayers(t *testing.T) {
	layers, err := ScanLayers("testdata")
	if err != nil {
		t.Fatalf("ScanLayers() error = %v", err)
	}

	routes := RouteLayers(layers)
	if len(routes) != 1 {
		t.Errorf("RouteLayers() returned %d layers, want 1", len(routes))
	}
	if len(routes) > 0 && routes[0].Name != "webservice" {
		t.Errorf("RouteLayers()[0].Name = %q, want %q", routes[0].Name, "webservice")
	}
}

func TestReadLineFile(t *testing.T) {
	// Test the function with a known file
	lines, err := readLineFile("testdata/layers/nodejs/rpm.list")
	if err != nil {
		t.Fatalf("readLineFile() error = %v", err)
	}

	expected := []string{"nodejs", "npm"}
	if !reflect.DeepEqual(lines, expected) {
		t.Errorf("readLineFile() = %v, want %v", lines, expected)
	}
}
