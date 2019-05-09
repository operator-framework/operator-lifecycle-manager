# Debugging a ClusterServiceVersion

We have a ClusterServiceVersion that is failing to report as available.

```sh
$ kubectl -n ci-olm-pr-188-gc-csvs get clusterserviceversions etcdoperator.v0.8.1 -o yaml
...
  lastTransitionTime: 2018-01-22T15:48:13Z
  lastUpdateTime: 2018-01-22T15:51:09Z
  message: |
    installing: Waiting: waiting for deployment etcd-operator to become ready: Waiting for rollout to finish: 0 of 1 updated replicas are available...
  phase: Installing
  reason: InstallWaiting
...
```

The message tells us install can't complete because the etcd-operator deployment isn't available yet. Now we check on that deployment:

```sh
$ kubectl -n ci-olm-pr-188-gc-csvs get deployments etcd-operator -o yaml
...
spec:
  template:
    metadata:
      labels:
        name: etcd-operator-olm-owned
...
status:
  unavailableReplicas: 1
...
```

We see that 1 of the replicas is unavailable, and the spec tells us the label query to use to find the failing pods:

```sh
$ kubectl -n ci-olm-pr-188-gc-csvs get pods -l name=etcd-operator-olm-owned                                                                                         1 ↵
NAME                             READY     STATUS             RESTARTS   AGE
etcd-operator-6c7c8ccb56-9scrz   2/3       CrashLoopBackOff   820        2d

$ kubectl -n ci-olm-pr-188-gc-csvs get pods etcd-operator-6c7c8ccb56-9scrz -o yaml
...
 containerStatuses:
  - containerID: docker://aa7ee0902228247c32b9198be13fc826dfaf4901a70ee84f31582c284721a110
    image: quay.io/coreos/etcd-operator@sha256:b85754eaeed0a684642b0886034742234d288132dc6439b8132e9abd7a199de0
    imageID: docker-pullable://quay.io/coreos/etcd-operator@sha256:b85754eaeed0a684642b0886034742234d288132dc6439b8132e9abd7a199de0
    lastState:
      terminated:
        containerID: docker://aa7ee0902228247c32b9198be13fc826dfaf4901a70ee84f31582c284721a110
        exitCode: 1
        finishedAt: 2018-01-22T15:55:16Z
        reason: Error
        startedAt: 2018-01-22T15:55:16Z
    name: etcd-backup-operator
    ready: false
    restartCount: 820
    state:
      waiting:
        message: Back-off 5m0s restarting failed container=etcd-backup-operator pod=etcd-operator-6c7c8ccb56-9scrz_ci-olm-pr-188-gc-csvs(3084f195-fd38-11e7-b3ea-0aae23d78648)
        reason: CrashLoopBackOff
...
```

One of the pods in the deployment, `etcd-backup-operator` is crash looping for some reason. Now we check the logs of that container:

```sh
$ kubectl -n ci-olm-pr-188-gc-csvs logs etcd-operator-6c7c8ccb56-9scrz etcd-backup-operator                                                                         1 ↵
time="2018-01-22T15:55:16Z" level=info msg="Go Version: go1.9.2"
time="2018-01-22T15:55:16Z" level=info msg="Go OS/Arch: linux/amd64"
time="2018-01-22T15:55:16Z" level=info msg="etcd-backup-operator Version: 0.8.1"
time="2018-01-22T15:55:16Z" level=info msg="Git SHA: b97d9305"
time="2018-01-22T15:55:16Z" level=info msg="Event(v1.ObjectReference{Kind:"Endpoints", Namespace:"ci-olm-pr-188-gc-csvs", Name:"etcd-backup-operator", UID:"328b063e-fd38-11e7-b021-122952f9fac4", APIVersion:"v1", ResourceVersion:"11570590", FieldPath:""}): type: 'Normal' reason: 'LeaderElection' etcd-operator-6c7c8ccb56-9scrz became leader"
time="2018-01-22T15:55:16Z" level=info msg="starting backup controller" pkg=controller
time="2018-01-22T15:55:16Z" level=fatal msg="unknown StorageType: "
```

And we can see the reason for the error and take action to craft a new CSV that doesn't cause this error.

# Debugging an InstallPlan

The primary way an InstallPlan can fail is by not resolving the resources needed to install a CSV.

```yaml
apiVersion: app.coreos.com/v1alpha1
kind: InstallPlan
metadata:
  namespace: ci-olm-pr-188-gc-csvs
  name: olm-testing
spec:
  clusterServiceVersionNames:
  - etcdoperator123
  approval: Automatic
```

This installplan will fail because `etcdoperator123` is not in the catalog. We can see this in its status:

```sh
$ kubectl get -n ci-olm-pr-188-gc-csvs installplans olm-testing -o yaml
apiVersion: app.coreos.com/v1alpha1
kind: InstallPlan
metadata:
  ... 
spec:
  approval: Automatic
  clusterServiceVersionNames:
  - etcdoperator123
status:
  catalogSources:
  - rh-operators
  conditions:
  - lastTransitionTime: 2018-01-22T16:05:09Z
    lastUpdateTime: 2018-01-22T16:06:59Z
    message: 'not found: ClusterServiceVersion etcdoperator123'
    reason: DependenciesConflict
    status: "False"
    type: Resolved
  phase: Planning
```

Error messages like this will displayed for any other inconsistency in the catalog. They can be resolved by either updating the catalog or choosing clusterservices that resolve correctly.

# Debugging ALM operators

Both the ALM and Catalog operators have `-debug` flags available that display much more useful information when diagnosing a problem. If necessary, add this flag to their deployments and perform the action that is showing undersired behavior.
