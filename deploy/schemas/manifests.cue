package manifests

import (
    "strings"
)

// Represents an object that will be unified with a config
#Object: {
    _config: {...}
    ...
}

// represents a single output file
#ManifestFile: {
    _meta: {
        visible: *true | false
        folder: *"" | string
        file_prefix: *"" | string
        order_prefix: *"" | string
        suffix: *".yaml" | string
        name: string
        filename: strings.Join([file_prefix, order_prefix, name], "") + suffix
        path: strings.Join([folder, filename], "/")
    }

    // config should be supplied when constructing the file - it will be passed into each object
    config: {...}

    // stream contains the set of objects to be converted into a single file
    _stream: [...#Object]

    // objects processes objects in stream with the config for the file
    objects: [for s in _stream { s & {_config: config}}]
}
