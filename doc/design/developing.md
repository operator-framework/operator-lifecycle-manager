 # Setting up a namespaced OLM for development
 
 * Disable global olm/catalog if the cluster is only for testing
    * spin `tectonic-alm-operator` down to 0
    * delete `alm-operator` and `catalog-operator` deployments from `tectonic-system`
 * Make any config customizations by editing `example-values.yaml`
 * Deploy a namespaced copy of OLM
```sh
./scripts/package_release.sh 1.0.0-custom custom-olm ./doc/install/example-values.yaml
kubectl create ns olm-testing
kubectl get secrets -n tectonic-system -o yaml coreos-pull-secret | sed 's/tectonic-system/olm-testing/g' | kubectl create -f -
kubectl apply -f ./custom-olm
```

* ALM config
    * `namespace` - namespace to run in 
    * `watchedNamespaces` - namespaces to watch and operate on
    * `catalog_namespace` - namespace that catalog resources are created in
    * ALM annotates the namespaces it's configured to watch and ignores namespaces annotated with another ALM instance
        * taking control of an existing namespace (i.e. if you've left the global olm running) may require manually editing namespace annotations

* Catalog generation
    * Files in `deploy/chart/catalog_resources/<catalogname>` get collected into a configmap
    * on startup, catalog operator reads the configmap and writes out a CatalogSource pointing to it
        * hack because x-operator can't write out CatalogSource
    * short term: catalogsource -> configmap, no generation
    * medium term: stored in a seperate repo
    * longer term: something registry-like

# Updating a Service and testing updates

* Install the initial version 
    * Create an installplan with the initial version if it's already in the catalog
    * Create a CSV with the initial version if it's not in the catalog

* Create the new version 
    * Copy old CSV
    * Edit fields to update version
        * name references (i.e. etcdoperator.0.5.6)
        * `replaces` field pointing to previous version
        * edit deployments
            * same name - gets patched
            * different name - gets created/deleted
        * use sha256 references
        * update any descriptions
        * update any references to CRDs that are required
        * update any permissions needed
* Save new CSV and kubectl create it
* Watch olm operator logs and verify state you want has happened


# Updating a catalog entry

* Once the CSV is verified as correct and updates work properly, add it to `catalog_resources`
    * do not overwrite the old one
* Add any new CRDs to `deploy/chart/catalog_resources/<catalog>`
* run `make update-catalog` to regen the catalog configmap
* either apply the new configmap on its own and restart the catalog or, easier, just run:

```sh
./scripts/package_release.sh 1.0.0-custom custom-olm ./doc/install/example-values.yaml
kubectl apply -f ./custom-olm
```

* You can validate the update process by creating an `InstallPlan` with the previous version, letting it install, and then creating an `InstallPlan` with the updated version and verifying the update succeeds.


# Example InstallPlan

```yaml
apiVersion: app.coreos.com/v1alpha1
kind: InstallPlan
metadata:
  namespace: default
  name: olm-testing
spec:
  clusterServiceVersionNames:
  - etcdoperator.v0.7.2
  approval: Automatic
```
