package main

import (
	"fmt"
	"log"

	"github.com/midiparse/kustomize-plugins/internal/transform"
	"sigs.k8s.io/kustomize/api/krusty"
	ktypes "sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
	"sigs.k8s.io/kustomize/kyaml/fn/framework"
	"sigs.k8s.io/kustomize/kyaml/fn/framework/command"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

func main() {
	api := &API{}

	// Use the kyaml framework to build a command-line tool.
	cmd := command.Build(
		framework.SimpleProcessor{Config: api, Filter: api},
		command.StandaloneEnabled,
		false,
	)
	if err := cmd.Execute(); err != nil {
		log.Fatalf("Error executing command: %v", err)
	}
}

// API is the top-level configuration for the function.
type API struct {
	Metadata struct {
		// Name is the Deployment Resource and Container name
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec ResourceInjectorSpec `yaml:"spec" json:"spec"`
}

type setValue struct {
	Value *yaml.RNode
}

func (s *setValue) CreateKind() yaml.Kind {
	return s.Value.YNode().Kind
}

func (s *setValue) Apply(target *yaml.RNode) error {
	value := s.Value.Copy()

	if target.YNode().Kind == yaml.ScalarNode {
		// For scalar, only copy the value (leave any type intact to auto-convert int->string or string->int)
		target.YNode().Value = value.YNode().Value
	} else {
		target.SetYNode(value.YNode())
	}

	return nil
}

// Filter reads the source, builds it if necessary, and injects the result
// into the target resources.
func (r *API) Filter(items []*yaml.RNode) ([]*yaml.RNode, error) {
	if r.Spec.Source == nil || r.Spec.Source.Path == "" {
		return nil, fmt.Errorf("source.path must be specified")
	}

	// 1. Render the source content.
	source, err := kustomizeSource(r.Spec.Source)
	if err != nil {
		return nil, fmt.Errorf("failed to render source: %w", err)
	}

	if r.Spec.Source.FieldPath != "" {
		var err error
		source, err = source.Pipe(yaml.Lookup(r.Spec.Source.FieldPath))
		if err != nil {
			return nil, fmt.Errorf("failed to lookup field path in rendered source: %w", err)
		}
		if source == nil {
			return nil, fmt.Errorf("field path %q not found in rendered source", r.Spec.Source.FieldPath)
		}
	}

	// We wrap it in a string node as the value needs to be injected as a string.
	sourceContent, err := source.String()
	if err != nil {
		return nil, fmt.Errorf("failed to convert source to string: %w", err)
	}
	setter := setValue{Value: yaml.NewScalarRNode(sourceContent)}

	items, err = transform.Apply(&setter, items, r.Spec.Targets)
	if err != nil {
		return nil, fmt.Errorf("failed to apply replacements: %w", err)
	}

	return items, nil
}

// ResourceInjectorSpec defines the configuration for the resource injector.
type ResourceInjectorSpec struct {
	Source  *SourceSpec                 `yaml:"source,omitempty" json:"source,omitempty"`
	Targets []*transform.TargetSelector `json:"targets,omitempty" yaml:"targets,omitempty"`
}

// SourceSpec defines the source of the content to be injected.
type SourceSpec struct {
	// Path to the kustomization directory.
	Path string `yaml:"path" json:"path"`
	// Optional field path to extract from the rendered source.
	FieldPath string `yaml:"fieldPath,omitempty" json:"fieldPath,omitempty"`
	// Optional kustomize options applied when rendering directories.
	Options *SourceOptions `yaml:"options,omitempty" json:"options,omitempty"`
}

// SourceOptions allows fine-tuning of the kustomize run for the source.
type SourceOptions struct {
	Reorder           krusty.ReorderOption `yaml:"reorder,omitempty" json:"reorder,omitempty"`
	AddManagedByLabel bool                 `yaml:"addManagedByLabel,omitempty" json:"addManagedByLabel,omitempty"`
	LoadRestrictions  LoadRestrictionsType `yaml:"loadRestrictions,omitempty" json:"loadRestrictions,omitempty"`
	PluginConfig      *PluginConfig        `yaml:"pluginConfig,omitempty" json:"pluginConfig,omitempty"`
}

// PluginConfig defines plugin-related configuration.
type PluginConfig struct {
	// PluginRestrictions distinguishes plugin restrictions.
	PluginRestrictions PluginRestrictionsType `yaml:"pluginRestrictions,omitempty" json:"pluginRestrictions,omitempty"`

	// FnpLoadingOptions sets the way function-based plugin behaviors.
	FnpLoadingOptions FnPluginLoadingOptions `yaml:"fnpLoadingOptions,omitempty" json:"fnpLoadingOptions,omitempty"`

	// HelmConfig contains metadata needed for allowing and running helm.
	HelmConfig HelmConfig `yaml:"helmConfig,omitempty" json:"helmConfig,omitempty"`
}

type HelmConfig struct {
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`
}

type FnPluginLoadingOptions struct {
	// Allow to run executables
	EnableExec bool `yaml:"enableExec,omitempty" json:"enableExec,omitempty"`
}

// LoadRestrictionsType is a typed string for load restriction options.
type LoadRestrictionsType string

// LoadRestrictions enumeration for kustomize load restrictions.
const (
	// LoadRestrictionsUnknown is the default (unknown) restriction.
	LoadRestrictionsUnknown LoadRestrictionsType = "unknown"
	// LoadRestrictionsRootOnly restricts file loads to the kustomization directory or below.
	LoadRestrictionsRootOnly LoadRestrictionsType = "rootOnly"
	// LoadRestrictionsNone allows unrestricted file paths.
	LoadRestrictionsNone LoadRestrictionsType = "none"
)

// parseLoadRestrictions converts a LoadRestrictionsType to ktypes.LoadRestrictions.
func parseLoadRestrictions(s LoadRestrictionsType) (ktypes.LoadRestrictions, error) {
	switch s {
	case LoadRestrictionsNone:
		return ktypes.LoadRestrictionsNone, nil
	case LoadRestrictionsRootOnly:
		return ktypes.LoadRestrictionsRootOnly, nil
	case LoadRestrictionsUnknown, "":
		return ktypes.LoadRestrictionsUnknown, nil
	default:
		return 0, fmt.Errorf("unrecognized load restriction: %q", s)
	}
}

// PluginRestrictionsType is a typed string for plugin restriction options.
type PluginRestrictionsType string

// PluginRestrictions enumeration for kustomize plugin restrictions.
const (
	// PluginRestrictionsUnknown is the default (unknown) restriction.
	PluginRestrictionsUnknown PluginRestrictionsType = "unknown"
	// PluginRestrictionsBuiltinsOnly allows only built-in plugins.
	PluginRestrictionsBuiltinsOnly PluginRestrictionsType = "builtinsOnly"
	// PluginRestrictionsNone allows unrestricted plugin usage.
	PluginRestrictionsNone PluginRestrictionsType = "none"
)

// parsePluginRestrictions converts a PluginRestrictionsType to ktypes.PluginRestrictions.
func parsePluginRestrictions(s PluginRestrictionsType) (ktypes.PluginRestrictions, error) {
	switch s {
	case PluginRestrictionsNone:
		return ktypes.PluginRestrictionsNone, nil
	case PluginRestrictionsBuiltinsOnly:
		return ktypes.PluginRestrictionsBuiltinsOnly, nil
	case PluginRestrictionsUnknown, "":
		return ktypes.PluginRestrictionsUnknown, nil
	default:
		return 0, fmt.Errorf("unrecognized plugin restriction: %q", s)
	}
}

func applySourceOptions(opts *krusty.Options, sourceOpts *SourceOptions) error {
	if sourceOpts == nil {
		return nil
	}

	if sourceOpts.Reorder != "" {
		opts.Reorder = sourceOpts.Reorder
	}
	if sourceOpts.LoadRestrictions != "" {
		lr, err := parseLoadRestrictions(sourceOpts.LoadRestrictions)
		if err != nil {
			return err
		}
		opts.LoadRestrictions = lr
	}
	opts.AddManagedbyLabel = sourceOpts.AddManagedByLabel

	if sourceOpts.PluginConfig != nil {
		pr, err := parsePluginRestrictions(sourceOpts.PluginConfig.PluginRestrictions)
		if err != nil {
			return err
		}
		opts.PluginConfig = &ktypes.PluginConfig{
			PluginRestrictions: pr,
			FnpLoadingOptions: ktypes.FnPluginLoadingOptions{
				EnableExec: sourceOpts.PluginConfig.FnpLoadingOptions.EnableExec,
			},
			HelmConfig: ktypes.HelmConfig{
				Enabled: sourceOpts.PluginConfig.HelmConfig.Enabled,
			},
		}
	}
	return nil
}

// kustomizeSource renders a SourceSpec and returns the content as a structured yaml node.
func kustomizeSource(source *SourceSpec) (*yaml.RNode, error) {
	fSys := filesys.MakeFsOnDisk()
	sourcePath := source.Path

	// Check if the path is a directory
	if fSys.IsDir(sourcePath) {
		// Treat as a kustomization directory and build it.
		opts := krusty.MakeDefaultOptions()
		if err := applySourceOptions(opts, source.Options); err != nil {
			return nil, fmt.Errorf("failed to apply source options: %w", err)
		}
		k := krusty.MakeKustomizer(opts)
		resMap, err := k.Run(fSys, sourcePath)
		if err != nil {
			return nil, fmt.Errorf("kustomize build failed for %q: %w", sourcePath, err)
		}
		yamlBytes, err := resMap.AsYaml()
		if err != nil {
			return nil, fmt.Errorf("failed to marshal kustomize output to YAML: %w", err)
		}
		return yaml.Parse(string(yamlBytes))
	}

	// If not a kustomization, treat it as a plain file.
	content, err := fSys.ReadFile(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read source file %q: %w", sourcePath, err)
	}

	return yaml.Parse(string(content))
}
