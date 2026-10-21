# VendorKustomization

The `VendorKustomization` is a Kustomize generator that vendors third-party Kubernetes manifests from GitHub releases
and emits them into the build. It records the source and a hash of each downloaded asset in a `status` field,
so subsequent builds can reuse the vendored file without hitting the network.

JSON Schema: [`schema/vendorkustomization.schema.json`](../schema/vendorkustomization.schema.json)

## How it works

The plugin operates as a Kustomize exec generator. For each entry in `spec` it:

1. Resolves the destination path, either from `destination` or as `vendor/<asset>`.
2. Looks for a matching entry in `status` for that destination.
3. Treats the item as a cache hit when:
   - the file exists at the destination,
   - the `status` entry records the same `source`, and
   - the file's hash matches the recorded `hash`.
4. On a cache hit, reads the vendored file from disk and performs no network requests.
5. On a cache miss, downloads
   `https://github.com/<repo>/releases/download/<version>/<asset>`, hashes it, writes it to the destination, and
   records the new `status` entry in the generator file.
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

The generator resource describes one or more assets to vendor:

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
status:
  - destination: vendor/sandbox-with-extensions.yaml
    hash: sha256:917727e6592a27b50fdd5d740eab3689654b3b19764dccf48115536badeb4f23
    source:
      type: githubRelease
      repo: kubernetes-sigs/agent-sandbox
      version: v1.0.7
      asset: sandbox-with-extensions.yaml
```

`spec` accepts either a single item or a list of items. When a list is used, `status` is written back as a list as
well; a single item is written back as a single mapping.

## Fields

- `spec`: A single item or a list of items.
- `spec[].source.type`: The source type. Only `githubRelease` is supported.
- `spec[].source.repo`: The GitHub repository in `<owner>/<repo>` form.
- `spec[].source.version`: The release tag to download.
- `spec[].source.asset`: The release asset filename.
- `spec[].destination`: (Optional) Where to write the asset, relative to the directory containing `kustomization.yaml`.
  Defaults to `vendor/<asset>`.
- `status[].destination`: The path that was written.
- `status[].hash`: The SHA-256 of the vendored file, in `sha256:<hex>` form.
- `status[].source`: A copy of the `source` that produced the file.

The `status` field is managed by the plugin. Add `status` entries to the generator file under version control so
builds can be reproduced without downloading the asset again.

## Status write-back

When an asset is downloaded, the plugin updates the `status` field of the generator file on disk. It locates the file
by checking, in order:

1. An explicit `config.kolobok.szakallas.eu/file-path` or `kustomize-plugins.midiparse.github.com/file-path`
   annotation on the generator resource.
2. The path annotations injected by Kustomize.
3. The `generators` entries in `kustomization.yaml`.
4. YAML files in the current directory that contain a matching `VendorKustomization`.

The update is performed at the YAML node level, so comments elsewhere in the file are preserved.

> [!TIP]
> Pin `source.version` with a Renovate comment so version bumps are proposed automatically:
>
> ```yaml
> # renovate: datasource=github-releases depName=kubernetes-sigs/agent-sandbox
> version: v1.0.7
> ```
>
> Renovate updates the `version` field in place, and the plugin re-downloads the asset on the next build because the
> recorded `status.source.version` no longer matches.
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
