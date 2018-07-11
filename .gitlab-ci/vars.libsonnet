local utils = import "utils.libsonnet";

{
    deploy_keys: { operator_client: "$OPERATORCLENT_RSA_B64" },
    alm_repo: "github.com/operator-framework/operator-lifecycle-manager",
    global: {
        // .gitlab-ci.yaml top `variables` key
        FAILFASTCI_NAMESPACE: "operator-framework",
        // increase attempts to handle occational auth failures against gitlab.com
        GET_SOURCES_ATTEMPTS: "10",
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
                repo: "quay.io/coreos/olm",
                tag: "${CI_COMMIT_REF_SLUG}-${SHA8}",
                name: utils.containerName(self.repo, self.tag),
            },
            catalog: {
                repo: "quay.io/coreos/catalog",
                tag: "${CI_COMMIT_REF_SLUG}-${SHA8}",
                name: utils.containerName(self.repo, self.tag),
            },
            servicebroker: {
                repo: "quay.io/coreos/alm-service-broker",
                tag: "${CI_COMMIT_REF_SLUG}-${SHA8}",
                name: utils.containerName(self.repo, self.tag),
            },
        },

        // tagRelease is a copy of the quayci image to the 'prod' repository for tags
        tagRelease: {
            alm: {
                repo: "quay.io/coreos/olm",
                tag: "${CI_COMMIT_TAG}",
                name: utils.containerName(self.repo, self.tag),
            },
            catalog: {
                repo: "quay.io/coreos/catalog",
                tag: "${CI_COMMIT_TAG}",
                name: utils.containerName(self.repo, self.tag),
            },
            servicebroker: {
                repo: "quay.io/coreos/alm-service-broker",
                tag: "${CI_COMMIT_TAG}",
                name: utils.containerName(self.repo, self.tag),
            },
            e2e: {
                repo: "quay.io/coreos/alm-e2e",
                tag: "${CI_COMMIT_TAG}",
                name: utils.containerName(self.repo, self.tag),
            },
        },

        ci: {
            alm: {
                repo: "quay.io/coreos/alm-ci",
                tag: "${CI_COMMIT_REF_SLUG}",
                name: utils.containerName(self.repo, self.tag),
            },
        },

        e2e: {
            repo: "quay.io/coreos/alm-e2e",
            tag: "${CI_COMMIT_REF_SLUG}-${SHA8}",
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
            servicebroker: {
                repo: "quay.io/coreos/alm-service-broker-ci",
                tag: "${CI_COMMIT_REF_SLUG}-pre",
                name: utils.containerName(self.repo, self.tag),
            },
        },
    },
}
