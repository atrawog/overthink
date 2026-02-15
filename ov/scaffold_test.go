package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScaffoldLayer(t *testing.T) {
	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "scaffold-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Scaffold a layer
	err = ScaffoldLayer(tmpDir, "test-layer")
	if err != nil {
		t.Fatalf("ScaffoldLayer() error = %v", err)
	}

	// Check directory was created
	layerDir := filepath.Join(tmpDir, "layers", "test-layer")
	if _, err := os.Stat(layerDir); os.IsNotExist(err) {
		t.Error("layer directory was not created")
	}

	// Check rpm.list was created
	rpmList := filepath.Join(layerDir, "rpm.list")
	if _, err := os.Stat(rpmList); os.IsNotExist(err) {
		t.Error("rpm.list was not created")
	}
}

func TestScaffoldLayerAlreadyExists(t *testing.T) {
	// Create temp directory with existing layer
	tmpDir, err := os.MkdirTemp("", "scaffold-test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	// Create layer directory
	layerDir := filepath.Join(tmpDir, "layers", "existing")
	if err := os.MkdirAll(layerDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Try to scaffold - should fail
	err = ScaffoldLayer(tmpDir, "existing")
	if err == nil {
		t.Error("expected error for existing layer")
	}
}
