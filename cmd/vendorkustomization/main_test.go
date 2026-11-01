package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/kustomize/kyaml/yaml"
)

type mockFetcher struct {
	called bool
	data   []byte
	err    error
}

func (m *mockFetcher) RoundTrip(req *http.Request) (*http.Response, error) {
	m.called = true
	if m.err != nil {
		return nil, m.err
	}
	return &http.Response{StatusCode: 200, Status: "200 OK", Body: io.NopCloser(strings.NewReader(string(m.data))), Header: make(http.Header)}, nil
}

func TestHTTPFetcherLiveServer(t *testing.T) {
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

	dl := &GithubReleaseSource{Repo: "myorg/myrepo", Version: "v1.2.3", Asset: "test-asset.yaml"}
	data, err := dl.Fetch(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, manifest, string(data))

	// Test 404
	dl.Asset = "notfound.yaml"
	_, err = dl.Fetch(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestInputFiltering(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)

	downloaded := "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: my-cm\n"
	api := &API{
		APIVersion: "kustomize-plugins.midiparse.github.com/v1alpha1",
		Metadata:   Metadata{Name: "my-generator"},
		Client:     &http.Client{Transport: &mockFetcher{data: []byte(downloaded)}},
		Spec: []SpecItem{
			{
				Source: &GithubReleaseSource{
					Type:    "githubRelease",
					Repo:    "my/repo",
					Version: "v1.0.0",
					Asset:   "res.yaml",
				},
			},
		},
	}

	genYaml := `apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: VendorKustomization
metadata:
  name: my-generator
spec:
  - source:
      type: githubRelease
      repo: my/repo
      version: v1.0.0
      asset: res.yaml
`
	require.NoError(t, os.WriteFile("my-generator.yaml", []byte(genYaml), 0644))

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

func TestUnsupportedSourceTypeRejected(t *testing.T) {
	var api API
	err := yaml.Unmarshal([]byte(`apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: VendorKustomization
metadata:
  name: invalid
spec:
  - source:
      type: helm
      repo: example/repo
      version: v1.0.0
      asset: chart.yaml
`), &api)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported source type")
}

func TestValidationErrors(t *testing.T) {
	newAPI := func() *API {
		return &API{
			APIVersion: "kustomize-plugins.midiparse.github.com/v1alpha1",
			Metadata:   Metadata{Name: "validation"},
			Client:     &http.Client{Transport: &mockFetcher{}},
			Spec: []SpecItem{
				{
					Source: &GithubReleaseSource{
						Type:    "githubRelease",
						Repo:    "owner/repo",
						Version: "v1.0.0",
						Asset:   "asset.yaml",
					},
				},
			},
		}
	}

	tests := []struct {
		name   string
		mutate func(*API)
		want   string
	}{
		{
			name: "unsupported source type",
			mutate: func(api *API) {
				api.Spec[0].Source.(*GithubReleaseSource).Type = "invalidType"
			},
			want: "unsupported source type",
		},
		{
			name: "repo without owner",
			mutate: func(api *API) {
				api.Spec[0].Source.(*GithubReleaseSource).Repo = "no-slash"
			},
			want: "<owner>/<repo>",
		},
		{
			name: "missing version",
			mutate: func(api *API) {
				api.Spec[0].Source.(*GithubReleaseSource).Version = ""
			},
			want: "source.version must be specified",
		},
		{
			name: "missing asset",
			mutate: func(api *API) {
				api.Spec[0].Source.(*GithubReleaseSource).Asset = ""
			},
			want: "source.asset must be specified",
		},
		{
			name: "absolute destination",
			mutate: func(api *API) {
				api.Spec[0].Destination = "/tmp/asset.yaml"
			},
			want: "must be a relative path",
		},
		{
			name: "destination escapes project",
			mutate: func(api *API) {
				api.Spec[0].Destination = "../asset.yaml"
			},
			want: "must be a relative path",
		},
		{
			name: "default destination escapes project",
			mutate: func(api *API) {
				api.Spec[0].Source.(*GithubReleaseSource).Asset = "../../asset.yaml"
			},
			want: "must be a relative path",
		},
		{
			name: "http unsupported source type",
			mutate: func(api *API) {
				api.Spec[0].Source = &HTTPSource{
					Type: "notHttp",
					URL:  "https://example.com/res.yaml",
				}
			},
			want: "unsupported source type",
		},
		{
			name: "http missing url",
			mutate: func(api *API) {
				api.Spec[0].Source = &HTTPSource{
					Type: "http",
					URL:  "",
				}
			},
			want: "source.url must be specified",
		},
		{
			name: "http invalid url scheme",
			mutate: func(api *API) {
				api.Spec[0].Source = &HTTPSource{
					Type: "http",
					URL:  "ftp://example.com/res.yaml",
				}
			},
			want: "source.url scheme must be http or https",
		},
		{
			name: "http missing host",
			mutate: func(api *API) {
				api.Spec[0].Source = &HTTPSource{
					Type: "http",
					URL:  "http:///res.yaml",
				}
			},
			want: "must include a host",
		},
		{
			name: "http default destination cannot be determined",
			mutate: func(api *API) {
				api.Spec[0].Source = &HTTPSource{
					Type: "http",
					URL:  "http://example.com/",
				}
			},
			want: "must be a relative path",
		},
		{
			name: "http default destination escapes project",
			mutate: func(api *API) {
				api.Spec[0].Source = &HTTPSource{
					Type: "http",
					URL:  "http://example.com/..",
				}
			},
			want: "must be a relative path",
		},
		{
			name: "http absolute destination",
			mutate: func(api *API) {
				api.Spec[0].Source = &HTTPSource{
					Type: "http",
					URL:  "https://example.com/res.yaml",
				}
				api.Spec[0].Destination = "/tmp/res.yaml"
			},
			want: "must be a relative path",
		},
		{
			name: "http destination escapes project",
			mutate: func(api *API) {
				api.Spec[0].Source = &HTTPSource{
					Type: "http",
					URL:  "https://example.com/res.yaml",
				}
				api.Spec[0].Destination = "../res.yaml"
			},
			want: "must be a relative path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			api := newAPI()
			tt.mutate(api)

			_, err := api.Filter(nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestHTTPFetcherFetchURL(t *testing.T) {
	manifest := "apiVersion: v1\nkind: Namespace\nmetadata:\n  name: http-ns\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/raw/namespace.yaml" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(manifest))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	dl := &HTTPSource{URL: server.URL + "/raw/namespace.yaml"}
	data, err := dl.Fetch(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, manifest, string(data))

	// Test 404
	dl.URL = server.URL + "/raw/notfound.yaml"
	_, err = dl.Fetch(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "404")
}

func TestConfigurationRequiresLists(t *testing.T) {
	tests := []struct {
		name  string
		input string
		json  bool
	}{
		{name: "YAML spec mapping", input: "spec:\n  source:\n    type: http\n    url: https://example.com/test.yaml\n"},
		{name: "JSON spec mapping", input: `{"spec":{"source":{"type":"http","url":"https://example.com/test.yaml"}}}`, json: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var api API
			if tt.json {
				require.Error(t, json.Unmarshal([]byte(tt.input), &api))
				return
			}
			require.Error(t, yaml.Unmarshal([]byte(tt.input), &api))
		})
	}
}

func TestLockFileReconciliation(t *testing.T) {
	t.Chdir(t.TempDir())
	manifest := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: downloaded\n")
	mock := &mockFetcher{data: manifest}
	api := &API{Metadata: Metadata{Name: "example"}, Client: &http.Client{Transport: mock}, Spec: []SpecItem{
		{Source: &HTTPSource{Type: "http", URL: "https://example.com/one.yaml"}},
		{Source: &HTTPSource{Type: "http", URL: "https://example.com/two.yaml"}},
	}}
	nodes, err := api.Filter(nil)
	require.NoError(t, err)
	require.Len(t, nodes, 2)
	require.True(t, mock.called)
	lockPath := "vendor/example.lock.yaml"
	status, err := readLock(lockPath)
	require.NoError(t, err)
	require.Len(t, status, 2)
	assert.Equal(t, "vendor/example/one.yaml", status[0].Destination)
	assert.Equal(t, fmt.Sprintf("sha256:%x", sha256.Sum256(manifest)), status[0].Hash)
	lockBefore, err := os.ReadFile(lockPath)
	require.NoError(t, err)

	mock.called = false
	_, err = api.Filter(nil)
	require.NoError(t, err)
	assert.False(t, mock.called)
	lockAfter, err := os.ReadFile(lockPath)
	require.NoError(t, err)
	assert.Equal(t, lockBefore, lockAfter)

	api.Spec = api.Spec[:1]
	_, err = api.Filter(nil)
	require.NoError(t, err)
	assert.False(t, mock.called)
	status, err = readLock(lockPath)
	require.NoError(t, err)
	require.Len(t, status, 1)
	assert.Equal(t, "vendor/example/one.yaml", status[0].Destination)

	require.NoError(t, os.WriteFile("vendor/example/one.yaml", []byte("modified"), 0644))
	_, err = api.Filter(nil)
	require.NoError(t, err)
	assert.True(t, mock.called)

	mock.called = false
	api.Spec[0].Source = &HTTPSource{Type: "http", URL: "https://example.com/changed.yaml"}
	api.Spec[0].Destination = "one.yaml"
	_, err = api.Filter(nil)
	require.NoError(t, err)
	assert.True(t, mock.called)
	status, err = readLock(lockPath)
	require.NoError(t, err)
	assert.Equal(t, "https://example.com/changed.yaml", status[0].Source.(*HTTPSource).URL)
}

func TestInvalidLockDoesNotDownload(t *testing.T) {
	t.Chdir(t.TempDir())
	require.NoError(t, os.MkdirAll("vendor", 0755))
	require.NoError(t, os.WriteFile("vendor/example.lock.yaml", []byte("not: a list\n"), 0644))
	mock := &mockFetcher{}
	api := &API{Metadata: Metadata{Name: "example"}, Client: &http.Client{Transport: mock}, Spec: []SpecItem{{Source: &HTTPSource{Type: "http", URL: "https://example.com/one.yaml"}}}}
	_, err := api.Filter(nil)
	require.ErrorContains(t, err, "parse lock file")
	assert.False(t, mock.called)
}

func TestLockPathValidation(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{"", "..", "../outside"} {
		t.Run(name, func(t *testing.T) {
			api := &API{Metadata: Metadata{Name: name}, Spec: []SpecItem{{Source: &HTTPSource{Type: "http", URL: "https://example.com/one.yaml"}}}}
			_, err := api.Filter(nil)
			require.ErrorContains(t, err, "metadata.name")
		})
	}
}

func TestGeneratorVendorDirectories(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{"first", "second"} {
		manifest := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\n")
		mock := &mockFetcher{data: manifest}
		api := &API{Metadata: Metadata{Name: name}, Client: &http.Client{Transport: mock}, Spec: []SpecItem{
			{Source: &HTTPSource{Type: "http", URL: "https://example.com/asset.yaml"}},
			{Source: &HTTPSource{Type: "http", URL: "https://example.com/another.yaml"}, Destination: "nested/custom.yaml"},
		}}
		_, err := api.Filter(nil)
		require.NoError(t, err)
		for _, path := range []string{"vendor/" + name + "/asset.yaml", "vendor/" + name + "/nested/custom.yaml"} {
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, manifest, got)
		}
	}
}
