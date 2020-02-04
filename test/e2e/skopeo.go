package e2e

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"time"
)

const (
	// robot credentials to access the catsrc-update-test quay repository
	// these are write-only and checks are made to ensure only the intended images are allowed to run
	creds       = "--screds=olmtest+e2etest:V1YASVAXKBCMX6FFKRXJ74T0XMWUEVLF8YYOX3V4BLXSR0LFZI5NDL2V16MNI813"
	dcreds      = "--dcreds=olmtest+e2etest:V1YASVAXKBCMX6FFKRXJ74T0XMWUEVLF8YYOX3V4BLXSR0LFZI5NDL2V16MNI813"
	deletecreds = "--creds=olmtest+e2etest:V1YASVAXKBCMX6FFKRXJ74T0XMWUEVLF8YYOX3V4BLXSR0LFZI5NDL2V16MNI813"
	insecure    = "--insecure-policy=true"
	skopeo      = "skopeo"
	debug       = "--debug"
)

func skopeoCopy(newImage, newTag string, oldImage, oldTag string) (string, error) {
	newImageName := fmt.Sprint(newImage, newTag)
	oldImageName := fmt.Sprint(oldImage, oldTag)
	cmd := exec.Command(skopeo, debug, insecure, "copy", creds, dcreds, oldImageName, newImageName)

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to exec %#v: %v", cmd.Args, err)
	}
	fmt.Println(string(out))

	return newImageName, nil
}

func skopeoDelete(image string) error {
	cmd := exec.Command(skopeo, "delete", deletecreds, image)

	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to exec %#v: %v", cmd.Args, err)
	}
	fmt.Println(string(out))

	return nil
}

type Digest string

// inspectOutput is the output format of (skopeo inspect), primarily so that we can format it with a simple json.MarshalIndent.
type inspectOutput struct {
	Name          string `json:",omitempty"`
	Tag           string `json:",omitempty"`
	Digest        Digest
	RepoTags      []string
	Created       *time.Time
	DockerVersion string
	Labels        map[string]string
	Architecture  string
	Os            string
	Layers        []string
	Env           []string
}

func skopeoInspectDigest(image string, sha string) (bool, error) {
	cmd := exec.Command(skopeo, debug, "inspect", image)
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("failed to exec %#v: %v", cmd.Args, err)
	}
	var skopeoOutput inspectOutput
	if err := json.Unmarshal(out, &skopeoOutput); err != nil {
		return false, err
	}
	if string(skopeoOutput.Digest) != sha {
		fmt.Printf("skopeoInspectDigest: expected: %s got: %s", sha, skopeoOutput.Digest)
		return false, nil
	}

	return true, nil
}
