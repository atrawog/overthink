package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/alecthomas/kong"
)

// CLI defines the command-line interface structure
type CLI struct {
	Generate GenerateCmd `cmd:"" help:"Write .build/ (Containerfiles + HCL)"`
	Validate ValidateCmd `cmd:"" help:"Check build.json + layers, exit 0 or 1"`
	Inspect  InspectCmd  `cmd:"" help:"Print resolved config for an image (JSON)"`
	List     ListCmd     `cmd:"" help:"List components"`
	New      NewCmd      `cmd:"" help:"Scaffold new components"`
	Version  VersionCmd  `cmd:"" help:"Print computed CalVer tag"`
}

// GenerateCmd generates Containerfiles and docker-bake.hcl
type GenerateCmd struct {
	Tag string `long:"tag" help:"Override tag (default: CalVer)"`
}

func (c *GenerateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	gen, err := NewGenerator(dir, c.Tag)
	if err != nil {
		return err
	}

	return gen.Generate()
}

// ValidateCmd validates build.json and layers
type ValidateCmd struct{}

func (c *ValidateCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	layers, err := ScanLayers(dir)
	if err != nil {
		return err
	}

	return Validate(cfg, layers)
}

// InspectCmd prints resolved config for an image
type InspectCmd struct {
	Image  string `arg:"" help:"Image name"`
	Format string `long:"format" help:"Output a single field instead of full JSON"`
}

func (c *InspectCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	calverTag := ComputeCalVer()
	resolved, err := cfg.ResolveImage(c.Image, calverTag)
	if err != nil {
		return err
	}

	if c.Format != "" {
		// Output single field
		switch c.Format {
		case "tag":
			fmt.Println(resolved.FullTag)
		case "base":
			fmt.Println(resolved.Base)
		case "pkg":
			fmt.Println(resolved.Pkg)
		case "registry":
			fmt.Println(resolved.Registry)
		case "platforms":
			for _, p := range resolved.Platforms {
				fmt.Println(p)
			}
		case "layers":
			for _, l := range resolved.Layers {
				fmt.Println(l)
			}
		default:
			return fmt.Errorf("unknown format field: %s", c.Format)
		}
		return nil
	}

	// Output full JSON
	data, err := json.MarshalIndent(resolved, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}

// ListCmd groups list subcommands
type ListCmd struct {
	Images   ListImagesCmd   `cmd:"" help:"Images from build.json"`
	Layers   ListLayersCmd   `cmd:"" help:"Layers from filesystem"`
	Targets  ListTargetsCmd  `cmd:"" help:"Bake targets from generated HCL"`
	Services ListServicesCmd `cmd:"" help:"Layers with supervisord.conf"`
}

// ListImagesCmd lists images from build.json
type ListImagesCmd struct{}

func (c *ListImagesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	for _, name := range cfg.ImageNames() {
		fmt.Println(name)
	}
	return nil
}

// ListLayersCmd lists layers from filesystem
type ListLayersCmd struct{}

func (c *ListLayersCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	layers, err := ScanLayers(dir)
	if err != nil {
		return err
	}

	for _, name := range LayerNames(layers) {
		fmt.Println(name)
	}
	return nil
}

// ListTargetsCmd lists bake targets
type ListTargetsCmd struct{}

func (c *ListTargetsCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	cfg, err := LoadConfig(dir)
	if err != nil {
		return err
	}

	calverTag := ComputeCalVer()
	images, err := cfg.ResolveAllImages(calverTag)
	if err != nil {
		return err
	}

	order, err := ResolveImageOrder(images)
	if err != nil {
		return err
	}

	for _, name := range order {
		fmt.Println(name)
	}
	return nil
}

// ListServicesCmd lists layers with supervisord.conf
type ListServicesCmd struct{}

func (c *ListServicesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	layers, err := ScanLayers(dir)
	if err != nil {
		return err
	}

	services := ServiceLayers(layers)
	for _, layer := range services {
		fmt.Println(layer.Name)
	}
	return nil
}

// NewCmd groups scaffolding subcommands
type NewCmd struct {
	Layer NewLayerCmd `cmd:"" help:"Scaffold a layer directory"`
}

// NewLayerCmd scaffolds a new layer
type NewLayerCmd struct {
	Name string `arg:"" help:"Layer name"`
}

func (c *NewLayerCmd) Run() error {
	// TODO: implement in scaffold.go
	return fmt.Errorf("not implemented yet")
}

// VersionCmd prints the computed CalVer tag
type VersionCmd struct{}

func (c *VersionCmd) Run() error {
	version := ComputeCalVer()
	println(version)
	return nil
}

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("ov"),
		kong.Description("Overthink build system - composable container images"),
		kong.UsageOnError(),
	)
	err := ctx.Run()
	ctx.FatalIfErrorf(err)
}
