//go:build e2e

package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"

	"github.com/midiparse/kustomize-plugins/internal/testutils"
)

func TestVendorKustomization(t *testing.T) {
	testutils.TestE2E(t, "./.")
}

func TestDownloadAndWriteLock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := fmt.Fprint(w, "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: downloaded\n"); err != nil {
			t.Errorf("write manifest: %v", err)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	config := fmt.Sprintf(`apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: VendorKustomization
metadata:
  name: example
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: kustomize-plugin-vendorkustomization
spec:
  - source:
      type: http
      url: %s/asset.yaml
`, server.URL)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "generator.yaml"), []byte(config), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "kustomization.yaml"), []byte("apiVersion: kustomize.config.k8s.io/v1beta1\nkind: Kustomization\ngenerators:\n  - generator.yaml\n"), 0644))
	opts := krusty.MakeDefaultOptions()
	opts.PluginConfig = &types.PluginConfig{PluginRestrictions: types.PluginRestrictionsNone, FnpLoadingOptions: types.FnPluginLoadingOptions{EnableExec: true}}
	result, err := krusty.MakeKustomizer(opts).Run(filesys.MakeFsOnDisk(), dir)
	require.NoError(t, err)
	output, err := result.AsYaml()
	require.NoError(t, err)
	require.Contains(t, string(output), "name: downloaded")
	updated, err := os.ReadFile(filepath.Join(dir, "generator.yaml"))
	require.NoError(t, err)
	require.Equal(t, config, string(updated))
	lock, err := os.ReadFile(filepath.Join(dir, "vendor", "example.lock.yaml"))
	require.NoError(t, err)
	require.Contains(t, string(lock), "destination: vendor/example/asset.yaml")
}
