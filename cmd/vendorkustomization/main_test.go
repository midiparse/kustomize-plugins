package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

type mockDownloader struct {
	called bool
	data   []byte
	err    error
}

func (m *mockDownloader) Download(ctx context.Context, repo, version, asset string) ([]byte, error) {
	m.called = true
	return m.data, m.err
}

func TestCacheHitWithoutNetwork(t *testing.T) {
	origDir, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(origDir) }()

	tmpDir := t.TempDir()
	require.NoError(t, os.Chdir(tmpDir))

	manifestContent := `apiVersion: v1
kind: ConfigMap
metadata:
  name: cached-cm
data:
  key: cached-val
`
	vendorPath := filepath.Join("vendor", "test-asset.yaml")
	require.NoError(t, os.MkdirAll(filepath.Dir(vendorPath), 0755))
	require.NoError(t, os.WriteFile(vendorPath, []byte(manifestContent), 0644))

	hash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(manifestContent)))

	genContent := fmt.Sprintf(`apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: VendorKustomization
metadata:
  name: test-cache-hit
spec:
  source:
    type: githubRelease
    repo: example/test-repo
    # renovate: datasource=github-releases depName=example/test-repo
    version: v1.0.0
    asset: test-asset.yaml
status:
  destination: %s
  hash: %s
  source:
    type: githubRelease
    repo: example/test-repo
    version: v1.0.0
    asset: test-asset.yaml
`, vendorPath, hash)
	genPath := filepath.Join(tmpDir, "generator.yaml")
	require.NoError(t, os.WriteFile(genPath, []byte(genContent), 0644))

	mock := &mockDownloader{}
	oldDownloader := defaultDownloader
	defaultDownloader = mock
	defer func() { defaultDownloader = oldDownloader }()

	api := &API{
		Metadata: Metadata{Name: "test-cache-hit"},
		Spec: SpecUnion{
			IsSingle: true,
			Items: []SpecItem{
				{
					Source: Source{
						Type:    "githubRelease",
						Repo:    "example/test-repo",
						Version: "v1.0.0",
						Asset:   "test-asset.yaml",
					},
					Destination: vendorPath,
				},
			},
		},
		Status: StatusUnion{
			IsSingle: true,
			Items: []StatusItem{
				{
					Destination: vendorPath,
					Hash:        hash,
					Source: Source{
						Type:    "githubRelease",
						Repo:    "example/test-repo",
						Version: "v1.0.0",
						Asset:   "test-asset.yaml",
					},
				},
			},
		},
	}

	nodes, err := api.Filter(nil)
	require.NoError(t, err)
	assert.False(t, mock.called, "downloader should NOT be called on cache hit")
	require.Len(t, nodes, 1)
	assert.Equal(t, "ConfigMap", nodes[0].GetKind())
	assert.Equal(t, "cached-cm", nodes[0].GetName())

	// Verify generator file content was untouched
	afterContent, err := os.ReadFile(genPath)
	require.NoError(t, err)
	assert.Equal(t, genContent, string(afterContent))
}

func TestCacheMissDownloadAndStatusWriteBackWithComments(t *testing.T) {
	origDir, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(origDir) }()

	tmpDir := t.TempDir()
	require.NoError(t, os.Chdir(tmpDir))

	genContent := `apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: VendorKustomization
metadata:
  name: agent-sandbox
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: kustomize-plugin-vendorkustomization
spec:
  source:
    type: githubRelease
    repo: kubernetes-sigs/agent-sandbox
    # renovate: datasource=github-releases depName=kubernetes-sigs/agent-sandbox
    version: v1.0.7
    asset: sandbox-with-extensions.yaml
`
	genPath := filepath.Join(tmpDir, "agent-sandbox.yaml")
	require.NoError(t, os.WriteFile(genPath, []byte(genContent), 0644))

	kustContent := `apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
generators:
  - agent-sandbox.yaml
`
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "kustomization.yaml"), []byte(kustContent), 0644))

	downloadedYaml := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: sandbox-controller
spec:
  replicas: 1
---
apiVersion: v1
kind: Service
metadata:
  name: sandbox-service
`
	mock := &mockDownloader{data: []byte(downloadedYaml)}
	oldDownloader := defaultDownloader
	defaultDownloader = mock
	defer func() { defaultDownloader = oldDownloader }()

	api := &API{
		Metadata: Metadata{Name: "agent-sandbox"},
		Spec: SpecUnion{
			IsSingle: true,
			Items: []SpecItem{
				{
					Source: Source{
						Type:    "githubRelease",
						Repo:    "kubernetes-sigs/agent-sandbox",
						Version: "v1.0.7",
						Asset:   "sandbox-with-extensions.yaml",
					},
				},
			},
		},
	}

	nodes, err := api.Filter(nil)
	require.NoError(t, err)
	assert.True(t, mock.called, "downloader should have been called on cache miss")
	require.Len(t, nodes, 2)
	assert.Equal(t, "Deployment", nodes[0].GetKind())
	assert.Equal(t, "Service", nodes[1].GetKind())

	// Verify file was written to default destination vendor/sandbox-with-extensions.yaml
	expectedDest := filepath.Join("vendor", "sandbox-with-extensions.yaml")
	writtenBytes, err := os.ReadFile(expectedDest)
	require.NoError(t, err)
	assert.Equal(t, downloadedYaml, string(writtenBytes))

	// Verify status write-back to agent-sandbox.yaml
	updatedGenBytes, err := os.ReadFile(genPath)
	require.NoError(t, err)
	updatedGenStr := string(updatedGenBytes)

	assert.Contains(t, updatedGenStr, "# renovate: datasource=github-releases depName=kubernetes-sigs/agent-sandbox")
	assert.Contains(t, updatedGenStr, "status:")
	assert.Contains(t, updatedGenStr, "destination: vendor/sandbox-with-extensions.yaml")
	expectedHash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(downloadedYaml)))
	assert.Contains(t, updatedGenStr, expectedHash)

	// Second run: should be a cache hit now!
	mock.called = false
	var updatedAPI API
	require.NoError(t, yaml.Unmarshal(updatedGenBytes, &updatedAPI))
	nodes2, err := updatedAPI.Filter(nil)
	require.NoError(t, err)
	assert.False(t, mock.called, "second run should be cache hit")
	require.Len(t, nodes2, 2)
}

func TestArraySourcesSupport(t *testing.T) {
	origDir, err := os.Getwd()
	require.NoError(t, err)
	defer func() { _ = os.Chdir(origDir) }()

	tmpDir := t.TempDir()
	require.NoError(t, os.Chdir(tmpDir))

	genContent := `apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: VendorKustomization
metadata:
  name: multi-source
spec:
  - source:
      type: githubRelease
      repo: repo1/proj
      version: v1.0.0
      asset: first.yaml
    destination: custom/first.yaml
  - source:
      type: githubRelease
      repo: repo2/proj
      version: v2.0.0
      asset: second.yaml
`
	genPath := filepath.Join(tmpDir, "multi.yaml")
	require.NoError(t, os.WriteFile(genPath, []byte(genContent), 0644))

	firstData := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm-first\n"
	secondData := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: cm-second\n"

	mock := &mockDownloader{}
	oldDownloader := defaultDownloader
	defaultDownloader = &mockMultiDownloader{
		responses: map[string][]byte{
			"repo1/proj@v1.0.0/first.yaml":  []byte(firstData),
			"repo2/proj@v2.0.0/second.yaml": []byte(secondData),
		},
	}
	defer func() { defaultDownloader = oldDownloader }()

	var api API
	require.NoError(t, yaml.Unmarshal([]byte(genContent), &api))
	assert.False(t, api.Spec.IsSingle)
	require.Len(t, api.Spec.Items, 2)

	nodes, err := api.Filter(nil)
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	assert.Equal(t, "cm-first", nodes[0].GetName())
	assert.Equal(t, "cm-second", nodes[1].GetName())

	// Verify multi-status sequence write-back
	updatedGenBytes, err := os.ReadFile(genPath)
	require.NoError(t, err)
	updatedGenStr := string(updatedGenBytes)
	assert.Contains(t, updatedGenStr, "- destination: custom/first.yaml")
	assert.Contains(t, updatedGenStr, "- destination: vendor/second.yaml")

	_ = mock
}

type mockMultiDownloader struct {
	responses map[string][]byte
}

func (m *mockMultiDownloader) Download(ctx context.Context, repo, version, asset string) ([]byte, error) {
	key := fmt.Sprintf("%s@%s/%s", repo, version, asset)
	if data, ok := m.responses[key]; ok {
		return data, nil
	}
	return nil, fmt.Errorf("not found: %s", key)
}

func TestHTTPDownloaderLiveServer(t *testing.T) {
	manifest := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: test-ns\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/test-asset.yaml") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(manifest))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	t.Setenv("GITHUB_BASE_URL", server.URL)

	dl := &HTTPDownloader{}
	data, err := dl.Download(context.Background(), "myorg/myrepo", "v1.2.3", "test-asset.yaml")
	require.NoError(t, err)
	assert.Equal(t, manifest, string(data))

	// Test 404
	_, err = dl.Download(context.Background(), "myorg/myrepo", "v1.2.3", "notfound.yaml")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestInputFiltering(t *testing.T) {
	api := &API{
		Metadata: Metadata{Name: "my-generator"},
		Spec: SpecUnion{
			IsSingle: true,
			Items: []SpecItem{
				{
					Source: Source{
						Type:    "githubRelease",
						Repo:    "my/repo",
						Version: "v1.0.0",
						Asset:   "res.yaml",
					},
				},
			},
		},
	}

	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	require.NoError(t, os.Chdir(tmpDir))
	defer func() { _ = os.Chdir(origDir) }()

	genYaml := `apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: VendorKustomization
metadata:
  name: my-generator
spec:
  source:
    type: githubRelease
    repo: my/repo
    version: v1.0.0
    asset: res.yaml
`
	require.NoError(t, os.WriteFile("my-generator.yaml", []byte(genYaml), 0644))

	downloaded := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: my-cm\n"
	oldDownloader := defaultDownloader
	defaultDownloader = &mockDownloader{data: []byte(downloaded)}
	defer func() { defaultDownloader = oldDownloader }()

	inputGenNode, err := yaml.Parse(genYaml)
	require.NoError(t, err)
	existingNode, err := yaml.Parse("apiVersion: v1\nkind: Service\nmetadata:\n  name: existing-svc\n")
	require.NoError(t, err)

	inputItems := []*yaml.RNode{inputGenNode, existingNode}
	out, err := api.Filter(inputItems)
	require.NoError(t, err)

	require.Len(t, out, 2)
	assert.Equal(t, "existing-svc", out[0].GetName())
	assert.Equal(t, "my-cm", out[1].GetName())
}

func TestValidationErrors(t *testing.T) {
	api := &API{
		Spec: SpecUnion{
			IsSingle: true,
			Items: []SpecItem{
				{
					Source: Source{
						Type: "invalidType",
					},
				},
			},
		},
	}
	_, err := api.Filter(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported source type")

	api.Spec.Items[0].Source.Type = "githubRelease"
	api.Spec.Items[0].Source.Repo = "no-slash"
	_, err = api.Filter(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "<owner>/<repo>")
}
