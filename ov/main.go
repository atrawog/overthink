package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/alecthomas/kong"
)

// CLI defines the command-line interface structure
type CLI struct {
	Generate GenerateCmd `cmd:"" help:"Write .build/ (Containerfiles)"`
	Validate ValidateCmd `cmd:"" help:"Check images.yml + layers, exit 0 or 1"`
	Inspect  InspectCmd  `cmd:"" help:"Print resolved config for an image (JSON)"`
	List     ListCmd     `cmd:"" help:"List components"`
	New      NewCmd      `cmd:"" help:"Scaffold new components"`
	Build    BuildCmd    `cmd:"" help:"Build container images"`
	Merge    MergeCmd    `cmd:"" help:"Merge small layers in a built container image"`
	Shell    ShellCmd    `cmd:"" help:"Start a bash shell in a container image"`
	Start    StartCmd    `cmd:"" help:"Start a service container with supervisord (detached)"`
	Stop     StopCmd     `cmd:"" help:"Stop a running service container"`
	Enable   EnableCmd   `cmd:"" help:"Enable a service (quadlet: generate .container + reload)"`
	Disable  DisableCmd  `cmd:"" help:"Disable service auto-start (quadlet only)"`
	Status   StatusCmd   `cmd:"" help:"Show service container status"`
	Logs     LogsCmd     `cmd:"" help:"Show service container logs"`
	Update   UpdateCmd   `cmd:"" help:"Update image and restart if active"`
	Remove   RemoveCmd   `cmd:"" help:"Remove service container"`
	Alias    AliasCmd    `cmd:"" help:"Manage command aliases for container images"`
	Config   ConfigCmd   `cmd:"" help:"Manage runtime configuration"`
	Version  VersionCmd  `cmd:"" help:"Print computed CalVer tag"`
}

// GenerateCmd generates Containerfiles
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

// ValidateCmd validates images.yml and layers
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
		case "ports":
			for _, p := range resolved.Ports {
				fmt.Println(p)
			}
		case "volumes":
			layers, err := ScanLayers(dir)
			if err != nil {
				return err
			}
			volumes, err := CollectImageVolumes(cfg, layers, c.Image, resolved.Home)
			if err != nil {
				return err
			}
			for _, vol := range volumes {
				fmt.Printf("%s\t%s\n", vol.VolumeName, vol.ContainerPath)
			}
		case "aliases":
			layers, err := ScanLayers(dir)
			if err != nil {
				return err
			}
			aliases, err := CollectImageAliases(cfg, layers, c.Image)
			if err != nil {
				return err
			}
			for _, a := range aliases {
				fmt.Printf("%s\t%s\n", a.Name, a.Command)
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
	Images   ListImagesCmd   `cmd:"" help:"Images from images.yml"`
	Layers   ListLayersCmd   `cmd:"" help:"Layers from filesystem"`
	Targets  ListTargetsCmd  `cmd:"" help:"Build targets in dependency order"`
	Services ListServicesCmd `cmd:"" help:"Layers with service in layer.yml"`
	Routes   ListRoutesCmd   `cmd:"" help:"Layers with route in layer.yml"`
	Volumes  ListVolumesCmd  `cmd:"" help:"Layers with volumes in layer.yml"`
	Aliases  ListAliasesCmd  `cmd:"" help:"Layers with aliases in layer.yml"`
}

// ListImagesCmd lists images from images.yml
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

// ListTargetsCmd lists build targets in dependency order
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

	layers, err := ScanLayers(dir)
	if err != nil {
		return err
	}

	// Compute intermediates to get full build order
	images, err = ComputeIntermediates(images, layers, cfg, calverTag)
	if err != nil {
		return err
	}

	order, err := ResolveImageOrder(images, layers, cfg.Defaults.Builder)
	if err != nil {
		return err
	}

	for _, name := range order {
		img := images[name]
		if img.Auto {
			fmt.Printf("%s [auto]\n", name)
		} else {
			fmt.Println(name)
		}
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

// ListRoutesCmd lists layers with route files
type ListRoutesCmd struct{}

func (c *ListRoutesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	layers, err := ScanLayers(dir)
	if err != nil {
		return err
	}

	routes := RouteLayers(layers)
	// Sort by name for deterministic output
	names := make([]string, 0, len(routes))
	for _, layer := range routes {
		names = append(names, layer.Name)
	}
	sortStrings(names)

	for _, name := range names {
		layer := layers[name]
		route, err := layer.Route()
		if err != nil {
			return err
		}
		fmt.Printf("%s\thost=%s\tport=%s\n", name, route.Host, route.Port)
	}
	return nil
}

// ListVolumesCmd lists layers with volume declarations
type ListVolumesCmd struct{}

func (c *ListVolumesCmd) Run() error {
	dir, err := os.Getwd()
	if err != nil {
		return err
	}

	layers, err := ScanLayers(dir)
	if err != nil {
		return err
	}

	vols := VolumeLayers(layers)
	// Sort by name for deterministic output
	names := make([]string, 0, len(vols))
	for _, layer := range vols {
		names = append(names, layer.Name)
	}
	sortStrings(names)

	for _, name := range names {
		layer := layers[name]
		for _, vol := range layer.Volumes() {
			fmt.Printf("%s\t%s\t%s\n", name, vol.Name, vol.Path)
		}
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
	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	return ScaffoldLayer(dir, c.Name)
}

// ConfigCmd groups config subcommands
type ConfigCmd struct {
	Get   ConfigGetCmd   `cmd:"" help:"Print resolved value for a config key"`
	Set   ConfigSetCmd   `cmd:"" help:"Set a config value"`
	List  ConfigListCmd  `cmd:"" help:"Show all settings with source"`
	Reset ConfigResetCmd `cmd:"" help:"Remove a key from config (revert to default)"`
	Path  ConfigPathCmd  `cmd:"" help:"Print config file path"`
}

// ConfigGetCmd prints the resolved value for a key
type ConfigGetCmd struct {
	Key string `arg:"" help:"Config key (engine.build, engine.run, run_mode, auto_enable)"`
}

func (c *ConfigGetCmd) Run() error {
	// Show resolved value (env > config > default)
	rt, err := ResolveRuntime()
	if err != nil {
		return err
	}

	switch c.Key {
	case "engine.build":
		fmt.Println(rt.BuildEngine)
	case "engine.run":
		fmt.Println(rt.RunEngine)
	case "run_mode":
		fmt.Println(rt.RunMode)
	case "auto_enable":
		if rt.AutoEnable {
			fmt.Println("true")
		} else {
			fmt.Println("false")
		}
	default:
		return fmt.Errorf("unknown config key %q (valid: engine.build, engine.run, run_mode, auto_enable)", c.Key)
	}
	return nil
}

// ConfigSetCmd sets a config value
type ConfigSetCmd struct {
	Key   string `arg:"" help:"Config key"`
	Value string `arg:"" help:"Config value"`
}

func (c *ConfigSetCmd) Run() error {
	if err := SetConfigValue(c.Key, c.Value); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Set %s = %s\n", c.Key, c.Value)
	return nil
}

// ConfigListCmd shows all settings
type ConfigListCmd struct{}

func (c *ConfigListCmd) Run() error {
	vals, err := ListConfigValues()
	if err != nil {
		return err
	}
	for _, v := range vals {
		fmt.Printf("%-15s %-10s (%s)\n", v.Key, v.Value, v.Source)
	}
	return nil
}

// ConfigResetCmd removes a key from config
type ConfigResetCmd struct {
	Key string `arg:"" optional:"" help:"Config key to reset (omit to reset all)"`
}

func (c *ConfigResetCmd) Run() error {
	if err := ResetConfigValue(c.Key); err != nil {
		return err
	}
	if c.Key == "" {
		fmt.Fprintln(os.Stderr, "Reset all config to defaults")
	} else {
		fmt.Fprintf(os.Stderr, "Reset %s to default\n", c.Key)
	}
	return nil
}

// ConfigPathCmd prints the config file path
type ConfigPathCmd struct{}

func (c *ConfigPathCmd) Run() error {
	path, err := RuntimeConfigPath()
	if err != nil {
		return err
	}
	fmt.Println(path)
	return nil
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
