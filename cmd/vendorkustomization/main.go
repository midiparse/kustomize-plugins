package main

import (
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
	"reflect"
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
	APIVersion string     `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
	Kind       string     `json:"kind,omitempty" yaml:"kind,omitempty"`
	Metadata   Metadata   `json:"metadata" yaml:"metadata"`
	Spec       []SpecItem `json:"spec" yaml:"spec"`

	Client *http.Client `json:"-" yaml:"-"`
}

type Metadata struct {
	Name        string            `json:"name" yaml:"name"`
	Namespace   string            `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty" yaml:"annotations,omitempty"`
}

// Source is a type-specific asset source. Implementations validate their
// configuration, suggest a destination path, fetch the asset, and identify
// themselves for cache comparisons.
type Source interface {
	SourceType() string
	Validate() error
	DefaultDestination() string
	Fetch(ctx context.Context, client *http.Client) ([]byte, error)
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
	return s.Asset
}

func (s *GithubReleaseSource) Key() string {
	return strings.Join([]string{s.SourceType(), s.Repo, s.Version, s.Asset}, "\x00")
}

func (s *GithubReleaseSource) Fetch(ctx context.Context, client *http.Client) ([]byte, error) {
	baseURL := os.Getenv("GITHUB_BASE_URL")
	if baseURL == "" {
		baseURL = "https://github.com"
	}
	url := fmt.Sprintf("%s/%s/releases/download/%s/%s", strings.TrimRight(baseURL, "/"), s.Repo, s.Version, s.Asset)
	return fetchURL(ctx, client, url)
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
	return base
}

func (s *HTTPSource) Key() string {
	return strings.Join([]string{s.SourceType(), s.URL}, "\x00")
}

func (s *HTTPSource) Fetch(ctx context.Context, client *http.Client) ([]byte, error) {
	return fetchURL(ctx, client, s.URL)
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

type SpecItem struct {
	Source      Source `json:"source" yaml:"source"`
	Destination string `json:"destination,omitempty" yaml:"destination,omitempty"`
}

type StatusItem struct {
	Destination string `json:"destination" yaml:"destination"`
	Hash        string `json:"hash" yaml:"hash"`
	Source      Source `json:"source" yaml:"source"`
}

func decodeSource(raw json.RawMessage) (Source, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var kind struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &kind); err != nil {
		return nil, err
	}
	var src Source
	switch kind.Type {
	case "githubRelease":
		src = &GithubReleaseSource{}
	case "http":
		src = &HTTPSource{}
	case "":
		return nil, nil
	default:
		return nil, fmt.Errorf("unsupported source type %q: only githubRelease and http are supported", kind.Type)
	}
	if err := json.Unmarshal(raw, src); err != nil {
		return nil, err
	}
	return src, nil
}

func (s *SpecItem) UnmarshalJSON(data []byte) error {
	var item struct {
		Source      json.RawMessage `json:"source"`
		Destination string          `json:"destination"`
	}
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}
	src, err := decodeSource(item.Source)
	if err != nil {
		return err
	}
	if src == nil {
		return fmt.Errorf("source.type must be specified")
	}
	s.Source, s.Destination = src, item.Destination
	return nil
}

func (s *StatusItem) UnmarshalJSON(data []byte) error {
	var item struct {
		Source      json.RawMessage `json:"source"`
		Destination string          `json:"destination"`
		Hash        string          `json:"hash"`
	}
	if err := json.Unmarshal(data, &item); err != nil {
		return err
	}
	src, err := decodeSource(item.Source)
	if err != nil {
		return err
	}
	s.Source, s.Destination, s.Hash = src, item.Destination, item.Hash
	return nil
}

func unmarshalYAMLAsJSON(node *goyaml.Node, dst interface{ UnmarshalJSON([]byte) error }) error {
	var value any
	if err := node.Decode(&value); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return dst.UnmarshalJSON(data)
}

func (s *SpecItem) UnmarshalYAML(node *goyaml.Node) error   { return unmarshalYAMLAsJSON(node, s) }
func (s *StatusItem) UnmarshalYAML(node *goyaml.Node) error { return unmarshalYAMLAsJSON(node, s) }

func fetchURL(ctx context.Context, client *http.Client, rawURL string) ([]byte, error) {
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

// Filter reconciles each vendored asset and outputs the generated Kubernetes resources.
func (r *API) Filter(items []*yaml.RNode) ([]*yaml.RNode, error) {
	if len(r.Spec) == 0 {
		return nil, fmt.Errorf("spec must specify at least one source")
	}

	if r.Metadata.Name == "" || filepath.Base(r.Metadata.Name) != r.Metadata.Name || r.Metadata.Name == "." || r.Metadata.Name == ".." {
		return nil, fmt.Errorf("metadata.name must be a valid file name")
	}
	lockPath := filepath.Join("vendor", r.Metadata.Name+".lock.yaml")
	status, err := readLock(lockPath)
	if err != nil {
		return nil, err
	}
	statusByDest := make(map[string]StatusItem, len(status))
	for _, st := range status {
		statusByDest[st.Destination] = st
	}

	var updatedStatus []StatusItem
	var generatedNodes []*yaml.RNode

	ctx := context.Background()

	for _, specItem := range r.Spec {
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
			return nil, fmt.Errorf("destination %q must be a relative path inside the vendor directory", dest)
		}
		dest = filepath.Join("vendor", r.Metadata.Name, dest)

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
			var err error
			fileContent, err = src.Fetch(ctx, r.Client)
			if err != nil {
				return nil, err
			}

			hash, err := saveVendoredFile(dest, fileContent)
			if err != nil {
				return nil, err
			}

			newStatus := StatusItem{
				Destination: dest,
				Hash:        hash,
				Source:      src,
			}
			updatedStatus = append(updatedStatus, newStatus)
		}

		// Parse the vendored manifest file
		nodes, err := kio.FromBytes(fileContent)
		if err != nil {
			return nil, fmt.Errorf("failed to parse manifests from %s: %w", dest, err)
		}
		generatedNodes = append(generatedNodes, nodes...)
	}

	if !reflect.DeepEqual(status, updatedStatus) {
		if err := writeLock(lockPath, updatedStatus); err != nil {
			return nil, err
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

func readLock(path string) ([]StatusItem, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lock file %s: %w", path, err)
	}
	var status []StatusItem
	if err := goyaml.Unmarshal(data, &status); err != nil {
		return nil, fmt.Errorf("parse lock file %s: %w", path, err)
	}
	return status, nil
}

func writeLock(path string, status []StatusItem) error {
	data, err := goyaml.Marshal(status)
	if err != nil {
		return fmt.Errorf("encode lock file %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create lock directory: %w", err)
	}
	if err := writeFileAtomic(path, data, 0644); err != nil {
		return fmt.Errorf("write lock file %s: %w", path, err)
	}
	return nil
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
