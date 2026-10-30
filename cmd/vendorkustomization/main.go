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
	"net/url"
	"os"
	"path"
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

	Fetcher Fetcher `json:"-" yaml:"-"`
}

type Metadata struct {
	Name        string            `json:"name" yaml:"name"`
	Namespace   string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// Source is a type-specific asset source. Implementations validate their
// configuration, suggest a destination path, download the asset to a path, and
// identify themselves for cache comparisons.
type Source interface {
	SourceType() string
	Validate() error
	DefaultDestination() string
	Download(ctx context.Context, fetcher Fetcher, dest string) (string, error)
	Key() string
}

// GithubReleaseSource downloads a release asset from github.com.
type GithubReleaseSource struct {
	Type    string `json:"type" yaml:"type"`
	Repo    string `json:"repo" yaml:"repo"`
	Version string `json:"version" yaml:"version"`
	Asset   string `json:"asset" yaml:"asset"`
}

func (s *GithubReleaseSource) SourceType() string {
	return "githubRelease"
}

func (s *GithubReleaseSource) Validate() error {
	if s.Type != "githubRelease" {
		return fmt.Errorf("unsupported source type %q: only githubRelease is supported", s.Type)
	}
	if s.Repo == "" || !strings.Contains(s.Repo, "/") {
		return fmt.Errorf("source.repo must be specified as <owner>/<repo>, got %q", s.Repo)
	}
	if s.Version == "" {
		return fmt.Errorf("source.version must be specified")
	}
	if s.Asset == "" {
		return fmt.Errorf("source.asset must be specified")
	}
	return nil
}

func (s *GithubReleaseSource) DefaultDestination() string {
	return filepath.Join("vendor", s.Asset)
}

func (s *GithubReleaseSource) Key() string {
	return strings.Join([]string{s.SourceType(), s.Repo, s.Version, s.Asset}, "\x00")
}

func (s *GithubReleaseSource) Download(ctx context.Context, fetcher Fetcher, dest string) (string, error) {
	data, err := fetcher.Fetch(ctx, s.Repo, s.Version, s.Asset)
	if err != nil {
		return "", fmt.Errorf("failed to fetch asset %s@%s: %w", s.Repo, s.Version, err)
	}
	return saveVendoredFile(dest, data)
}

// HTTPSource downloads a resource from a generic HTTP or HTTPS URL.
type HTTPSource struct {
	Type string `json:"type" yaml:"type"`
	URL  string `json:"url" yaml:"url"`
}

func (s *HTTPSource) SourceType() string {
	return "http"
}

func (s *HTTPSource) Validate() error {
	if s.Type != "http" {
		return fmt.Errorf("unsupported source type %q: only http is supported", s.Type)
	}
	if s.URL == "" {
		return fmt.Errorf("source.url must be specified")
	}
	parsed, err := url.Parse(s.URL)
	if err != nil {
		return fmt.Errorf("source.url %q is invalid: %w", s.URL, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("source.url scheme must be http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return fmt.Errorf("source.url %q must include a host", s.URL)
	}
	return nil
}

func (s *HTTPSource) DefaultDestination() string {
	parsed, err := url.Parse(s.URL)
	if err != nil {
		return ""
	}
	clean := path.Clean(parsed.Path)
	base := path.Base(clean)
	if base == "" || base == "." || base == "/" || base == ".." {
		return ""
	}
	return filepath.Join("vendor", base)
}

func (s *HTTPSource) Key() string {
	return strings.Join([]string{s.SourceType(), s.URL}, "\x00")
}

func (s *HTTPSource) Download(ctx context.Context, fetcher Fetcher, dest string) (string, error) {
	data, err := fetcher.FetchURL(ctx, s.URL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch from %s: %w", s.URL, err)
	}
	return saveVendoredFile(dest, data)
}

func saveVendoredFile(dest string, data []byte) (string, error) {
	hash := fmt.Sprintf("sha256:%x", sha256.Sum256(data))

	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		return "", fmt.Errorf("failed to create destination directory for %s: %w", dest, err)
	}
	if err := writeFileAtomic(dest, data, 0644); err != nil {
		return "", fmt.Errorf("failed to write vendored file %s: %w", dest, err)
	}

	return hash, nil
}

type sourceConfig struct {
	Type    string `json:"type" yaml:"type"`
	Repo    string `json:"repo,omitempty" yaml:"repo,omitempty"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	Asset   string `json:"asset,omitempty" yaml:"asset,omitempty"`
	URL     string `json:"url,omitempty" yaml:"url,omitempty"`
}

func (c sourceConfig) source() (Source, error) {
	if c.Type == "" {
		return nil, nil
	}
	switch c.Type {
	case "githubRelease":
		return &GithubReleaseSource{
			Type:    c.Type,
			Repo:    c.Repo,
			Version: c.Version,
			Asset:   c.Asset,
		}, nil
	case "http":
		return &HTTPSource{
			Type: c.Type,
			URL:  c.URL,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported source type %q: only githubRelease and http are supported", c.Type)
	}
}

type SpecItem struct {
	Source      Source `json:"source" yaml:"source"`
	Destination string `json:"destination,omitempty" yaml:"destination,omitempty"`
}

type specItemConfig struct {
	Source      sourceConfig `json:"source" yaml:"source"`
	Destination string       `json:"destination,omitempty" yaml:"destination,omitempty"`
}

func (s *SpecItem) UnmarshalJSON(b []byte) error {
	var cfg specItemConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	src, err := cfg.Source.source()
	if err != nil {
		return err
	}
	if src == nil {
		return fmt.Errorf("source.type must be specified")
	}
	s.Source = src
	s.Destination = cfg.Destination
	return nil
}

func (s *SpecItem) UnmarshalYAML(value *goyaml.Node) error {
	var cfg specItemConfig
	if err := value.Decode(&cfg); err != nil {
		return err
	}
	src, err := cfg.Source.source()
	if err != nil {
		return err
	}
	if src == nil {
		return fmt.Errorf("source.type must be specified")
	}
	s.Source = src
	s.Destination = cfg.Destination
	return nil
}

type StatusItem struct {
	Destination string `json:"destination" yaml:"destination"`
	Hash        string `json:"hash" yaml:"hash"`
	Source      Source `json:"source" yaml:"source"`
}

type statusItemConfig struct {
	Destination string       `json:"destination" yaml:"destination"`
	Hash        string       `json:"hash" yaml:"hash"`
	Source      sourceConfig `json:"source" yaml:"source"`
}

func (s *StatusItem) UnmarshalJSON(b []byte) error {
	var cfg statusItemConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return err
	}
	src, err := cfg.Source.source()
	if err != nil {
		return err
	}
	s.Destination = cfg.Destination
	s.Hash = cfg.Hash
	s.Source = src
	return nil
}

func (s *StatusItem) UnmarshalYAML(value *goyaml.Node) error {
	var cfg statusItemConfig
	if err := value.Decode(&cfg); err != nil {
		return err
	}
	src, err := cfg.Source.source()
	if err != nil {
		return err
	}
	s.Destination = cfg.Destination
	s.Hash = cfg.Hash
	s.Source = src
	return nil
}

type SpecUnion struct {
	Items    []SpecItem
	IsSingle bool
}

func (s *SpecUnion) UnmarshalJSON(b []byte) error {
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 {
		return fmt.Errorf("spec must not be empty")
	}
	if trimmed[0] == '[' {
		s.IsSingle = false
		return json.Unmarshal(trimmed, &s.Items)
	}

	s.IsSingle = true
	var single SpecItem
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return err
	}
	s.Items = []SpecItem{single}
	return nil
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
	trimmed := bytes.TrimSpace(b)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil
	}
	if trimmed[0] == '[' {
		s.IsSingle = false
		return json.Unmarshal(trimmed, &s.Items)
	}

	s.IsSingle = true
	var single StatusItem
	if err := json.Unmarshal(trimmed, &single); err != nil {
		return err
	}
	s.Items = []StatusItem{single}
	return nil
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

// Fetcher retrieves the raw bytes of an asset.
type Fetcher interface {
	Fetch(ctx context.Context, repo, version, asset string) ([]byte, error)
	FetchURL(ctx context.Context, url string) ([]byte, error)
}

type HTTPFetcher struct {
	Client *http.Client
}

func (d *HTTPFetcher) Fetch(ctx context.Context, repo, version, asset string) ([]byte, error) {
	url := fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, version, asset)
	if baseURL := os.Getenv("GITHUB_BASE_URL"); baseURL != "" {
		url = fmt.Sprintf("%s/%s/releases/download/%s/%s", strings.TrimRight(baseURL, "/"), repo, version, asset)
	}
	return d.FetchURL(ctx, url)
}

func (d *HTTPFetcher) FetchURL(ctx context.Context, rawURL string) ([]byte, error) {
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request for %s: %w", rawURL, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to download %s: %w", rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download from %s returned status %d %s", rawURL, resp.StatusCode, resp.Status)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body from %s: %w", rawURL, err)
	}

	return data, nil
}

func (r *API) fetcher() Fetcher {
	if r.Fetcher != nil {
		return r.Fetcher
	}
	return &HTTPFetcher{}
}

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
		if src == nil {
			return nil, fmt.Errorf("source must be specified")
		}
		if err := src.Validate(); err != nil {
			return nil, err
		}

		dest := specItem.Destination
		if dest == "" {
			dest = src.DefaultDestination()
		}
		if !filepath.IsLocal(dest) {
			return nil, fmt.Errorf("destination %q must be a relative path inside the kustomization directory", dest)
		}

		// Check cache hit
		cacheHit := false
		var fileContent []byte

		if existingStatus, ok := statusByDest[dest]; ok {
			sourceMatches := existingStatus.Source != nil && existingStatus.Source.Key() == src.Key()
			if sourceMatches && existingStatus.Hash != "" {
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
			hash, err := src.Download(ctx, r.fetcher(), dest)
			if err != nil {
				return nil, err
			}

			fileContent, err = os.ReadFile(dest)
			if err != nil {
				return nil, fmt.Errorf("failed to read vendored file %s: %w", dest, err)
			}

			newStatus := StatusItem{
				Destination: dest,
				Hash:        hash,
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
		if err := updateStatusInFile(genPath, updatedStatus, r); err != nil {
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
		if matchesGenerator(os.Args[1], api.APIVersion, api.Metadata.Name) {
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
					if matchesGenerator(p, api.APIVersion, api.Metadata.Name) {
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
				if matchesGenerator(name, api.APIVersion, api.Metadata.Name) {
					return name, nil
				}
			}
		}
	}

	return "", fmt.Errorf("could not find generator file matching name %q", api.Metadata.Name)
}

func matchesGenerator(filePath, apiVersion, expectedName string) bool {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}
	docs, err := decodeDocuments(data)
	if err != nil {
		return false
	}
	return findGeneratorMapping(docs, apiVersion, expectedName) != nil
}

func decodeDocuments(data []byte) ([]*goyaml.Node, error) {
	decoder := goyaml.NewDecoder(bytes.NewReader(data))
	var docs []*goyaml.Node
	for {
		var doc goyaml.Node
		if err := decoder.Decode(&doc); err != nil {
			if err == io.EOF {
				return docs, nil
			}
			return nil, err
		}
		docs = append(docs, &doc)
	}
}

func findGeneratorMapping(docs []*goyaml.Node, apiVersion, name string) *goyaml.Node {
	for _, doc := range docs {
		for _, mapping := range doc.Content {
			if mapping.Kind != goyaml.MappingNode {
				continue
			}
			if generatorMappingMatches(mapping, apiVersion, name) {
				return mapping
			}
		}
	}
	return nil
}

func generatorMappingMatches(mapping *goyaml.Node, apiVersion, name string) bool {
	var foundAPIVersion, foundKind, foundName string
	for i := 0; i < len(mapping.Content); i += 2 {
		key := mapping.Content[i].Value
		value := mapping.Content[i+1]
		switch key {
		case "apiVersion":
			foundAPIVersion = value.Value
		case "kind":
			foundKind = value.Value
		case "metadata":
			if value.Kind != goyaml.MappingNode {
				continue
			}
			for j := 0; j < len(value.Content); j += 2 {
				if value.Content[j].Value == "name" {
					foundName = value.Content[j+1].Value
				}
			}
		}
	}
	return foundAPIVersion == apiVersion &&
		foundKind == "VendorKustomization" &&
		foundName == name
}

func updateStatusInFile(filePath string, statusItems []StatusItem, api *API) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	docs, err := decodeDocuments(data)
	if err != nil {
		return err
	}
	if len(docs) == 0 {
		return fmt.Errorf("empty yaml document in %s", filePath)
	}

	targetMapping := findGeneratorMapping(docs, api.APIVersion, api.Metadata.Name)
	if targetMapping == nil {
		return fmt.Errorf(
			"no VendorKustomization %q with apiVersion %q found in %s",
			api.Metadata.Name,
			api.APIVersion,
			filePath,
		)
	}

	// Detect if spec in targetMapping is single or sequence to preserve structure
	isSingle := api.Spec.IsSingle
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
	for _, doc := range docs {
		if err := enc.Encode(doc); err != nil {
			return err
		}
	}
	if err := enc.Close(); err != nil {
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
