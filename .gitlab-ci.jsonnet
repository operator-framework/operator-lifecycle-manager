local utils = import '.gitlab-ci/utils.libsonnet';
local vars = import '.gitlab-ci/vars.libsonnet';
local baseJob = import '.gitlab-ci/base_jobs.libsonnet';
local mergeJob = utils.ci.mergeJob;
local images = vars.images;
local docker = utils.docker;
local stages_list = [
    // gitlab-ci stages
    'sanity',
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

    'sanity-checks': baseJob.sanityCheck {
        stage: 'sanity',
        script: [
            "make verify-catalog",
            "make verify-codegen",
        ],
    },

    'container-build': baseJob.dockerBuild {
        // Build and push the alm container.
        // Docker Tag is the branch/tag name
        stage: stages.docker_build,
        before_script+: ["mkdir -p $PWD/bin"],
        script:
            docker.build_and_push(images.ci.alm.name,
                                  cache=false,
                                  extra_opts=["-f alm-ci.Dockerfile"]) +
            docker.build_and_push(images.ci.catalog.name,
                                  cache=false,
                                  extra_opts=["-f catalog-ci.Dockerfile"]) +
            docker.cp(images.ci.alm.name, src="/bin/alm", dest="bin/alm") +
            docker.cp(images.ci.catalog.name, src="/bin/catalog", dest="bin/catalog") +
            docker.build_and_push(images.prerelease.alm.name,
                                  cache=false,
                                  extra_opts=["-f alm-pre.Dockerfile"]) +
            docker.build_and_push(images.prerelease.catalog.name,
                                  cache=false,
                                  extra_opts=["-f catalog-pre.Dockerfile"]) +
            docker.build_and_push(images.e2e.name,
                                  cache=false,
                                  extra_opts=["-f e2e-run.Dockerfile"]),
    },

    'container-release': baseJob.dockerBuild {
        // ! Only master/tags
        // push the container to the 'prod' repository
        stage: stages.docker_release,
        before_script+: ["mkdir -p $PWD/bin"],
        script:
            docker.rename(images.prerelease.alm.name, images.release.alm.name) +
            docker.rename(images.prerelease.catalog.name, images.release.catalog.name) +
            docker.rename(images.e2e.name, images.e2elatest.name),

    } + onlyMaster,

    // Unit-tests
    local unittest_stage = baseJob.AlmTest {
        stage: stages.tests,
    },

    'unit-tests': unittest_stage {
        coverage: @"/\d\d\.\d.$/",
        script: [
            "make vendor",
            "make verify-catalog",
            "make verify-codegen",
            "make coverage",
        ],
    },

    'e2e-setup': baseJob.Deploy {
        local _vars = self.localvars,
        localvars+:: {
            namespace: "e2e-%s" % "${CI_COMMIT_REF_SLUG}-${SHA8}",
            catalog_namespace: "e2e-%s" % "${CI_COMMIT_REF_SLUG}-${SHA8}",
        },
        stage: stages.test_setup,
    },

    'e2e-teardown': baseJob.DeployStop {
        local _vars = self.localvars,
        localvars+:: {
            namespace: "e2e-%s" % "${CI_COMMIT_REF_SLUG}-${SHA8}",
            catalog_namespace: "e2e-%s" % "${CI_COMMIT_REF_SLUG}-${SHA8}",
        },
        stage: stages.test_teardown,
    },

    // End2End tests
    local integration_test = baseJob.EndToEndTest {
        stage: stages.tests,
    },

    e2e_tests: integration_test {
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

    "deploy-teamui": baseJob.Deploy {
        local _vars = self.localvars,
        localvars+:: {
            image: images.release,
            domain: "teamui18.console.team.coreos.systems",
            namespace: "tectonic-system",
            channel: "staging",
            helm_opts: ["--force"],
            kubeconfig: "$TEAMUI_KUBECONFIG",
            params+:: {
                watchedNamespaces: "",
            },
        },
        stage: stages.deploy_staging,
        script+: [
            "curl -X POST --data-urlencode \"payload={\\\"text\\\": \\\"New ALM Operator quay.io/coreos/alm:${CI_COMMIT_REF_SLUG}-${SHA8} deployed to https://teamui18.console.team.coreos.systems/k8s/ns/tectonic-system/deployments/alm-operator\\\"}\" https://hooks.slack.com/services/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",
        ],
        environment+: {
            name: "teamui",
        },
        only: ['master'],
    },
};

{
    stages: stages_list,
    variables: vars.global,
} + jobs
