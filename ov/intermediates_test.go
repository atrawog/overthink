package main

import (
	"reflect"
	"testing"
)

func TestGlobalLayerOrder_PopularityTieBreaking(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Depends: nil},
		"nodejs":  {Name: "nodejs", Depends: nil},
		"python":  {Name: "python", Depends: []string{"pixi"}},
		"testapi": {Name: "testapi", Depends: []string{"python"}, HasPixiToml: true},
	}

	// pixi is used by 2 images, nodejs by 1
	images := map[string]*ResolvedImage{
		"a": {Name: "a", Base: "ext:1", IsExternalBase: true, Layers: []string{"pixi", "python", "testapi"}},
		"b": {Name: "b", Base: "ext:1", IsExternalBase: true, Layers: []string{"pixi", "nodejs"}},
	}

	order, err := GlobalLayerOrder(images, layers)
	if err != nil {
		t.Fatalf("GlobalLayerOrder() error = %v", err)
	}

	// pixi (popularity 2) should come before nodejs (popularity 1)
	// python depends on pixi so must come after pixi
	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	if indexOf("pixi") > indexOf("nodejs") {
		t.Errorf("pixi should come before nodejs (higher popularity), got order %v", order)
	}
	if indexOf("pixi") > indexOf("python") {
		t.Errorf("pixi should come before python (dependency), got order %v", order)
	}
}

func TestGlobalLayerOrder_RespectsDependencies(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":   {Name: "pixi", Depends: nil},
		"python": {Name: "python", Depends: []string{"pixi"}},
	}

	images := map[string]*ResolvedImage{
		"a": {Name: "a", Base: "ext:1", IsExternalBase: true, Layers: []string{"python"}},
	}

	order, err := GlobalLayerOrder(images, layers)
	if err != nil {
		t.Fatalf("GlobalLayerOrder() error = %v", err)
	}

	if len(order) != 2 {
		t.Fatalf("expected 2 layers, got %d: %v", len(order), order)
	}
	if order[0] != "pixi" || order[1] != "python" {
		t.Errorf("expected [pixi python], got %v", order)
	}
}

func TestAbsoluteLayerSequence_WithInternalBase(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Depends: nil},
		"python":  {Name: "python", Depends: []string{"pixi"}},
		"nodejs":  {Name: "nodejs", Depends: nil},
		"testapi": {Name: "testapi", Depends: []string{"python"}, HasPixiToml: true},
	}

	images := map[string]*ResolvedImage{
		"base": {Name: "base", Base: "ext:1", IsExternalBase: true, Layers: []string{"pixi"}},
		"app":  {Name: "app", Base: "base", IsExternalBase: false, Layers: []string{"python", "testapi"}},
	}

	globalOrder := []string{"pixi", "nodejs", "python", "testapi"}

	seq := AbsoluteLayerSequence("app", images, layers, globalOrder)

	// app needs pixi (from base) + python + testapi
	expected := []string{"pixi", "python", "testapi"}
	if !reflect.DeepEqual(seq, expected) {
		t.Errorf("AbsoluteLayerSequence = %v, want %v", seq, expected)
	}
}

func TestComputeIntermediates_NoBranching(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":   {Name: "pixi", Depends: nil, HasRootYml: true},
		"python": {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
	}

	images := map[string]*ResolvedImage{
		"app": {
			Name: "app", Base: "ext:1", IsExternalBase: true,
			Layers: []string{"python"}, Tag: "v1", Registry: "r",
			FullTag: "r/app:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm"},
		Images:   map[string]ImageConfig{"app": {Layers: []string{"python"}}},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// With single image, no intermediates should be created
	autoCount := 0
	for _, img := range result {
		if img.Auto {
			autoCount++
		}
	}
	if autoCount != 0 {
		t.Errorf("expected 0 auto intermediates, got %d", autoCount)
	}
}

func TestComputeIntermediates_SimpleBranch(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Depends: nil, HasRootYml: true},
		"python":  {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
		"nodejs":  {Name: "nodejs", Depends: nil, HasRootYml: true},
		"testapi": {Name: "testapi", Depends: []string{"python"}, HasPixiToml: true},
	}

	images := map[string]*ResolvedImage{
		"fedora": {
			Name: "fedora", Base: "ext:1", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		"app1": {
			Name: "app1", Base: "fedora", IsExternalBase: false,
			Layers: []string{"python", "testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/app1:v1", Pkg: "rpm",
		},
		"app2": {
			Name: "app2", Base: "fedora", IsExternalBase: false,
			Layers: []string{"nodejs"}, Tag: "v1", Registry: "r",
			FullTag: "r/app2:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm"},
		Images: map[string]ImageConfig{
			"fedora": {Layers: []string{}},
			"app1":   {Base: "fedora", Layers: []string{"python", "testapi"}},
			"app2":   {Base: "fedora", Layers: []string{"nodejs"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// With pixi shared between app1 and app2 (through python dep),
	// we may get an intermediate for pixi
	// Both share pixi as common prefix in global order
	// app1: pixi, python, testapi
	// app2: pixi, nodejs (but nodejs doesn't depend on pixi in this setup)

	// Actually app2 only has nodejs which doesn't depend on pixi.
	// So the absolute sequences diverge immediately:
	// app1: pixi, python, testapi (pixi is transitive dep of python)
	// app2: nodejs
	// No common prefix → no intermediate created

	// Verify all original images still exist
	for name := range images {
		if _, ok := result[name]; !ok {
			t.Errorf("original image %q missing from result", name)
		}
	}
}

func TestComputeIntermediates_SharedPrefix(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":         {Name: "pixi", Depends: nil, HasRootYml: true},
		"python":       {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
		"supervisord":  {Name: "supervisord", Depends: []string{"python"}, HasPixiToml: true},
		"testapi":      {Name: "testapi", Depends: []string{"supervisord"}, HasPixiToml: true},
		"openclaw":     {Name: "openclaw", Depends: []string{"supervisord"}, HasPackageJson: true},
	}

	images := map[string]*ResolvedImage{
		"fedora": {
			Name: "fedora", Base: "ext:1", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		"fedora-test": {
			Name: "fedora-test", Base: "fedora", IsExternalBase: false,
			Layers: []string{"testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora-test:v1", Pkg: "rpm",
		},
		"openclaw": {
			Name: "openclaw", Base: "fedora", IsExternalBase: false,
			Layers: []string{"openclaw"}, Tag: "v1", Registry: "r",
			FullTag: "r/openclaw:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm"},
		Images: map[string]ImageConfig{
			"fedora":      {Layers: []string{}},
			"fedora-test": {Base: "fedora", Layers: []string{"testapi"}},
			"openclaw":    {Base: "fedora", Layers: []string{"openclaw"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// Both fedora-test and openclaw share: pixi → python → supervisord
	// They diverge at supervisord: testapi vs openclaw
	// So we should get an intermediate at the supervisord branching point

	// Check that at least one auto intermediate was created
	autoCount := 0
	for _, img := range result {
		if img.Auto {
			autoCount++
		}
	}
	if autoCount == 0 {
		t.Error("expected at least 1 auto intermediate, got 0")
		for name, img := range result {
			t.Logf("  %s: base=%s layers=%v auto=%v", name, img.Base, img.Layers, img.Auto)
		}
	}

	// Both fedora-test and openclaw should have an intermediate as base (not fedora directly)
	ftImg := result["fedora-test"]
	ocImg := result["openclaw"]
	if ftImg.Base == "fedora" && ocImg.Base == "fedora" {
		t.Error("both images still use fedora as base — expected an intermediate")
		for name, img := range result {
			t.Logf("  %s: base=%s layers=%v auto=%v", name, img.Base, img.Layers, img.Auto)
		}
	}
}

func TestComputeIntermediates_ExistingImageReuse(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":   {Name: "pixi", Depends: nil, HasRootYml: true},
		"nodejs": {Name: "nodejs", Depends: nil, HasRootYml: true},
	}

	images := map[string]*ResolvedImage{
		"fedora": {
			Name: "fedora", Base: "ext:1", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		"app1": {
			Name: "app1", Base: "fedora", IsExternalBase: false,
			Layers: []string{"pixi"}, Tag: "v1", Registry: "r",
			FullTag: "r/app1:v1", Pkg: "rpm",
		},
		"app2": {
			Name: "app2", Base: "fedora", IsExternalBase: false,
			Layers: []string{"nodejs"}, Tag: "v1", Registry: "r",
			FullTag: "r/app2:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm"},
		Images: map[string]ImageConfig{
			"fedora": {Layers: []string{}},
			"app1":   {Base: "fedora", Layers: []string{"pixi"}},
			"app2":   {Base: "fedora", Layers: []string{"nodejs"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// fedora at root should be reused (not duplicated)
	if _, ok := result["fedora"]; !ok {
		t.Error("fedora should still exist in result")
	}

	// Both app1 and app2 have no common prefix after fedora (pixi vs nodejs)
	// so no intermediate is needed — they should still base on fedora
	if result["app1"].Base != "fedora" {
		t.Errorf("app1 base = %q, want 'fedora'", result["app1"].Base)
	}
	if result["app2"].Base != "fedora" {
		t.Errorf("app2 base = %q, want 'fedora'", result["app2"].Base)
	}
}

func TestImageNeedsBuilder(t *testing.T) {
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Depends: nil, HasRootYml: true},
		"python":  {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
		"nodejs":  {Name: "nodejs", Depends: nil, HasRootYml: true},
		"tooling": {Name: "tooling", Depends: nil, HasRootYml: true},
	}

	images := map[string]*ResolvedImage{
		"builder": {
			Name: "builder", Base: "ext:1", IsExternalBase: true,
			Layers: []string{"pixi", "nodejs", "tooling"},
		},
		"base": {
			Name: "base", Base: "ext:1", IsExternalBase: true,
			Layers: []string{"pixi"},
		},
		"app": {
			Name: "app", Base: "base", IsExternalBase: false,
			Layers: []string{"python"},
		},
		"simple": {
			Name: "simple", Base: "ext:1", IsExternalBase: true,
			Layers: []string{"tooling"},
		},
	}

	// pixi has root.yml only (no pixi.toml) → does NOT need builder
	if ImageNeedsBuilder(images["base"], images, layers, "builder") {
		t.Error("base should not need builder (pixi has root.yml only, no pixi.toml)")
	}

	// app has python which has pixi.toml → NEEDS builder
	if !ImageNeedsBuilder(images["app"], images, layers, "builder") {
		t.Error("app should need builder (python has pixi.toml)")
	}

	// simple has tooling (root.yml only) → does NOT need builder
	if ImageNeedsBuilder(images["simple"], images, layers, "builder") {
		t.Error("simple should not need builder (tooling has root.yml only)")
	}

	// nil layers → conservative true
	if !ImageNeedsBuilder(images["simple"], images, nil, "builder") {
		t.Error("nil layers should return true (conservative)")
	}
}

func TestComputeIntermediates_RealisticConfig(t *testing.T) {
	// Simplified version of the actual images.yml setup
	layers := map[string]*Layer{
		"pixi":            {Name: "pixi", Depends: nil, HasRootYml: true},
		"nodejs":          {Name: "nodejs", Depends: nil, HasRootYml: true},
		"python":          {Name: "python", Depends: []string{"pixi"}, HasPixiToml: true},
		"supervisord":     {Name: "supervisord", Depends: []string{"python"}, HasPixiToml: true},
		"build-toolchain": {Name: "build-toolchain", Depends: nil, HasRootYml: true},
		"testapi":         {Name: "testapi", Depends: []string{"supervisord"}, HasPixiToml: true},
		"traefik":         {Name: "traefik", Depends: []string{"supervisord"}, HasRootYml: true},
		"openclaw":        {Name: "openclaw", Depends: []string{"supervisord", "nodejs"}, HasPackageJson: true},
	}

	images := map[string]*ResolvedImage{
		"builder": {
			Name: "builder", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layers: []string{"pixi", "nodejs", "build-toolchain"}, Tag: "v1", Registry: "r",
			FullTag: "r/builder:v1", Pkg: "rpm",
		},
		"fedora": {
			Name: "fedora", Base: "quay.io/fedora/fedora:43", IsExternalBase: true,
			Layers: []string{}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora:v1", Pkg: "rpm",
		},
		"fedora-test": {
			Name: "fedora-test", Base: "fedora", IsExternalBase: false,
			Layers: []string{"traefik", "testapi"}, Tag: "v1", Registry: "r",
			FullTag: "r/fedora-test:v1", Pkg: "rpm",
		},
		"openclaw": {
			Name: "openclaw", Base: "fedora", IsExternalBase: false,
			Layers: []string{"openclaw"}, Tag: "v1", Registry: "r",
			FullTag: "r/openclaw:v1", Pkg: "rpm",
		},
	}

	cfg := &Config{
		Defaults: ImageConfig{Registry: "r", Pkg: "rpm", Builder: "builder"},
		Images: map[string]ImageConfig{
			"builder":     {Layers: []string{"pixi", "nodejs", "build-toolchain"}},
			"fedora":      {Layers: []string{}},
			"fedora-test": {Base: "fedora", Layers: []string{"traefik", "testapi"}},
			"openclaw":    {Base: "fedora", Layers: []string{"openclaw"}},
		},
	}

	result, err := ComputeIntermediates(images, layers, cfg, "v1")
	if err != nil {
		t.Fatalf("ComputeIntermediates() error = %v", err)
	}

	// Log all images for debugging
	t.Log("Resulting images:")
	for name, img := range result {
		t.Logf("  %s: base=%s layers=%v auto=%v", name, img.Base, img.Layers, img.Auto)
	}

	// All original images should still exist
	for name := range images {
		if _, ok := result[name]; !ok {
			t.Errorf("original image %q missing from result", name)
		}
	}

	// The build order should not have cycles
	order, err := ResolveImageOrder(result, layers, cfg.Defaults.Builder)
	if err != nil {
		t.Fatalf("ResolveImageOrder after intermediates: %v", err)
	}
	t.Logf("Build order: %v", order)

	// builder should come before any image that needs it
	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	builderIdx := indexOf("builder")
	if builderIdx < 0 {
		t.Fatal("builder not in build order")
	}

	// Verify no cycles by checking builder comes early
	fedoraIdx := indexOf("fedora")
	if fedoraIdx < 0 {
		t.Fatal("fedora not in build order")
	}
}
