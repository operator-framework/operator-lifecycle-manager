local utils = import "utils.libsonnet";

{
    deploy_keys: { operator_client: "$OPERATORCLENT_RSA_B64" },
    alm_repo: "github.com/coreos-inc/alm",
    global: {
        // .gitlab-ci.yaml top `variables` key
        FAILFASTCI_NAMESPACE: "quay",
    },

    paths: {
        alm: {
            src: "$GOPATH/src/%s" % $.alm_repo,
        },
    },

    // internal variables
    images: {
        // Quay initial image, used in the Dockerfile FROM clause
        base: {
            repo: "quay.io/coreos/alm-ci",
            tag: "base",
            name: utils.containerName(self.repo, self.tag),
        },

        // release is a copy of the quayci image to the 'prod' repository
        release: {
            alm: {
                repo: "quay.io/coreos/alm",
                tag: "${CI_COMMIT_REF_SLUG}-${CI_COMMIT_SHA}",
                name: utils.containerName(self.repo, self.tag),
            },
            catalog: {
                repo: "quay.io/coreos/catalog",
                tag: "${CI_COMMIT_REF_SLUG}-${CI_COMMIT_SHA}",
                name: utils.containerName(self.repo, self.tag),
            },
        },

        ci: {
            alm: {
                repo: "quay.io/coreos/alm-ci",
                tag: "${CI_COMMIT_REF_SLUG}",
                name: utils.containerName(self.repo, self.tag),
            },
            catalog: {
                repo: "quay.io/coreos/catalog-ci",
                tag: "${CI_COMMIT_REF_SLUG}",
                name: utils.containerName(self.repo, self.tag),
            },
        },

        e2e: {
            repo: "quay.io/coreos/alm-e2e",
            tag: "${CI_COMMIT_REF_SLUG}-${CI_COMMIT_SHA}",
            name: utils.containerName(self.repo, self.tag),
        },

        e2elatest: {
            repo: "quay.io/coreos/alm-e2e",
            tag: "latest",
            name: utils.containerName(self.repo, self.tag),
        },

        prerelease: {
            alm: {
                repo: "quay.io/coreos/alm-ci",
                tag: "${CI_COMMIT_REF_SLUG}-pre",
                name: utils.containerName(self.repo, self.tag),
            },
            catalog: {
                repo: "quay.io/coreos/catalog-ci",
                tag: "${CI_COMMIT_REF_SLUG}-pre",
                name: utils.containerName(self.repo, self.tag),
            },
        },
    },
}
