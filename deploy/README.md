# Deploy Configuration

Installation manifests for OLM are defined with [cuelang](https://cuelang.org/).

## Quickstart

From this directory, manifests can generated with:

```sh
cue cmd generate
```

The resulting manifests will be in `../manifests` and `../kube`

## Options

Some values can / need to be specified when generating manifests. This uses `@tags` in cue and can be specified when
invoking the `generate command`. See [config.cue](config.cue) for the available tags.

Generate a debug configuration:

```sh
cue -t debug=true generate
```

## Layout

- Definitions: [schemas](schemas) contains the definitions of the objects generated for the manifests, and the definitions of the manifests themselves. These are shared specs that may be specialized further in [schemas/kube](schemas/kube) or [schemas/ocp](schemas/ocp)
- Manifest Defintions: within the schema folders, [schemas/manifests.cue](schemas/manifests.cue) defines how to combine object defintions into the final output files.
- Default values: [schamas/defaults.cue](schemas/defaults.cue) defines the default config values for all objects, while [schemas/kube/defaults.cue](schemas/kube/defaults.cue) and [schemas/ocp/defaults.cue](schemas/kube/defaults.cue) define the flavor-specific default config values.
- [config.cue](config.cue) exposes the user-facing knobs via `@tag` annotations and applies user config to the default configs and completes the definitions of all manifests.
- [deploy_tool.cue](deploy_tool.cue) contains the generation command definition which copies crd definitions from the vendored api directory and outputs the manifest files.
- [cue.mod/gen](cue.mod/gen) contains generated definitions from imported apis (i.e. `cue go get k8s.io/api/core/v1`)

## Troubleshooting / Tips

### Importing go dependencies

In order to use `cue get go X` you must first run `go get X`.

Cue must be installed with `go get` in order for imports of kube apis to work properly (see: https://github.com/cuelang/cue/issues/148)

Mostly, `cue get go` works without issue. For operator-framework imports, it doesn't correctly connect the api 
dependencies on kube apis, so the generated packages need to be augmented with a bridge between them, see 
[cue.mod/gen/github.com/operator-framework/api/pkg/operators/v1alpha1/meta.cue](deploy/cue.mod/gen/github.com/operator-framework/api/pkg/operators/v1alpha1/meta.cue) 
for an example.

### Unhelpful error messages

If cue reports errors on `cue cmd`, you will often get much more detailed messages if you `cue export`, `cue eval` or 
`cue def` the package that the error is coming from. i.e. if `cue cmd generate` is throwing a cryptic error, 
`cue def config.cue` likely has more information.

Errors sometimes seem to stop at package boundaries. If an error is coming from a specific package it can be 
helpful to evaluate it directly, i.e. `cue eval schemas/ocp`.

### Updating generated cuefiles

```sh
$ cue get go github.com/operator-framework/api/pkg/operators/v1
$ cue get go github.com/operator-framework/api/pkg/operators/v1alpha1
$ cue get go k8s.io/api/apps/v1 
$ cue get go k8s.io/api/core/v
```

