package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/midiparse/kustomize-plugins/internal/transform"
	"github.com/mikefarah/yq/v4/pkg/yqlib"
	goyaml "go.yaml.in/yaml/v3"
	yamlv4 "go.yaml.in/yaml/v4"
	logging "gopkg.in/op/go-logging.v1"
	"sigs.k8s.io/kustomize/kyaml/fn/framework"
	"sigs.k8s.io/kustomize/kyaml/fn/framework/command"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

func main() {
	// Configure yq logging - suppress debug messages unless DEBUG env var is set
	configureYqLogging()

	api := &API{}

	// Use the kyaml framework to build a command-line tool.
	cmd := command.Build(
		framework.SimpleProcessor{Config: api, Filter: api},
		command.StandaloneEnabled,
		false,
	)
	command.AddGenerateDockerfile(cmd)

	if err := cmd.Execute(); err != nil {
		log.Fatalf("Error executing command: %v", err)
	}
}

func configureYqLogging() {
	debugEnabled := os.Getenv("DEBUG") != ""
	logging.SetLevel(logging.ERROR, "yq-lib") // Default to ERROR level
	if debugEnabled {
		logging.SetLevel(logging.DEBUG, "yq-lib")
		return
	}
}

// YqTransformSpec defines the configuration for the yq transformer.
type YqTransformSpec struct {
	Source  *Source                     `yaml:"source,omitempty" json:"source,omitempty"`
	Targets []*transform.TargetSelector `json:"targets,omitempty" yaml:"targets,omitempty"`
}

// Source defines the yq expression and arguments.
type Source struct {
	Expression string `yaml:"expression" json:"expression"`
	Vars       []Var  `yaml:"vars,omitempty" json:"vars,omitempty"`
}

// Var defines a variable to be passed to the yq expression.
type Var struct {
	Name        string                    `yaml:"name" json:"name"`
	SourceValue *string                   `yaml:"sourceValue,omitempty" json:"sourceValue,omitempty"`
	Source      *transform.SourceSelector `yaml:"source,omitempty" json:"source,omitempty"`
}

// API is the top-level configuration for the function.
type API struct {
	Metadata struct {
		// Name is the Deployment Resource and Container name
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec YqTransformSpec `yaml:"spec" json:"spec"`
}

// Filter applies the yq expression to the target resources.
func (r *API) Filter(items []*yaml.RNode) ([]*yaml.RNode, error) {
	if r.Spec.Source == nil || r.Spec.Source.Expression == "" {
		return nil, fmt.Errorf("source.expression must be specified")
	}

	// Prepare yq variables
	vars, err := prepareVars(r.Spec.Source.Vars, items)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare yq vars: %w", err)
	}

	yq := &yqTransform{
		Expression: r.Spec.Source.Expression,
		Evaluator:  yqlib.NewAllAtOnceEvaluator(),
		Variables:  vars,
	}

	items, err = transform.Apply(yq, items, r.Spec.Targets)
	if err != nil {
		return nil, fmt.Errorf("failed to apply yq: %w", err)
	}

	return items, nil
}

func prepareVars(vars []Var, items []*yaml.RNode) (map[string]*goyaml.Node, error) {
	varNodes := make(map[string]*goyaml.Node)

	for _, v := range vars {
		if v.Name == "" {
			return nil, fmt.Errorf("variable name must be specified")
		}

		if _, exists := varNodes[v.Name]; exists {
			return nil, fmt.Errorf("duplicate variable %s", v.Name)
		}

		var node *goyaml.Node
		var err error

		if v.SourceValue != nil {
			node, err = sourceValueToNode(*v.SourceValue)
			if err != nil {
				return nil, fmt.Errorf("failed to parse sourceValue for variable %q: %w", v.Name, err)
			}
		} else if v.Source != nil {
			selectedNode, err := transform.SelectSourceNode(items, v.Source)
			if err != nil {
				return nil, fmt.Errorf("failed to select source for variable %q: %w", v.Name, err)
			}
			if selectedNode == nil {
				return nil, fmt.Errorf("no matching resource found for variable %q", v.Name)
			}
			node = selectedNode.YNode()
		} else {
			return nil, fmt.Errorf("either sourceValue or source must be specified for variable %q", v.Name)
		}
		varNodes[v.Name] = node
	}

	return varNodes, nil
}

func sourceValueToNode(value string) (*goyaml.Node, error) {
	decoder := goyaml.NewDecoder(strings.NewReader(value))
	var ynode goyaml.Node
	if err := decoder.Decode(&ynode); err != nil {
		return nil, err
	}
	// yq expects the top level node to be a document node
	docNode := &goyaml.Node{
		Kind:    goyaml.DocumentNode,
		Content: []*goyaml.Node{&ynode},
	}
	return docNode, nil
}

func v3ToV4(n *goyaml.Node) *yamlv4.Node {
	if n == nil {
		return nil
	}
	out := &yamlv4.Node{
		Kind:        yamlv4.Kind(n.Kind),
		Style:       yamlv4.Style(n.Style),
		Tag:         n.Tag,
		Value:       n.Value,
		Anchor:      n.Anchor,
		HeadComment: n.HeadComment,
		LineComment: n.LineComment,
		FootComment: n.FootComment,
		Line:        n.Line,
		Column:      n.Column,
	}
	if n.Alias != nil {
		out.Alias = v3ToV4(n.Alias)
	}
	if len(n.Content) > 0 {
		out.Content = make([]*yamlv4.Node, len(n.Content))
		for i, c := range n.Content {
			out.Content[i] = v3ToV4(c)
		}
	}
	return out
}

func v4ToV3(n *yamlv4.Node) *goyaml.Node {
	if n == nil {
		return nil
	}
	out := &goyaml.Node{
		Kind:        goyaml.Kind(n.Kind),
		Style:       goyaml.Style(n.Style),
		Tag:         n.Tag,
		Value:       n.Value,
		Anchor:      n.Anchor,
		HeadComment: n.HeadComment,
		LineComment: n.LineComment,
		FootComment: n.FootComment,
		Line:        n.Line,
		Column:      n.Column,
	}
	if n.Alias != nil {
		out.Alias = v4ToV3(n.Alias)
	}
	if len(n.Content) > 0 {
		out.Content = make([]*goyaml.Node, len(n.Content))
		for i, c := range n.Content {
			out.Content[i] = v4ToV3(c)
		}
	}
	return out
}

func toCandidateNode(node *yamlv4.Node) (*yqlib.CandidateNode, error) {
	var res yqlib.CandidateNode
	if err := res.UnmarshalYAML(node, nil); err != nil {
		return nil, err
	}
	return &res, nil
}

type yqTransform struct {
	Expression string
	Evaluator  yqlib.Evaluator
	Context    context.Context
	Variables  map[string]*goyaml.Node
}

func (s *yqTransform) CreateKind() yaml.Kind {
	return yaml.ScalarNode // Create a null scalar node to support node creation
}

func (s *yqTransform) Apply(target *yaml.RNode) error {
	expr, node := wrapInVariableContext(s.Expression, target.YNode(), s.Variables)

	inputNode, err := toCandidateNode(v3ToV4(node))
	if err != nil {
		return fmt.Errorf("failed to create input node for expression: %w", err)
	}

	// Evaluate the expression
	result, err := s.Evaluator.EvaluateNodes(expr, inputNode)
	if err != nil {
		return fmt.Errorf("failed to evaluate expression: %w", err)
	}

	if result.Len() == 0 {
		return fmt.Errorf("expression produced no results")
	}

	// Get the first result from the list
	firstResult := result.Front()
	if firstResult == nil {
		return fmt.Errorf("expression produced empty result")
	}

	resultCandidate, ok := firstResult.Value.(*yqlib.CandidateNode)
	if !ok {
		return fmt.Errorf("unexpected result type")
	}

	outNode, err := resultCandidate.MarshalYAML()
	if err != nil {
		return fmt.Errorf("failed to marshal yq result: %w", err)
	}

	// Replace the target node's content with the transformed content
	target.SetYNode(v4ToV3(outNode))

	return nil
}

func wrapInVariableContext(expression string, node *goyaml.Node, vars map[string]*goyaml.Node) (string, *goyaml.Node) {
	if len(vars) == 0 {
		// No variables to wrap
		return expression, node
	}
	// Create the 'vars' mapping node
	varsMapNode := &goyaml.Node{Kind: goyaml.MappingNode}
	for k, v := range vars {
		keyNode := &goyaml.Node{
			Kind:  goyaml.ScalarNode,
			Tag:   "!!str",
			Value: k,
		}
		// Ensure we are appending the actual yaml.Node from the CandidateNode
		varsMapNode.Content = append(varsMapNode.Content, keyNode, v)
	}

	// Create the top-level wrapper object
	wrapperNode := &goyaml.Node{
		Kind: goyaml.MappingNode,
		Content: []*goyaml.Node{
			{
				Kind:  goyaml.ScalarNode,
				Tag:   "!!str",
				Value: "target",
			},
			node,
			{
				Kind:  goyaml.ScalarNode,
				Tag:   "!!str",
				Value: "vars",
			},
			varsMapNode,
		},
	}

	// Wrap the user expression
	var wrappedExpression strings.Builder
	for varName := range vars {
		fmt.Fprintf(&wrappedExpression, ".vars.%s as $%s | ", varName, varName)
	}

	finalExpression := fmt.Sprintf(".target | %s", expression)
	wrappedExpression.WriteString(finalExpression)
	return wrappedExpression.String(), wrapperNode
}
