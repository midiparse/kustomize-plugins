# kustomize-plugins

KRM exec plugins for [Kustomize](https://kustomize.io).

## Plugins

- [ResourceInjector](docs/resourceinjector.md) - Render a Kustomize source and inject the resulting YAML into
  specified fields of other resources.
- [YqTransform](docs/yqtransform.md) - Apply yq expressions to transform specific fields in Kubernetes resources.
- [VendorKustomization](docs/vendorkustomization.md) - Vendor third-party release manifests and track their integrity
  hashes.

## Schemas

JSON Schemas for the plugin function configurations live in [`schema/`](schema/). The aggregate
[`kustomize-plugins.schema.json`](schema/kustomize-plugins.schema.json) matches any plugin resource. The Nix package
installs them under `share/kustomize-plugins/schema/`.

## Build

The plugins are built into `bin/`:

```sh
make all
```

Run the end-to-end and unit tests with:

```sh
make test
```
