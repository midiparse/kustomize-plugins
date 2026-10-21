# YqTransform

The `YqTransform` is a Kustomize plugin designed to apply [yq](https://github.com/mikefarah/yq) expressions to
transform specific fields within Kubernetes resources. This allows for powerful in-place transformations such as
sorting arrays, filtering values, modifying nested structures, and more.

JSON Schema: [`schema/yqtransform.schema.json`](../schema/yqtransform.schema.json)

## How it works

The plugin operates as a Kustomize function and is configured through a custom resource definition (CRD) within your
Kustomize setup. It performs the following steps:

1. **Selects Target Fields**: It identifies specific fields within target resources using a selector based on
   properties like `apiVersion`, `kind`, `name`, and field paths.
2. **Applies yq Expression**: It applies the specified yq expression to transform each selected field in place.
3. **Updates Resources**: The transformed fields are written back to the resources, preserving the rest of the resource
   structure.

## Configuration

The `YqTransform` is configured using a YAML file that defines the `expression` to apply and the `targets` to select.

Here is an example of a `YqTransform` configuration:

```yaml
apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: YqTransform
metadata:
  name: sort-env-vars
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: kustomize-plugin-yqtransform
spec:
  expression: "sort_by(.name)"
  targets:
    - select:
        kind: Deployment
      fieldPaths:
        - spec.template.spec.containers.*.env
```

## Fields

- `spec.source.expression`: A yq expression to apply to the selected fields. The expression operates on each selected field
  independently.
- `spec.source.vars`: (Optional) A list of variables to be made available in the yq expression.
  - `name`: The name of the variable.
  - `sourceValue`: A string containing a YAML value to be used as the variable's value.
  - `source`: A selector to another resource to be used as the variable's value.
- `spec.targets`: A list of target selectors to identify which fields should be transformed.
- `spec.targets.select`: A selector to identify the target resources. It supports fields like `group`, `version`,
  `kind`, `name`, and `namespace`.
- `spec.targets.fieldPaths`: A list of field paths within the target resources to transform. Supports array wildcards
  like `[]` to apply the transformation to all array elements.
- `spec.targets.options.create`: (Optional) A boolean that, if `true`, creates the specified field if it does not
  already exist in the target resource.

## Usage

To use the `YqTransform` plugin, you need to include it in your `kustomization.yaml` as a transformer.

Here is an example of how to use it in a `kustomization.yaml`:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
resources:
  - deployment.yaml

transformers:
  - |-
    apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
    kind: YqTransform
    metadata:
      name: sort-env-vars
      annotations:
        config.kubernetes.io/function: |
          exec:
            path: kustomize-plugin-yqtransform
    spec:
      source:
        expression: "sort_by(.name)"
      targets:
        - select:
            kind: Deployment
            name: my-app
          fieldPaths:
            - spec.template.spec.containers.*.env
```

In this example, the `YqTransform` will:

1. Find the `Deployment` named `my-app`.
2. Locate all `env` arrays within the deployment's container specifications.
3. Apply the `sort_by(.name)` yq expression to sort environment variables by name in each container.

## Using variables

You can define variables and use them in your yq expressions.
This is useful for injecting values from other resources or from literal strings.

Here is an example of how to inject a field from another resource into a `ConfigMap`:

```yaml
apiVersion: kustomize-plugins.midiparse.github.com/v1alpha1
kind: YqTransform
metadata:
  name: inject
  annotations:
    config.kubernetes.io/function: |
      exec:
        path: kustomize-plugin-yqtransform
spec:
  source:
    vars:
      - name: source
        source:
          kind: CustomResource
          name: example
          fieldPath: spec
    expression: ".= ($source | @yaml)"
  targets:
  - select:
      kind: ConfigMap
    fieldPaths:
    - data.[inner.yaml]
    options:
      create: true
```

In this example:

1. A variable named `source` is created from the `spec` field of a `CustomResource` named `example`.
2. The yq expression `.= ($source | @yaml)` takes the `source` variable, converts it to
   a YAML string, and sets it as the value of the selected field.
3. The target is the `data.[inner.yaml]` field in a `ConfigMap`.

## Common use cases

**Sort environment variables:**

```yaml
source:
  expression: "sort_by(.name)"
targets:
- fieldPaths:
  - spec.template.spec.containers.*.env
```

**Inject another resource as a string:**

```yaml
source:
  vars:
    - name: example
      source:
        kind: CustomResource
        name: example
        fieldPath: spec
  expression: ".= ($example | @yaml)"
targets:
- select:
    kind: ConfigMap
  fieldPaths:
  - data.[inner.yaml]
  options:
    create: true
```

**Filter items from an array:**

```yaml
source:
  expression: "map(select(.name != \"DEBUG\"))"
targets:
- fieldPaths:
  - spec.template.spec.containers.*.env
```

**Add or modify fields:**

```yaml
source:
  expression: ". + {\"imagePullPolicy\": \"Always\"}"
targets:
- fieldPaths:
  - spec.template.spec.containers.*
```
