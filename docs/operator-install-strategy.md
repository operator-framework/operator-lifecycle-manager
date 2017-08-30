# Operator Install Strategy

### [ALM-76](https://coreosdev.atlassian.net/browse/ALM-76)

> As a CoreOS service dev. who has written an operator,
> I would like to provide ALM with a strategy for installing my operator.


## Thoughts

1. Do operator definitions vary across clusters?
2. Do operator definitions depend on temporal or dynamic variables?
2. Does the operator-install strategy itself need to be dynamic in any way?
3. Or can the variations be handled via templating prior to registering AppType?


## Proposal

### 1. Static k8s manifests

ALM applies manifest supplied directly to cluster via kubernetes API without
modifications.

e.g.

```yaml
apiVersion: app.coreos.com/v1beta1
kind: App
metadata:
  name: vault
  type: com.tectonic.storage
spec:
  operators:
    cdr: vault
    installStrategy:
      kind: Deployment
      metadata:
        name: vault-operator
      spec:
        replicas: 1
        template:
          metadata:
            labels:
              name: vault-operator
          spec:
            containers:
            - name: vault-operator
              image: quay.io/coreos/vault-operator:0.0.2
              env:
              - name: MY_POD_NAMESPACE
                valueFrom:
                  fieldRef:
                    fieldPath: metadata.namespace
              - name: MY_POD_NAME
                valueFrom:
                  fieldRef:
                    fieldPath: metadata.name
            imagePullSecrets:
            - name: coreos-pull-secret
```

### 2. Templated k8s

Via Helm or other templating system.


### 3. Sourced templates

Pull templates/charts from a remote location.


apptype
installstrategy
resourcecdr
