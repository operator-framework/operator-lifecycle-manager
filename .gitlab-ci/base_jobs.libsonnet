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

    WaitInQueue: {
        image: vars.images.e2e.name,
        script:
            [
                ". ${CI_PROJECT_DIR}/scripts/wait_in_queue.sh $CI_PROJECT_URL $CI_PIPELINE_ID",
            ],
    } + job_tags,


    EndToEndTest: {
        local _vars = self.localvars,
        localvars:: {
            appname: self.namespace,
            namespace: "e2e-%s" % "${CI_COMMIT_REF_SLUG}-${SHA8}",
            jobname: "e2e-%s" % "${CI_COMMIT_REF_SLUG}-${SHA8}",
            chart: "test/e2e/chart",
            appversion: "1.0.0-e2e-%s" % self.image.olm.tag,
            helm_opts: [],
            params: {
                namespace: _vars.namespace,
                "e2e.image.ref": vars.images.e2e.name,
                job_name: _vars.jobname,
            },
            patch: "{\"imagePullSecrets\": [{\"name\": \"coreos-pull-secret\"}]}",
        },
        image: "quay.io/coreos/alm-ci-build:latest",
        script:
            k8s.setKubeConfig("$CD_KUBECONFIG") +
            k8s.createPullSecret("coreos-pull-secret",
                                 _vars.namespace,
                                 "quay.io",
                                 "$DOCKER_USER",
                                 "$DOCKER_PASS") +
            [
                'kubectl -n %s patch serviceaccount default -p %s || true' % [_vars.namespace, std.escapeStringBash(_vars.patch)],
            ] +
            [
                'kubectl -n %s create rolebinding e2e-admin-rb --clusterrole=cluster-admin --serviceaccount=%s:default --namespace=%s || true' % [_vars.namespace, _vars.namespace, _vars.namespace],
            ] +
            helm.templateApply("olm-e2e", _vars.chart, _vars.namespace, _vars.params) +
            [
                "until kubectl -n %s logs job/%s | grep -v 'ContainerCreating'; do echo 'waiting for job to run' && sleep 1; done" % [_vars.namespace, _vars.jobname],
                "kubectl -n %s logs job/%s -f" % [_vars.namespace, _vars.jobname],
                "kubectl -n %s logs job/%s > e2e.log" % [_vars.namespace, _vars.jobname],
                "if cat e2e.log | grep -q '^not'; then echo 'err' && exit 1; else echo 'no err' && exit 0; fi",
            ],

        variables: {
            NAMESPACE: _vars.namespace,
            K8S_NAMESPACE: _vars.namespace,
        },
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
