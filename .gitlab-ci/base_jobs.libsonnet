local utils = import 'utils.libsonnet';
local vars = import 'vars.libsonnet';
local k8s = utils.k8s;
local helm = utils.helm;
local docker = utils.docker;
local appr = utils.appr;

{
    local job_tags = { tags: ["kubernetes"] },

    dockerBuild: {
        // base job to manage containers (build / push)
        image: "docker:git",
        variables: {
            DOCKER_DRIVER: "overlay2",
            DOCKER_HOST: "tcp://docker-host.gitlab-runner.svc.cluster.local:2375",
        },
        before_script: [
            "docker login -u $DOCKER_USER -p $DOCKER_PASS quay.io",
        ],
    } + job_tags,

    AlmTest: {
        before_script: [
            "mkdir -p $GOPATH/src/%s" % vars.alm_repo,
            "cp -a $CI_PROJECT_DIR/* $GOPATH/src/%s" % vars.alm_repo,
            "cd $GOPATH/src/%s" % vars.alm_repo,
        ],
        // base job to test the container
        image: vars.images.ci.name,
    } + job_tags,


    EndToEndTest: {
        image: "python:2.7",
        services: [
            { name: vars.images.prerelease.name, alias: 'alm' },
        ],
        before_script: ["cd /"],
        script: ['sleep 10'],
        variables: {
            GIT_STRATEGY: "none",
            ALM_HOST: "localhost:8080",
        },
    } + job_tags,

    Deploy: {
        local this = self,
        local _vars = self.localvars,
        localvars:: {
            appversion: "1.0.0-%s" % self.image.tag,
            apprepo: "quay.io/coreos/alm-ci-app",
            appname: self.namespace,
            app: "%s@%s" % [self.apprepo, self.appversion],
            domain: "alm-%s.k8s.devtable.com" % "${CI_COMMIT_REF_SLUG}",
            namespace: "ci-alm-%s" % "${CI_COMMIT_REF_SLUG}",
            image: vars.images.prerelease,
            channel: null,
            helm_opts: [],
            params: {
                "image.repository": _vars.image.repo,
                "image.tag": _vars.image.tag,
                namespace: _vars.namespace,
            },
        },

        variables: {
            K8S_NAMESPACE: _vars.namespace,
            ALM_DOMAIN: _vars.domain,
        },

        image: "quay.io/coreos/alm-ci-build:latest",
        environment: {
            name: "review/%s" % _vars.appname,
            url: "https://%s" % _vars.domain,
        },

        before_script: [
            "appr login -u $DOCKER_USER -p $DOCKER_PASS quay.io",
            "cd deploy/alm-app",
            'echo "version: %s" >> Chart.yaml' % _vars.appversion,
            'echo %s > params.json' % std.escapeStringJson(_vars.params),
            "cat params.json",
        ],

        script:
            appr.push(_vars.apprepo, channel=_vars.channel, force=true) +
            k8s.createNamespace(_vars.namespace) +
            k8s.createPullSecret("coreos-pull-secret",
                                 _vars.namespace,
                                 "quay.io",
                                 "$DOCKER_USER",
                                 "$DOCKER_PASS") +
            k8s.apply("../../Documentation/design/resources/apptype.crd.yaml") +
            k8s.apply("../../Documentation/design/resources/operatorversion.crd.yaml") +
            helm.upgrade(_vars.app,
                         _vars.appname,
                         _vars.namespace,
                         _vars.params,
                         _vars.helm_opts) +
            ["kubectl get ingresses -n %s -o wide" % _vars.namespace],
    } + job_tags,

    DeployStop: self.Deploy {
        variables+: { GIT_STRATEGY: "none" },
        environment+: {
            action: "stop",
        },
        before_script: [],
        script: [
            "helm del --purge %s" % self.localvars.appname,
            "kubectl delete ns %s" % self.localvars.namespace,
            "kubectl get pods -o wide -n %s" % self.localvars.namespace,
        ],
    } + job_tags,

}
