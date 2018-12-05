local utils = import '.gitlab-ci/utils.libsonnet';
local vars = import '.gitlab-ci/vars.libsonnet';
local baseJob = import '.gitlab-ci/base_jobs.libsonnet';
local k8s = utils.k8s;
local mergeJob = utils.ci.mergeJob;
local images = vars.images;
local docker = utils.docker;
local stages_list = [
    // gitlab-ci stages
    'docker_base',
    'docker_build',
    'deploy_preview',
    'test_setup',
    'tests',
    'test_teardown',
    'integration',
    'docker_release',
    'deploy_staging',
    'teardown',
];

local stages = utils.set(stages_list);

// List CI jobs
local jobs = {
    // Helpers
    local onlyMaster = {
        only: ['master', 'tags'],
    },

    local onlyBranch = {
        only: ['branches'],
        except: ['master', 'tags'],
    },

    'container-base-build': baseJob.dockerBuild {
        stage: stages.docker_base,
        script: docker.build_and_push(images.base.name,
                                      cache=false,
                                      args={ sshkey: vars.deploy_keys.operator_client },
                                      extra_opts=["-f base.Dockerfile"]),
        only: ["schedules", "tags"],
    },

    'container-build': baseJob.dockerBuild {
        // Build and push the olm container.
        // Docker Tag is the branch/tag name
        stage: stages.docker_build,
        before_script+: [
        	"mkdir -p $PWD/bin",
        ],

        // builds a single multistage dockerfile and tags images based on labels
        // on the intermediate builds
        script: docker.multibuild_and_push("upstream.Dockerfile", labelImageMap={
            'builder': images.ci.olm.name,
            'olm': images.prerelease.olm.name,
            'e2e': images.e2e.name,
        })
    },

    'container-release': baseJob.dockerBuild {
        // ! Only master
        // push the container to the 'prod' repository
        stage: stages.docker_release,
        before_script+: ["mkdir -p $PWD/bin"],
        script:
            docker.rename(images.prerelease.olm.name, images.release.olm.name) +
            docker.rename(images.e2e.name, images.e2elatest.name),
        only: ['master'],
    },

    'tag-release': baseJob.dockerBuild {
        // ! Only tags
        // push the container to the 'prod' repository
        stage: stages.docker_release,
        before_script+: ["mkdir -p $PWD/bin"],
        script:
            docker.rename(images.prerelease.olm.name, images.tag.olm.name) +
            docker.rename(images.e2e.name, images.e2elatest.name),
        only: ['tags'],
    },

    "deploy-preview": baseJob.Deploy {
        local _vars = self.localvars,
        localvars+:: {
            helm_opts: ["--force"],
        },
        stage: stages.deploy_preview,
        when: "manual",
        environment+: {
            on_stop: "stop-preview",
        },
    } + onlyBranch,

    "stop-preview": baseJob.DeployStop {
        when: "manual",
        stage: stages.deploy_preview,
    } + onlyBranch,

    "deploy-staging": baseJob.Deploy {
        local _vars = self.localvars,
        localvars+:: {
            image: images.release,
            domain: "alm-staging.k8s.devtable.com",
            namespace: "ci-alm-staging",
            channel: "staging",
            helm_opts: ["--force"],
            kubeconfig: "$CD_KUBECONFIG",
        },
        stage: stages.deploy_staging,
        script+: [],
        environment+: {
            name: "staging",
        },
        only: ['master'],
    },

    # currently not needed, to re-enable remove the "." prefix
    ".deploy-teamui": baseJob.Deploy {
        local _vars = self.localvars,
        localvars+:: {
            image: images.release,
            domain: "teamui.console.team.coreos.systems",
            namespace: "operator-lifecycle-manager",
            catalog_namespace: "operator-lifecycle-manager",
            channel: "staging",
            helm_opts: ["--force"],
            kubeconfig: "$TEAMUI_KUBECONFIG",
            params+:: {
                watchedNamespaces: "",
            },
        },
        stage: stages.deploy_staging,
        script+: [
            "curl -X POST --data-urlencode \"payload={\\\"text\\\": \\\"New OLM Operator quay.io/coreos/olm:${CI_COMMIT_REF_SLUG}-${CI_COMMIT_SHA} deployed to ${TEAMUI_HOST}/k8s/ns/operator-lifecycle-manager/deployments/alm-operator\\\"}\" ${TEAMUI_SLACK_URL}",
        ],
        environment+: {
            name: "teamui",
        },
        only: [''],
    },

    "deploy-openshift": baseJob.Deploy {
        local _vars = self.localvars,
        localvars+:: {
            image: images.release,
            domain: "console.apps.ui-preserve.origin-gce.dev.openshift.com",
            namespace: "operator-lifecycle-manager",
            channel: "staging",
            helm_opts: ["--force"],
            kubeconfig: "$OPENSHIFT_KUBECONFIG",
            params+:: {
                watchedNamespaces: "",
            },
        },
        stage: stages.deploy_staging,
        script+: [
            "curl -X POST --data-urlencode \"payload={\\\"text\\\": \\\"New OLM Operator quay.io/coreos/olm:${CI_COMMIT_REF_SLUG}-${CI_COMMIT_SHA} deployed to ${OPENSHIFT_HOST}/k8s/ns/operator-lifecycle-manager/deployments/alm-operator\\\"}\" ${TEAMUI_SLACK_URL}",
        ],
        environment+: {
            name: "openshift",
        },
        only: ['master'],
    },
};

{
    stages: stages_list,
    variables: vars.global,
} + jobs
