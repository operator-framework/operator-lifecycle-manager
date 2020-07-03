package deploy

import (
	"tool/file"
	"tool/exec"
	"tool/cli"
	"encoding/yaml"
)

command: generate: {
    importFiles: {
        setupCRDFile: exec.Run & {
            cmd: ["sh", "-c", #"echo "package manifests" > schemas/crds.cue"#]
        }

        importCRDs: exec.Run & {
            $after: setupCRDFile
            cmd: ["sh", "-c", #"cue import -f -l '"crds": "\(spec.names.kind)": ' -n  ../vendor/github.com/operator-framework/api/crds/*.yaml -o - >> schemas/crds.cue"#]
        }
    }

	for m in files {
		"generate_\(m._meta.path)": {
		    print: cli.Print & {
                text: "generating \(m._meta.path)"
		    }
		    create: file.Create & {
                $after: importFiles
                filename: m._meta.path
                contents: yaml.MarshalStream(m.objects)
            }
		}
	}
}
