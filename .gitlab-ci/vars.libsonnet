local utils = import "utils.libsonnet";

{
    deploy_keys: { operator_client: "$OPERATORCLENT_RSA_B64" },
    olm_repo: "github.com/operator-framework/operator-lifecycle-manager",
    global: {
        // .gitlab-ci.yaml top `variables` key
        FAILFASTCI_NAMESPACE: "operator-framework",
        // increase attempts to handle occational auth failures against gitlab.com
        GET_SOURCES_ATTEMPTS: "10",
    },

    paths: {
        olm: {
            src: "$GOPATH/src/%s" % $.olm_repo,
        },
    },

    // internal variables
    images: {
        // Quay initial image, used in the Dockerfile FROM clause
        base: {
            repo: "quay.io/coreos/olm-ci",
            tag: "base",
            name: utils.containerName(self.repo, self.tag),
        },

        // release is a copy of the quayci image to the 'prod' repository
        release: {
            olm: {
                repo: "quay.io/coreos/olm",
                tag: "${CI_COMMIT_REF_SLUG}-${SHA8}",
                name: utils.containerName(self.repo, self.tag),
            },
        },

        tag: {
            olm: {
                repo: "quay.io/coreos/olm",
                tag: "${CI_COMMIT_TAG}",
                name: utils.containerName(self.repo, self.tag),
            },
        },


        ci: {
            olm: {
                repo: "quay.io/coreos/olm-ci",
                tag: "${CI_COMMIT_REF_SLUG}",
                name: utils.containerName(self.repo, self.tag),
            },
        },

        e2e: {
            repo: "quay.io/coreos/olm-e2e",
            tag: "${CI_COMMIT_REF_SLUG}-${SHA8}",
            name: utils.containerName(self.repo, self.tag),
        },

        e2elatest: {
            repo: "quay.io/coreos/olm-e2e",
            tag: "latest",
            name: utils.containerName(self.repo, self.tag),
        },

        prerelease: {
            olm: {
                repo: "quay.io/coreos/olm-ci",
                tag: "${CI_COMMIT_REF_SLUG}-pre",
                name: utils.containerName(self.repo, self.tag),
            },
        },
    },
}
