package testutils

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/kustomize/api/krusty"
	"sigs.k8s.io/kustomize/api/types"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

func TestE2E(t *testing.T, path string) {
	t.Helper()
	testDirs, err := os.ReadDir(path)
	require.NoError(t, err)

	for _, dir := range testDirs {
		if !dir.IsDir() {
			continue
		}
		testName := dir.Name()
		t.Run(testName, func(t *testing.T) {
			testDir := filepath.Join(path, testName)
			fixtureDir := filepath.Join(testDir, "fixture")
			outPath := filepath.Join(testDir, "out.yaml")

			opts := krusty.MakeDefaultOptions()
			opts.PluginConfig = &types.PluginConfig{
				PluginRestrictions: types.PluginRestrictionsNone,
				FnpLoadingOptions: types.FnPluginLoadingOptions{
					EnableExec: true,
				},
			}
			kustomizer := krusty.MakeKustomizer(opts)
			fSys := filesys.MakeFsOnDisk()
			resMap, err := kustomizer.Run(fSys, fixtureDir)
			require.NoError(t, err)
			yaml, err := resMap.AsYaml()
			require.NoError(t, err)

			var outFileExists bool
			if _, err := os.Stat(outPath); err == nil {
				outFileExists = true
			}

			if os.Getenv("TEST_REGEN") != "" || !outFileExists {
				err := os.WriteFile(outPath, yaml, 0644)
				require.NoError(t, err)
				t.Logf("regenerated %s", outPath)
				return
			}

			expected, err := os.ReadFile(outPath)
			require.NoError(t, err)

			// Normalize line endings for comparison
			expectedStr := strings.ReplaceAll(string(expected), "\r\n", "\n")
			actualStr := strings.ReplaceAll(string(yaml), "\r\n", "\n")

			assert.Equal(t, expectedStr, actualStr, "kustomize output does not match out.yaml")
		})
	}
}
