# VendorKustomization

The `VendorKustomization` is a Kustomize generator that vendors third-party Kubernetes manifests from GitHub
releases or generic HTTP URLs and emits them into the build. It records the source and a hash of each downloaded
asset in a lock file, so subsequent builds can reuse the vendored file without hitting the network.

JSON Schema: [`schema/vendorkustomization.schema.json`](../schema/vendorkustomization.schema.json)

## How it works

The plugin operates as a Kustomize exec generator. For each entry in `spec` it:

1. Resolves the destination path, either from `destination`, as `vendor/<metadata.name>/<asset>` for
   `githubRelease`, or as `vendor/<metadata.name>/<filename>` derived from the URL for `http`.
2. Looks for a matching entry in the lock file for that destination.
3. Treats the item as a cache hit when:
   - the file exists at the destination,
   - the lock entry records the same `source`, and
   - the file's hash matches the recorded `hash`.
4. On a cache hit, reads the vendored file from disk and performs no network requests.
5. On a cache miss, downloads the resource, hashes it, writes it to the destination, and records it in the lock file.
6. Parses the vendored file and appends its resources to the build output.

Downloads are written to a temporary file and renamed into place, so an interrupted build does not leave a partial
asset at the destination.

## Configuration

The plugin is used as a generator in `kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

generators:
  - agent-sandbox.yaml
```

The generator resource describes one or more assets to vendor in the `spec` list.

## Sources

### `githubRelease`

Downloads an asset from a GitHub release. The destination defaults to `vendor/<metadata.name>/<asset>`.

| Field            | Description                                      |
| ---------------- | ------------------------------------------------ |
| `source.type`    | `githubRelease`                                  |
| `source.repo`    | The GitHub repository in `<owner>/<repo>` form.  |
| `source.version` | The release tag to download.                     |
| `source.asset`   | The release asset filename.                      |

```yaml
apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: VendorKustomization
metadata:
  name: agent-sandbox
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: kustomize-plugin-vendorkustomization
spec:
  - source:
      type: githubRelease
      repo: kubernetes-sigs/agent-sandbox
      version: v1.0.7
      asset: sandbox-with-extensions.yaml
```

### `http`

Downloads a resource from a generic HTTP or HTTPS URL. The destination defaults to
`vendor/<metadata.name>/<filename>`, derived from the URL path.

| Field         | Description                                             |
| ------------- | ------------------------------------------------------- |
| `source.type` | `http`                                                  |
| `source.url`  | The HTTP or HTTPS URL to fetch the resource from.       |

```yaml
apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: VendorKustomization
metadata:
  name: http-manifest
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: kustomize-plugin-vendorkustomization
spec:
  - source:
      type: http
      url: https://example.com/manifests/app.yaml
```

## Common fields

- `spec`: A list of items.
- `spec[].source.type`: The source type. Either `githubRelease` or `http`. See [Sources](#sources).
- `spec[].destination`: (Optional) Where to write the asset, relative to `vendor/<metadata.name>`.
  Overrides the per-source default.

## Lock file

The plugin stores a YAML list of records in `vendor/<metadata.name>.lock.yaml`. Each record contains the
`destination`, SHA-256 `hash`, and `source` of one vendored asset.

> [!TIP]
> Pin `source.version` with a Renovate comment so version bumps are proposed automatically:
>
> ```yaml
> # renovate: datasource=github-releases depName=kubernetes-sigs/agent-sandbox
> version: v1.0.7
> ```
>
> Renovate updates the `version` field in place, and the plugin re-downloads the asset on the next build because the
> locked `source.version` no longer matches.
>
> Generator files are not package manifests, so declare a custom manager. Adjust `managerFilePatterns` to match your
> generator file layout:
>
> ```json
> {
>   "customManagers": [
>     {
>       "customType": "regex",
>       "description": "Update vendored manifests version in vendor.yaml",
>       "managerFilePatterns": ["/(^|/)src(/.+)?/vendor\\.ya?ml$/"],
>       "matchStrings": [
>         "# renovate: datasource=(?<datasource>[^\\s]+) depName=(?<depName>[^\\s]+)(?: versioning=(?<versioning>[^\\s]+))?\\n\\s*version:\\s*[\"']?(?<currentValue>[^\"'\\s]+)[\"']?"
>       ]
>     }
>   ]
> }
> ```
