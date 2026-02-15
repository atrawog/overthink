package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// ScaffoldLayer creates a new layer directory with placeholder files
func ScaffoldLayer(dir string, name string) error {
	layerDir := filepath.Join(dir, "layers", name)

	// Check if layer already exists
	if _, err := os.Stat(layerDir); err == nil {
		return fmt.Errorf("layer %q already exists at %s", name, layerDir)
	}

	// Create layer directory
	if err := os.MkdirAll(layerDir, 0755); err != nil {
		return fmt.Errorf("creating layer directory: %w", err)
	}

	// Create a placeholder rpm.list
	rpmList := filepath.Join(layerDir, "rpm.list")
	rpmContent := fmt.Sprintf("# %s layer packages (rpm)\n# Add package names here, one per line\n", name)
	if err := os.WriteFile(rpmList, []byte(rpmContent), 0644); err != nil {
		return fmt.Errorf("creating rpm.list: %w", err)
	}

	fmt.Printf("Created layer at %s\n", layerDir)
	fmt.Println("Files created:")
	fmt.Println("  rpm.list - Add RPM packages here")
	fmt.Println()
	fmt.Println("Optional files you can add:")
	fmt.Println("  deb.list        - Debian/Ubuntu packages")
	fmt.Println("  copr.repo       - Fedora COPR repositories")
	fmt.Println("  root.yml        - Custom root install task")
	fmt.Println("  pixi.toml       - Python/conda packages")
	fmt.Println("  package.json    - npm packages")
	fmt.Println("  Cargo.toml      - Rust crate (requires src/)")
	fmt.Println("  user.yml        - Custom user install task")
	fmt.Println("  depends         - Layer dependencies")
	fmt.Println("  supervisord.conf - Service configuration")

	return nil
}
