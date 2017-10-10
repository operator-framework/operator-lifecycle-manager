local utils = import '.gitlab-ci/utils.libsonnet';
local vars = import '.gitlab-ci/vars.libsonnet';
local baseJob = import '.gitlab-ci/base_jobs.libsonnet';
local mergeJob = utils.ci.mergeJob;
local images = vars.images;
local docker = utils.docker;
local stages_list = [
    // gitlab-ci stages
    'docker_base',
    'docker_build',
    'deploy_preview',
    'tests',
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
        only: ["schedules", "tag"],
    },

    'container-build': baseJob.dockerBuild {
        // Build and push the alm container.
        // Docker Tag is the branch/tag name
        stage: stages.docker_build,
        before_script+: ["mkdir -p $PWD/bin"],
        script: docker.build_and_push(images.ci.name,
                                      cache=false,
                                      extra_opts=[
                                          "-f alm-ci.Dockerfile",
                                          "--build-arg BASE_TAG=base-{$SHA8}",
                                      ]) +
                docker.cp(images.ci.name, src="/bin/alm", dest="bin/alm") +
                docker.build_and_push(images.prerelease.name,
                                      cache=false),
    },

    'container-release': baseJob.dockerBuild {
        // ! Only master/tags
        // push the container to the 'prod' repository
        stage: stages.docker_release,
        before_script+: ["mkdir -p $PWD/bin"],
        script:
            docker.rename(images.prerelease.name, images.release.name),

    } + onlyMaster,

    // Unit-tests
    local unittest_stage = baseJob.AlmTest {
        stage: stages.tests,
    },

    'unit-tests': unittest_stage {
        coverage: @"/^TOTAL.*\s+(\d+\%)\s*$/",
        script: ["make test"],
    },

    // End2End tests
    local integration_test = baseJob.EndToEndTest {
        stage: stages.tests,
    },

    e2e_example: integration_test {
        image: { name: "python:2.7" },
        script+: [
            "curl localhost:8080",
        ],
        allow_failure: true,
    },

    "deploy-preview": baseJob.Deploy {
        local _vars = self.localvars,
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
        },
        stage: stages.deploy_staging,
        script+: [],
        environment+: {
            name: "staging",
        },
        only: ['master'],
    },

};

{
    stages: stages_list,
    variables: vars.global,
} + jobs
