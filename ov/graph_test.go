package main

import (
	"reflect"
	"testing"
)

func TestResolveLayerOrder(t *testing.T) {
	// Create test layers
	layers := map[string]*Layer{
		"pixi":    {Name: "pixi", Depends: nil},
		"python":  {Name: "python", Depends: []string{"pixi"}},
		"ml-libs": {Name: "ml-libs", Depends: []string{"python"}},
		"nodejs":  {Name: "nodejs", Depends: nil},
		"web-ui":  {Name: "web-ui", Depends: []string{"nodejs"}},
	}

	tests := []struct {
		name         string
		requested    []string
		parentLayers map[string]bool
		wantOrder    []string
		wantErr      bool
	}{
		{
			name:         "single layer no deps",
			requested:    []string{"pixi"},
			parentLayers: nil,
			wantOrder:    []string{"pixi"},
		},
		{
			name:         "layer with deps",
			requested:    []string{"python"},
			parentLayers: nil,
			wantOrder:    []string{"pixi", "python"},
		},
		{
			name:         "transitive deps",
			requested:    []string{"ml-libs"},
			parentLayers: nil,
			wantOrder:    []string{"pixi", "python", "ml-libs"},
		},
		{
			name:         "multiple independent layers",
			requested:    []string{"pixi", "nodejs"},
			parentLayers: nil,
			wantOrder:    []string{"nodejs", "pixi"}, // sorted alphabetically
		},
		{
			name:         "mixed deps",
			requested:    []string{"ml-libs", "web-ui"},
			parentLayers: nil,
			wantOrder:    []string{"nodejs", "pixi", "python", "ml-libs", "web-ui"},
		},
		{
			name:         "parent provides dep",
			requested:    []string{"python"},
			parentLayers: map[string]bool{"pixi": true},
			wantOrder:    []string{"python"}, // pixi excluded
		},
		{
			name:         "unknown layer",
			requested:    []string{"unknown"},
			parentLayers: nil,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			order, err := ResolveLayerOrder(tt.requested, layers, tt.parentLayers)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(order, tt.wantOrder) {
				t.Errorf("order = %v, want %v", order, tt.wantOrder)
			}
		})
	}
}

func TestResolveLayerOrderCycle(t *testing.T) {
	// Create layers with a cycle: a -> b -> c -> a
	layers := map[string]*Layer{
		"a": {Name: "a", Depends: []string{"b"}},
		"b": {Name: "b", Depends: []string{"c"}},
		"c": {Name: "c", Depends: []string{"a"}},
	}

	_, err := ResolveLayerOrder([]string{"a"}, layers, nil)
	if err == nil {
		t.Error("expected cycle error, got nil")
	}

	cycleErr, ok := err.(*CycleError)
	if !ok {
		t.Errorf("expected CycleError, got %T", err)
	} else if len(cycleErr.Cycle) == 0 {
		t.Error("CycleError.Cycle is empty")
	}
}

func TestResolveImageOrder(t *testing.T) {
	// Create test images
	images := map[string]*ResolvedImage{
		"base": {
			Name:           "base",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"cuda": {
			Name:           "cuda",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"ml-cuda": {
			Name:           "ml-cuda",
			Base:           "cuda",
			IsExternalBase: false,
		},
		"inference": {
			Name:           "inference",
			Base:           "ml-cuda",
			IsExternalBase: false,
		},
	}

	order, err := ResolveImageOrder(images, nil, "")
	if err != nil {
		t.Fatalf("ResolveImageOrder() error = %v", err)
	}

	// Check that dependencies come before dependents
	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	// cuda must come before ml-cuda
	if indexOf("cuda") > indexOf("ml-cuda") {
		t.Errorf("cuda should come before ml-cuda, got order %v", order)
	}

	// ml-cuda must come before inference
	if indexOf("ml-cuda") > indexOf("inference") {
		t.Errorf("ml-cuda should come before inference, got order %v", order)
	}
}

func TestResolveImageOrderWithBuilder(t *testing.T) {
	images := map[string]*ResolvedImage{
		"builder": {
			Name:           "builder",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"fedora": {
			Name:           "fedora",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
		},
		"app": {
			Name:           "app",
			Base:           "fedora",
			IsExternalBase: false,
		},
	}

	order, err := ResolveImageOrder(images, nil, "builder")
	if err != nil {
		t.Fatalf("ResolveImageOrder() error = %v", err)
	}

	indexOf := func(name string) int {
		for i, n := range order {
			if n == name {
				return i
			}
		}
		return -1
	}

	// builder must come before fedora and app
	if indexOf("builder") > indexOf("fedora") {
		t.Errorf("builder should come before fedora, got order %v", order)
	}
	if indexOf("builder") > indexOf("app") {
		t.Errorf("builder should come before app, got order %v", order)
	}
	// fedora must come before app
	if indexOf("fedora") > indexOf("app") {
		t.Errorf("fedora should come before app, got order %v", order)
	}
}

func TestResolveImageOrderCycle(t *testing.T) {
	// Create images with a cycle
	images := map[string]*ResolvedImage{
		"a": {Name: "a", Base: "b", IsExternalBase: false},
		"b": {Name: "b", Base: "c", IsExternalBase: false},
		"c": {Name: "c", Base: "a", IsExternalBase: false},
	}

	_, err := ResolveImageOrder(images, nil, "")
	if err == nil {
		t.Error("expected cycle error, got nil")
	}
}

func TestLayersProvidedByImage(t *testing.T) {
	images := map[string]*ResolvedImage{
		"base": {
			Name:           "base",
			Base:           "quay.io/fedora/fedora:43",
			IsExternalBase: true,
			Layers:         []string{"pixi"},
		},
		"cuda": {
			Name:           "cuda",
			Base:           "base",
			IsExternalBase: false,
			Layers:         []string{"cuda"},
		},
		"ml-cuda": {
			Name:           "ml-cuda",
			Base:           "cuda",
			IsExternalBase: false,
			Layers:         []string{"python", "ml-libs"},
		},
	}

	layers := map[string]*Layer{} // not used, just for type

	tests := []struct {
		name      string
		imageName string
		want      map[string]bool
	}{
		{
			name:      "base image",
			imageName: "base",
			want:      map[string]bool{"pixi": true},
		},
		{
			name:      "cuda inherits from base",
			imageName: "cuda",
			want:      map[string]bool{"pixi": true, "cuda": true},
		},
		{
			name:      "ml-cuda inherits from cuda",
			imageName: "ml-cuda",
			want:      map[string]bool{"pixi": true, "cuda": true, "python": true, "ml-libs": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := LayersProvidedByImage(tt.imageName, images, layers)
			if err != nil {
				t.Fatalf("LayersProvidedByImage() error = %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("LayersProvidedByImage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTopoSortDeterministic(t *testing.T) {
	// Run multiple times to ensure deterministic output
	graph := map[string][]string{
		"a": nil,
		"b": nil,
		"c": {"a"},
		"d": {"a", "b"},
	}

	first, err := topoSort(graph)
	if err != nil {
		t.Fatalf("topoSort() error = %v", err)
	}

	for i := 0; i < 10; i++ {
		result, err := topoSort(graph)
		if err != nil {
			t.Fatalf("topoSort() error = %v", err)
		}
		if !reflect.DeepEqual(result, first) {
			t.Errorf("non-deterministic output: got %v, first was %v", result, first)
		}
	}
}
