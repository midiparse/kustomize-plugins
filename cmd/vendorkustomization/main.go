package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	goyaml "go.yaml.in/yaml/v3"
	"sigs.k8s.io/kustomize/kyaml/fn/framework"
	"sigs.k8s.io/kustomize/kyaml/fn/framework/command"
	"sigs.k8s.io/kustomize/kyaml/kio"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

func main() {
	api := &API{}

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

// API is the top-level configuration for the VendorKustomization function.
type API struct {
	APIVersion string      `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
	Kind       string      `json:"kind,omitempty" yaml:"kind,omitempty"`
	Metadata   Metadata    `json:"metadata" yaml:"metadata"`
	Spec       SpecUnion   `json:"spec" yaml:"spec"`
	Status     StatusUnion `json:"status,omitempty" yaml:"status,omitempty"`
}

type Metadata struct {
	Name        string            `json:"name" yaml:"name"`
	Namespace   string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

type Source struct {
	Type    string `json:"type" yaml:"type"`
	Repo    string `json:"repo" yaml:"repo"`
	Version string `json:"version" yaml:"version"`
	Asset   string `json:"asset" yaml:"asset"`
}

type SpecItem struct {
	Source      Source `json:"source" yaml:"source"`
	Destination string `json:"destination,omitempty" yaml:"destination,omitempty"`
}

type StatusItem struct {
	Destination string `json:"destination" yaml:"destination"`
	Hash        string `json:"hash" yaml:"hash"`
	Source      Source `json:"source" yaml:"source"`
}

type SpecUnion struct {
	Items    []SpecItem
	IsSingle bool
}

func (s *SpecUnion) UnmarshalJSON(b []byte) error {
	var single SpecItem
	if err := json.Unmarshal(b, &single); err == nil && single.Source.Type != "" {
		s.IsSingle = true
		s.Items = []SpecItem{single}
		return nil
	}
	var list []SpecItem
	if err := json.Unmarshal(b, &list); err == nil {
		s.IsSingle = false
		s.Items = list
		return nil
	}
	return fmt.Errorf("failed to unmarshal spec as single item or list: %s", string(b))
}

func (s *SpecUnion) UnmarshalYAML(value *goyaml.Node) error {
	if value.Kind == goyaml.SequenceNode {
		s.IsSingle = false
		return value.Decode(&s.Items)
	}
	if value.Kind == goyaml.MappingNode {
		s.IsSingle = true
		var single SpecItem
		if err := value.Decode(&single); err != nil {
			return err
		}
		s.Items = []SpecItem{single}
		return nil
	}
	return fmt.Errorf("unexpected YAML kind for spec: %v", value.Kind)
}

type StatusUnion struct {
	Items    []StatusItem
	IsSingle bool
}

func (s *StatusUnion) UnmarshalJSON(b []byte) error {
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	var single StatusItem
	if err := json.Unmarshal(b, &single); err == nil && (single.Destination != "" || single.Hash != "") {
		s.IsSingle = true
		s.Items = []StatusItem{single}
		return nil
	}
	var list []StatusItem
	if err := json.Unmarshal(b, &list); err == nil {
		s.IsSingle = false
		s.Items = list
		return nil
	}
	return fmt.Errorf("failed to unmarshal status as single item or list: %s", string(b))
}

func (s *StatusUnion) UnmarshalYAML(value *goyaml.Node) error {
	if value.Kind == goyaml.SequenceNode {
		s.IsSingle = false
		return value.Decode(&s.Items)
	}
	if value.Kind == goyaml.MappingNode {
		s.IsSingle = true
		var single StatusItem
		if err := value.Decode(&single); err != nil {
			return err
		}
		s.Items = []StatusItem{single}
		return nil
	}
	return fmt.Errorf("unexpected YAML kind for status: %v", value.Kind)
}

// Downloader interface abstracts asset fetching.
type Downloader interface {
	Download(ctx context.Context, repo, version, asset string) ([]byte, error)
}

type HTTPDownloader struct {
	Client *http.Client
}

func (d *HTTPDownloader) Download(ctx context.Context, repo, version, asset string) ([]byte, error) {
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}

	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, version, asset)
	if baseURL := os.Getenv("GITHUB_BASE_URL"); baseURL != "" {
		url = fmt.Sprintf("%s/%s/releases/download/%s/%s", strings.TrimRight(baseURL, "/"), repo, version, asset)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", url, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download from %s returned status %d %s", url, resp.StatusCode, resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body from %s: %w", url, err)
	}

	return data, nil
}

var defaultDownloader Downloader = &HTTPDownloader{}

// Filter reconciles each vendored asset and outputs the generated Kubernetes resources.
func (r *API) Filter(items []*yaml.RNode) ([]*yaml.RNode, error) {
	if len(r.Spec.Items) == 0 {
		return nil, fmt.Errorf("spec must specify at least one source")
	}

	// Status map by destination for fast lookup
	statusByDest := make(map[string]StatusItem)
	for _, st := range r.Status.Items {
		if st.Destination != "" {
			statusByDest[st.Destination] = st
		}
	}

	var updatedStatus []StatusItem
	var needWriteBack bool
	var generatedNodes []*yaml.RNode

	ctx := context.Background()

	for _, specItem := range r.Spec.Items {
		src := specItem.Source
		if src.Type != "githubRelease" {
			return nil, fmt.Errorf("unsupported source type %q: only githubRelease is supported", src.Type)
		}
		if src.Repo == "" || !strings.Contains(src.Repo, "/") {
			return nil, fmt.Errorf("source.repo must be specified as <owner>/<repo>, got %q", src.Repo)
		}
		if src.Version == "" {
			return nil, fmt.Errorf("source.version must be specified")
		}
		if src.Asset == "" {
			return nil, fmt.Errorf("source.asset must be specified")
		}

		dest := specItem.Destination
		if dest == "" {
			dest = filepath.Join("vendor", src.Asset)
		}

		// Check cache hit
		cacheHit := false
		var fileContent []byte

		if existingStatus, ok := statusByDest[dest]; ok {
			if existingStatus.Source == src && existingStatus.Hash != "" {
				if existingData, err := os.ReadFile(dest); err == nil {
					computedHash := fmt.Sprintf("sha256:%x", sha256.Sum256(existingData))
					if computedHash == existingStatus.Hash {
						cacheHit = true
						fileContent = existingData
						updatedStatus = append(updatedStatus, existingStatus)
					}
				}
			}
		}

		if !cacheHit {
			data, err := defaultDownloader.Download(ctx, src.Repo, src.Version, src.Asset)
			if err != nil {
				return nil, fmt.Errorf("failed to fetch asset %s@%s: %w", src.Repo, src.Version, err)
			}

			computedHash := fmt.Sprintf("sha256:%x", sha256.Sum256(data))

			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				return nil, fmt.Errorf("failed to create destination directory for %s: %w", dest, err)
			}
			if err := writeFileAtomic(dest, data, 0644); err != nil {
				return nil, fmt.Errorf("failed to write vendored file %s: %w", dest, err)
			}

			fileContent = data
			newStatus := StatusItem{
				Destination: dest,
				Hash:        computedHash,
				Source:      src,
			}
			updatedStatus = append(updatedStatus, newStatus)
			needWriteBack = true
		}

		// Parse the vendored manifest file
		nodes, err := kio.FromBytes(fileContent)
		if err != nil {
			return nil, fmt.Errorf("failed to parse manifests from %s: %w", dest, err)
		}
		generatedNodes = append(generatedNodes, nodes...)
	}

	// If status was missing or updated, write back to generator file
	if needWriteBack {
		genPath, err := findGeneratorFile(r)
		if err != nil {
			return nil, fmt.Errorf("failed to locate generator file for write-back: %w", err)
		}
		if err := updateStatusInFile(genPath, updatedStatus, r.Spec.IsSingle); err != nil {
			return nil, fmt.Errorf("failed to update status in %s: %w", genPath, err)
		}
	}

	// Filter out the generator resource itself from input items if present
	var outItems []*yaml.RNode
	for _, item := range items {
		if item.GetKind() == "VendorKustomization" && item.GetName() == r.Metadata.Name {
			continue
		}
		outItems = append(outItems, item)
	}

	outItems = append(outItems, generatedNodes...)
	return outItems, nil
}

func findGeneratorFile(api *API) (string, error) {
	// 1. Explicit annotations
	annotations := api.Metadata.Annotations
	candidateKeys := []string{
		"config.kolobok.szakallas.eu/file-path",
		"kustomize-plugins.midiparse.github.com/file-path",
		"internal.config.kubernetes.io/path",
		"config.kubernetes.io/path",
	}
	for _, key := range candidateKeys {
		if path, ok := annotations[key]; ok && path != "" {
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}
	}

	// 2. Command line arguments in standalone mode
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		if matchesGenerator(os.Args[1], api.Metadata.Name) {
			return os.Args[1], nil
		}
	}

	// 3. Inspect kustomization.yaml / kustomization.yml / Kustomization
	for _, kFileName := range []string{"kustomization.yaml", "kustomization.yml", "Kustomization"} {
		if data, err := os.ReadFile(kFileName); err == nil {
			var kNode goyaml.Node
			if err := goyaml.Unmarshal(data, &kNode); err == nil && len(kNode.Content) > 0 {
				root := kNode.Content[0]
				var genPaths []string
				for i := 0; i < len(root.Content); i += 2 {
					if root.Content[i].Value == "generators" {
						seq := root.Content[i+1]
						if seq.Kind == goyaml.SequenceNode {
							for _, it := range seq.Content {
								genPaths = append(genPaths, it.Value)
							}
						}
					}
				}
				for _, p := range genPaths {
					if matchesGenerator(p, api.Metadata.Name) {
						return p, nil
					}
				}
			}
		}
	}

	// 4. Scan current working directory for YAML files
	entries, err := os.ReadDir(".")
	if err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
				if matchesGenerator(name, api.Metadata.Name) {
					return name, nil
				}
			}
		}
	}

	return "", fmt.Errorf("could not find generator file matching name %q", api.Metadata.Name)
}

func matchesGenerator(filePath, expectedName string) bool {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	var doc goyaml.Node
	if err := goyaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return false
	}
	for _, root := range doc.Content {
		if root.Kind != goyaml.MappingNode {
			continue
		}
		var kind string
		var name string
		for i := 0; i < len(root.Content); i += 2 {
			k := root.Content[i].Value
			v := root.Content[i+1]
			if k == "kind" {
				kind = v.Value
			} else if k == "metadata" && v.Kind == goyaml.MappingNode {
				for j := 0; j < len(v.Content); j += 2 {
					if v.Content[j].Value == "name" {
						name = v.Content[j+1].Value
					}
				}
			}
		}
		if kind == "VendorKustomization" && name == expectedName {
			return true
		}
	}
	return false
}

func updateStatusInFile(filePath string, statusItems []StatusItem, isSingle bool) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	var doc goyaml.Node
	if err := goyaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		return fmt.Errorf("empty yaml document in %s", filePath)
	}

	// Find the mapping node with kind: VendorKustomization
	var targetMapping *goyaml.Node
	for _, n := range doc.Content {
		if n.Kind == goyaml.MappingNode {
			for i := 0; i < len(n.Content); i += 2 {
				if n.Content[i].Value == "kind" && n.Content[i+1].Value == "VendorKustomization" {
					targetMapping = n
					break
				}
			}
		}
		if targetMapping != nil {
			break
		}
	}

	if targetMapping == nil && len(doc.Content) > 0 && doc.Content[0].Kind == goyaml.MappingNode {
		targetMapping = doc.Content[0]
	}
	if targetMapping == nil {
		return fmt.Errorf("no mapping node found in %s", filePath)
	}

	// Detect if spec in targetMapping is single or sequence to preserve structure
	for i := 0; i < len(targetMapping.Content); i += 2 {
		if targetMapping.Content[i].Value == "spec" {
			switch targetMapping.Content[i+1].Kind {
			case goyaml.MappingNode:
				isSingle = true
			case goyaml.SequenceNode:
				isSingle = false
			}
		}
	}

	var statusVal goyaml.Node
	if isSingle && len(statusItems) > 0 {
		if err := statusVal.Encode(statusItems[0]); err != nil {
			return err
		}
	} else {
		if err := statusVal.Encode(statusItems); err != nil {
			return err
		}
	}

	found := false
	for i := 0; i < len(targetMapping.Content); i += 2 {
		if targetMapping.Content[i].Value == "status" {
			targetMapping.Content[i+1] = &statusVal
			found = true
			break
		}
	}
	if !found {
		statusKey := &goyaml.Node{
			Kind:  goyaml.ScalarNode,
			Value: "status",
		}
		targetMapping.Content = append(targetMapping.Content, statusKey, &statusVal)
	}

	var buf bytes.Buffer
	enc := goyaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return err
	}

	return writeFileAtomic(filePath, buf.Bytes(), info.Mode().Perm())
}

// writeFileAtomic writes data to a temporary file in the destination directory and
// renames it into place, so readers never observe a partially written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) (err error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if err != nil {
			_ = os.Remove(tmpName)
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}

	return os.Rename(tmpName, path)
}
