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
            DOCKER_HOST: "tcp://docker-host.gitlab.svc.cluster.local:2375",
        },
        before_script: [
            "docker login -u $DOCKER_USER -p $DOCKER_PASS quay.io",
        ],
    } + job_tags,

    Deploy: {
        local this = self,
        local _vars = self.localvars,
        localvars:: {
            appversion: "1.0.0-%s" % self.image.olm.tag,
            apprepo: "quay.io/coreos/olm-ci-app",
            appname: self.namespace,
            chart: "deploy/chart",
            app: "%s@%s" % [self.apprepo, self.appversion],
            domain: "olm-%s.k8s.devtable.com" % "${CI_COMMIT_REF_SLUG}",
            namespace: "ci-olm-%s" % "${CI_COMMIT_REF_SLUG}",
            image: vars.images.prerelease,
            channel: null,
            helm_opts: [],
            kubeconfig: "$CD_KUBECONFIG",
            params: {
                "olm.image.ref": _vars.image.olm.name,
                "catalog.image.ref": _vars.image.olm.name,
                "package.image.ref": _vars.image.olm.name,
                watchedNamespaces: _vars.namespace,
                catalog_namespace: _vars.namespace,
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
            'echo "version: 1.0.0-${CI_COMMIT_REF_SLUG}-pre" >> %s/Chart.yaml' % _vars.chart,
            'echo %s > params.json' % std.escapeStringJson(_vars.params),
            "cat params.json",
        ],

        script:
            k8s.setKubeConfig(_vars.kubeconfig) +
            helm.templateApply("olm", _vars.chart, _vars.namespace, _vars.params) +
            k8s.createPullSecret("coreos-pull-secret",
                                 _vars.namespace,
                                 "quay.io",
                                 "$DOCKER_USER",
                                 "$DOCKER_PASS") +
            k8s.waitForDeployment("olm-operator", _vars.namespace) +
            k8s.waitForDeployment("catalog-operator", _vars.namespace) +
            k8s.waitForDeployment("package-server", _vars.namespace),
    } + job_tags,

    DeployStop: self.Deploy {
        variables+: { GIT_STRATEGY: "none" },
        environment+: {
            action: "stop",
        },
        before_script: [],
        script:
            k8s.setKubeConfig(self.localvars.kubeconfig) + [
                "kubectl delete apiservice v1alpha1.packages.apps.redhat.com --ignore-not-found=true",
                "kubectl delete ns --ignore-not-found=true %s" % self.localvars.namespace,
                "kubectl get pods -o wide -n %s" % self.localvars.namespace,
            ],
    } + job_tags,

}
