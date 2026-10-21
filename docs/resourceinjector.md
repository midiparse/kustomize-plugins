# ResourceInjector

The `ResourceInjector` is a Kustomize plugin designed to render a Kustomize source and inject the resulting YAML
into specified fields of other resources. This allows for dynamic configuration and management of Kubernetes resources
by embedding the output of one Kustomize build into others.

JSON Schema: [`schema/resourceinjector.schema.json`](../schema/resourceinjector.schema.json)

## How it works

The plugin operates as a Kustomize function and is configured through a custom resource definition (CRD) within your
Kustomize setup. It performs the following steps:

1. **Renders a Kustomize Source**: It takes a path to a Kustomize source directory, builds it, and captures the
   resulting YAML output.
2. **Selects Target Resources**: It identifies target resources within the Kustomize build using a selector based on
   properties like `apiVersion`, `kind`, and `name`.
3. **Injects Content**: It injects the rendered YAML from the source as a string into one or more specified fields of
   the selected target resources.

## Configuration

The `ResourceInjector` is configured using a YAML file that defines the `source` to be rendered and the `targets` for
injection.

Here is an example of a `ResourceInjector` configuration:

```yaml
apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: ResourceInjector
metadata:
  name: inject
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: kustomize-plugin-resourceinjector
spec:
  source:
    path: ../path/to/source/kustomization
    fieldPath: spec
  targets:
    - select:
        kind: ConfigMap
        name: my-configmap
      fieldPaths:
        - data.mykey
```

## Fields

- `spec.source.path`: The path to the Kustomize source directory to be rendered. This path is relative to the
  `kustomization.yaml` file that includes the plugin.
- `spec.source.fieldPath`: Optionally specify a field in the YAML to project.
- `spec.source.options`: (Optional) Kustomize build options applied when rendering the source directory.
  - `spec.source.options.reorder`: (Optional) Specifies the order in which resources should be output. Valid values:
    - `"legacy"` - Use legacy ordering
    - `"none"` - Respect the depth-first input order
    - `"unspecified"` - Use the kustomize default
  - `spec.source.options.addManagedbyLabel`: (Optional) A boolean that, if `true`, adds a `app.kubernetes.io/managed-by`
    label to all resources.
  - `spec.source.options.loadRestrictions`: (Optional) Specifies restrictions on where files can be loaded from. Valid values:
    - `"none"` - No restrictions, allowing absolute or relative paths outside the kustomization directory
    - `"rootOnly"` - Restrict file loads to the kustomization directory or below (default)
    - `"unknown"` - Unknown restriction mode
  - `spec.source.options.pluginConfig`: (Optional) Plugin configuration for the source build.
    - `spec.source.options.pluginConfig.pluginRestrictions`: (Optional) Specifies plugin restrictions. Valid values:
      - `"none"` - No restrictions, all plugins allowed
      - `"builtinsOnly"` - Only built-in plugins allowed
      - `"unknown"` - Unknown restriction mode
    - `spec.source.options.pluginConfig.fnpLoadingOptions.enableExec`: (Optional) A boolean that, if `true`, allows
      execution of external plugins (exec-based plugins).
    - `spec.source.options.pluginConfig.helmConfig.enabled`: (Optional) A boolean that, if `true`, enables Helm
      chart rendering.
- `spec.targets`: A list of target selectors to identify where the rendered content should be injected.
- `spec.targets.select`: A selector to identify the target resources. It supports fields like `group`, `version`,
  `kind`, `name`, and `namespace`.
- `spec.targets.fieldPaths`: A list of fields in the target resources where the rendered YAML should be injected. The
  content is injected as a string.
- `spec.targets.options.create`: (Optional) A boolean that, if `true`, creates the specified field if it does not
  already exist in the target resource.

## Usage

To use the `ResourceInjector` plugin, you need to include it in your `kustomization.yaml` as a generator or transformer.

Here is an example of how to use it in a `kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - my-app-deployment.yaml
  - my-app-configmap.yaml

transformers:
  - |-
    apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
    kind: ResourceInjector
    metadata:
      name: inject
      annotations:
        config.kubernetes.io/function: |
          exec:
            path: kustomize-plugin-resourceinjector
    spec:
      source:
        path: ../common-resources
      targets:
        - select:
            kind: ConfigMap
            name: my-app-configmap
          fieldPaths:
            - data.injected-config
```

In this example, the `ResourceInjector` will:

1. Build the Kustomize source located at `../common-resources`.
2. Find the `ConfigMap` named `my-app-configmap`.
3. Inject the rendered YAML from `../common-resources` into the `data.injected-config` field of the `ConfigMap`.

## Advanced configuration

You can fine-tune how the source is rendered by specifying kustomize options:

```yaml
apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: ResourceInjector
metadata:
  name: inject-with-options
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: kustomize-plugin-resourceinjector
spec:
  source:
    path: ../common-resources
    fieldPath: spec.template
    options:
      reorder: "none"
      addManagedByLabel: true
      loadRestrictions: "none"
      pluginConfig:
        pluginRestrictions: "none"
        fnpLoadingOptions:
          enableExec: true
        helmConfig:
          enabled: true
  targets:
    - select:
        kind: ConfigMap
        name: my-app-configmap
      fieldPaths:
        - data.template-spec
```

In this example:

- The source is rendered with `reorder: "none"` to respect the input order
- A managed-by label is added to all resources in the source
- File loading is unrestricted (`loadRestrictions: "none"`)
- All plugins are allowed, including external executables and Helm charts
