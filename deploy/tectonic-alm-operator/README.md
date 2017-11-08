# tectonic-alm-operator [![Docker Repository on Quay](https://quay.io/repository/coreos/tectonic-alm-operator/status?token=24eb04b8-f7fb-4ac7-a131-73acaf66e496 "Docker Repository on Quay")](https://quay.io/repository/coreos/tectonic-alm-operator)

`tectonic-alm-operator` updates the [ALM](https://github.com/coreos-inc/alm) `alm` and `catalog` operators, in response to an AppVersion TPR. It is built directly on [tectonic-x-operator](https://github.com/coreos-inc/tectonic-x-operator) using the pre-built container strategy.

## Usage

Deploy a CoreOS Tectonic cluster with the [Tectonic Installer](https://github.com/coreos/tectonic-installer). Create the `tectonic-alm-operator` Deployment.

```sh
kubectl apply -f examples/appversion.yaml
kubectl apply -f examples/rbac.yaml
kubectl apply -f examples/operator.yaml
```

Watch the operator's logs.

```sh
kubectl get pods -n tectonic-system
kubectl logs tectonic-alm-operator-xyz -n tectonic-system -f
```

### Updating

Create the `tectonic-alm-operator` AppVersion or edit an existing AppVersion TPR. Set the `desiredVersion`.

```sh
kubectl apply -f examples/appversion.yaml
kubectl get appversion -n tectonic-system
kubectl edit appversion tectonic-alm-operator -n tectonic-system
```

### Verify

Verify the `update-operator` and `update-agent` updated.

```sh
kubectl describe deployment alm-operator -n tectonic-system | grep Image -A 8
```

## Development

Build the container image.

```sh
make docker-image
```

Set KUBECONFIG to the path to the Tectonic cluster's kubeconfig. Then run the docker image locally.

```sh
make dev
```

*Note: Local development is currently not working*

Follow the usage instructions above.