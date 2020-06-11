<!-- START doctoc generated TOC please keep comment here to allow auto update -->
<!-- DON'T EDIT THIS SECTION, INSTEAD RE-RUN doctoc TO UPDATE -->
**Table of Contents**  *generated with [DocToc](https://github.com/thlorenz/doctoc)*

- [Building a Cluster Service Version (CSV) for the Operator Framework](#building-a-cluster-service-version-csv-for-the-operator-framework)
  - [What is a Cluster Service Version (CSV)?](#what-is-a-cluster-service-version-csv)
  - [CSV Metadata](#csv-metadata)
  - [Your Custom Resource Definitions](#your-custom-resource-definitions)
    - [Owned CRDs](#owned-crds)
    - [Required CRDs](#required-crds)
  - [CRD Templates](#crd-templates)
  - [Your API Services](#your-api-services)
    - [Owned APIServices](#owned-apiservices)
    - [APIService Resource Creation](#apiservice-resource-creation)
    - [APIService Serving Certs](#apiservice-serving-certs)
    - [Required APIServices](#required-apiservices)
  - [Operator Metadata](#operator-metadata)
  - [Operator Install](#operator-install)
  - [Full Examples](#full-examples)

<!-- END doctoc generated TOC please keep comment here to allow auto update -->

# Building a Cluster Service Version (CSV) for the Operator Framework

This guide is intended to guide an Operator author to package a version of their Operator to run with the [Operator Lifecycle Manager](https://github.com/operator-framework/operator-lifecycle-manager). This will be a manual method that will walk through each section of the file, what it’s used for and how to populate it.

## What is a Cluster Service Version (CSV)?

A CSV is the metadata that accompanies your Operator container image. It can be used to populate user interfaces with info like your logo/description/version and it is also a source of technical information needed to run the Operator, like the RBAC rules it requires and which Custom Resources it manages or depends on.

The Lifecycle Manager will parse this and do all of the hard work to wire up the correct Roles and Role Bindings, ensure that the Operator is started (or updated) within the desired namespace and check for various other requirements, all without the end users having to do anything.

You can read about the [full architecture in more detail](architecture.md#what-is-a-clusterserviceversion).

## CSV Metadata

The object has the normal Kubernetes metadata. Since the CSV pertains to the specific version, the naming scheme is the name of the Operator + the semantic version number, eg `mongodboperator.v0.3`.

The namespace is used when a CSV will remain private to a specific namespace. Only users of that namespace will be able to view or instantiate the Operator. If you plan on distributing your Operator to many namespaces or clusters, you may want to explore bundling it into a [Catalog](architecture.md#catalog-registry-design).

The namespace listed in the CSV within a catalog is actually a placeholder, so it is common to simply list `placeholder`. Otherwise, loading a CSV directly into a namespace requires that namespace, of course.

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: ClusterServiceVersion
metadata:
  name: mongodboperator.v0.3
  namespace: placeholder
```

## Your Custom Resource Definitions
There are two types of CRDs that your Operator may use, ones that are “owned” by it and ones that it depends on, which are “required”.
### Owned CRDs

The CRDs owned by your Operator are the most important part of your CSV. This establishes the link between your Operator and the required RBAC rules, dependency management and other under-the-hood Kubernetes concepts.

It’s common for your Operator to use multiple CRDs to link together concepts, such as top-level database configuration in one object and a representation of replica sets in another. List out each one in the CSV file.

**DisplayName**: A human readable version of your CRD name, eg. “MongoDB Standalone”

**Description**: A short description of how this CRD is used by the Operator or a description of the functionality provided by the CRD.

**Group**: The API group that this CRD belongs to, eg. database.example.com

**Kind**: The machine readable name of your CRD

**Name**: The full name of your CRD

The next two sections require more explanation.

**Resources**:
Your CRDs will own one or more types of Kubernetes objects. These are listed in the resources section to inform your end-users of the objects they might need to troubleshoot or how to connect to the application, such as the Service or Ingress rule that exposes a database.

It’s recommended to only list out the objects that are important to a human, not an exhaustive list of everything you orchestrate. For example, ConfigMaps that store internal state that shouldn’t be modified by a user shouldn’t appear here.

**SpecDescriptors, StatusDescriptors, and ActionDescriptors**:
These are a way to hint UIs with certain inputs or outputs of your Operator that are most important to an end user. If your CRD contains the name of a Secret or ConfigMap that the user must provide, you can specify that here. These items will be linked and highlighted in compatible UIs.

There are three types of descriptors:

***SpecDescriptors***: A reference to fields in the `spec` block of an object.

***StatusDescriptors***: A reference to fields in the `status` block of an object.

***ActionDescriptors***: A reference to actions that can be performed on an object.

All Descriptors accept the following fields:

**DisplayName**: A human readable name for the Spec, Status, or Action.

**Description**: A short description of the Spec, Status, or Action and how it is used by the Operator.

**Path**: A dot-delimited path of the field on the object that this descriptor describes.

**X-Descriptors**: Used to determine which "capabilities" this descriptor has and which UI component to use. A canonical list of React UI X-Descriptors for OpenShift can be found [here](https://github.com/openshift/console/blob/master/frontend/packages/operator-lifecycle-manager/src/components/descriptors/types.ts).

More information on Descriptors can be found [here](https://github.com/openshift/console/tree/master/frontend/packages/operator-lifecycle-manager/src/components/descriptors).

Below is an example of a MongoDB “standalone” CRD that requires some user input in the form of a Secret and ConfigMap, and orchestrates Services, StatefulSets, Pods and ConfigMaps.

```yaml
      - displayName: MongoDB Standalone
        group: mongodb.com
        kind: MongoDbStandalone
        name: mongodbstandalones.mongodb.com
        resources:
          - kind: Service
            name: ''
            version: v1
          - kind: StatefulSet
            name: ''
            version: v1beta2
          - kind: Pod
            name: ''
            version: v1
          - kind: ConfigMap
            name: ''
            version: v1
        specDescriptors:
          - description: Credentials for Ops Manager or Cloud Manager.
            displayName: Credentials
            path: credentials
            x-descriptors:
              - 'urn:alm:descriptor:com.tectonic.ui:selector:core:v1:Secret'
          - description: Project this deployment belongs to.
            displayName: Project
            path: project
            x-descriptors:
              - 'urn:alm:descriptor:com.tectonic.ui:selector:core:v1:ConfigMap'
          - description: MongoDB version to be installed.
            displayName: Version
            path: version
            x-descriptors:
              - 'urn:alm:descriptor:com.tectonic.ui:label'
        statusDescriptors:
          - description: The status of each of the Pods for the MongoDB cluster.
            displayName: Pod Status
            path: pods
            x-descriptors:
              - 'urn:alm:descriptor:com.tectonic.ui:podStatuses'
        version: v1
        description: >-
          MongoDB Deployment consisting of only one host. No replication of
          data.
```

### Required CRDs

Relying on other “required” CRDs is completely optional and only exists to reduce the scope of individual Operators and provide a way to compose multiple Operators together to solve an end-to-end use case. An example of this is an Operator that might set up an application and install an etcd cluster (from an etcd Operator) to use for distributed locking and a Postgres database (from a Postgres Operator) for data storage.

The Lifecycle Manager will check against the available CRDs and Operators in the cluster to fulfill these requirements. If suitable versions are found, the Operators will be started within the desired namespace and a Service Account created for each Operator to create/watch/modify the Kubernetes resources required.

**Name**: The full name of the CRD you require

**Version**: The version of that object API

**Kind**: The Kubernetes object kind

**DisplayName**: A human readable version of the CRD

**Description**: A summary of how the component fits in your larger architecture

```yaml
    required:
    - name: etcdclusters.etcd.database.coreos.com
      version: v1beta2
      kind: EtcdCluster
      displayName: etcd Cluster
      description: Represents a cluster of etcd nodes.
```
## CRD Templates
Users of your Operator will need to be aware of which options are required vs optional. You can provide templates for each of your CRDs with a minimum set of configuration as an annotation named `alm-examples`. Metadata for each template, for exmaple an expanded description, can be included in an annotation named `alm-examples-metadata`, which should be a hash indexed with the `metadata.name` of the example in the `alm-examples` list. Compatible UIs will pre-enter the `alm-examples` template for users to further customize, and use the `alm-examples-metadata` to help users decide which template to select.

The annotation consists of a list of the `kind`, eg. the CRD name, and the corresponding `metadata` and `spec` of the Kubernetes object. Here’s a full example that provides templates for `EtcdCluster`, `EtcdBackup` and `EtcdRestore`:

```yaml
metadata:
  annotations:
    alm-examples-metadata: >-
      {"example-etcd-cluster":{"description":"Example EtcdCluster CR"},"example-etcd-restore":{"description":"Example EtcdRestore CR that restores data from S3"},"example-etcd-backup":{"description":"Example EtcdBackup CR that stores backups on S3"}}
    alm-examples: >-
      [{"apiVersion":"etcd.database.coreos.com/v1beta2","kind":"EtcdCluster","metadata":{"name":"example-etcd-cluster","namespace":"default"},"spec":{"size":3,"version":"3.2.13"}},{"apiVersion":"etcd.database.coreos.com/v1beta2","kind":"EtcdRestore","metadata":{"name":"example-etcd-restore"},"spec":{"etcdCluster":{"name":"example-etcd-cluster"},"backupStorageType":"S3","s3":{"path":"<full-s3-path>","awsSecret":"<aws-secret>"}}},{"apiVersion":"etcd.database.coreos.com/v1beta2","kind":"EtcdBackup","metadata":{"name":"example-etcd-backup"},"spec":{"etcdEndpoints":["<etcd-cluster-endpoints>"],"storageType":"S3","s3":{"path":"<full-s3-path>","awsSecret":"<aws-secret>"}}}]
```

## Your API Services
As with CRDs, there are two types of APIServices that your Operator may use, “owned” and "required".

### Owned APIServices

When a CSV owns an APIService, it is responsible for describing the deployment of the extension api-server that backs it and the group-version-kinds it provides.

An APIService is uniquely identified by the group-version it provides and can be listed multiple times to denote the different kinds it is expected to provide.

**DisplayName**: A human readable version of your APIService name, eg. “MongoDB Standalone”

**Description**: A short description of how this APIService is used by the Operator or a description of the functionality provided by the APIService.

**Group**: Group that the APIService provides, eg. database.example.com.

**Version**: Version of the APIService, eg v1alpha1

**Kind**: A kind that the APIService is expected to provide.

**DeploymentName**:
Name of the deployment defined by your CSV that corresponds to your APIService (required for owned APIServices). During the CSV pending phase, the OLM Operator will search your CSV's InstallStrategy for a deployment spec with a matching name, and if not found, will not transition the CSV to the install ready phase.

**Resources**:
Your APIServices will own one or more types of Kubernetes objects. These are listed in the resources section to inform your end-users of the objects they might need to troubleshoot or how to connect to the application, such as the Service or Ingress rule that exposes a database.

It’s recommended to only list out the objects that are important to a human, not an exhaustive list of everything you orchestrate. For example, ConfigMaps that store internal state that shouldn’t be modified by a user shouldn’t appear here.

**SpecDescriptors, StatusDescriptors, and ActionDescriptors**:
Essentially the same as for owned CRDs.

### APIService Resource Creation
The Lifecycle Manager is responsible for creating or replacing the Service and APIService resources for each unique owned APIService.
* Service pod selectors are copied from the CSV deployment matching the APIServiceDescription's DeploymentName.
* A new CA key/cert pair is generated for for each installation and the base64 encoded CA bundle is embedded in the respective APIService resource.

### APIService Serving Certs
The Lifecycle Manager handles generating a serving key/cert pair whenever an owned APIService is being installed. The serving cert has a CN containing the host name of the generated Service resource and is signed by the private key of the CA bundle embedded in the corresponding APIService resource. The cert is stored as a type `kubernetes.io/tls` Secret in the deployment namespace and a Volume named "apiservice-cert" is automatically appended to the Volumes section of the deployment in the CSV matching the APIServiceDescription's `DeploymentName` field. If one does not already exist, a VolumeMount with a matching name is also appended to all containers of that deployment. This allows users to define a VolumeMount with the expected name to accommodate any custom path requirements. The generated VolumeMount's path defaults to `/apiserver.local.config/certificates` and any existing VolumeMounts with the same path are replaced.

### Required APIServices

The Lifecycle Manager will ensure all required CSVs have an APIService that is available and all expected group-version-kinds are discoverable before attempting installation. This allows a CSV to rely on specific kinds provided by APIServices it does not own.

**DisplayName**: A human readable version of your APIService name, eg. “MongoDB Standalone”

**Description**: A short description of how this APIService is used by the Operator or a description of the functionality provided by the APIService.

**Group**: Group that the APIService provides, eg. database.example.com.

**Version**: Version of the APIService, eg v1alpha1

**Kind**: A kind that the APIService is expected to provide.

## Operator Metadata
The metadata section contains general metadata around the name, version and other info that aids users in discovery of your Operator.

**DisplayName**: Human readable name that describes your Operator and the CRDs that it implements

**Keywords**: A list of categories that your Operator falls into. Used for filtering within compatible UIs.

**Provider**: The name of the publishing entity behind the Operator

**Maturity**: Level of maturity the Operator has achieved at this version, eg. planning, pre-alpha, alpha, beta, stable, mature, inactive, or deprecated.

**Version**: The semanic version of the Operator. This value should be incremented each time a new Operator image is published.

**Icon**: a base64 encoded image of the Operator logo or the logo of the publisher. The `base64data` parameter contains the data and the `mediatype` specifies the type of image, eg. `image/png` or `image/svg`.

**Links**: A list of relevant links for the Operator. Common links include documentation, how-to guides, blog posts, and the company homepage.

**Maintainers**: A list of names and email addresses of the maintainers of the Operator code. This can be a list of individuals or a shared email alias, eg. support@example.com.

**Description**: A markdown blob that describes the Operator. Important information to include: features, limitations and common use-cases for the Operator. If your Operator manages different types of installs, eg. standalone vs clustered, it is useful to give an overview of how each differs from each other, or which ones are supported for production use.

**MinKubeVersion**: A minimum version of Kubernetes that server is supposed to have so operator(s) can be deployed. The Kubernetes version must be in "Major.Minor.Patch" format (e.g: 1.11.0).

**Labels** (optional): Any key/value pairs used to organize and categorize this CSV object.

**Selectors** (optional): A label selector to identify related resources. Set this to select on current labels applied to this CSV object (if applicable).

**InstallModes**: A set of `InstallMode`s that tell OLM which `OperatorGroup`s an Operator can belong to. Belonging to an `OperatorGroup` means that OLM provides the set of targeted namespaces as an annotation on the Operator's CSV and any deployments defined therein. These deployments can then utilize [the Downward API](https://kubernetes.io/docs/tasks/inject-data-application/downward-api-volume-expose-pod-information/#the-downward-api) to inject the list of namespaces into their container(s). An `InstallMode` consists of an `InstallModeType` field and a boolean `Supported` field. There are four `InstallModeTypes`:
* `OwnNamespace`: If supported, the operator can be a member of an `OperatorGroup` that selects its own namespace
* `SingleNamespace`: If supported, the operator can be a member of an `OperatorGroup` that selects one namespace
* `MultiNamespace`: If supported, the operator can be a member of an `OperatorGroup` that selects more than one namespace
* `AllNamespaces`: If supported, the operator can be a member of an `OperatorGroup` that selects all namespaces (target namespace set is the empty string "")

Here's an example:

```yaml
   keywords: ['etcd', 'key value', 'database', 'coreos', 'open source']
   version: 0.9.2
   maturity: alpha
   replaces: etcdoperator.v0.9.0
   maintainers:
   - name: CoreOS, Inc
     email: support@coreos.com
   provider:
     name: CoreOS, Inc
   labels:
     alm-owner-etcd: etcdoperator
     operated-by: etcdoperator
   selector:
     matchLabels:
       alm-owner-etcd: etcdoperator
       operated-by: etcdoperator
   links:
   - name: Blog
     url: https://coreos.com/etcd
   - name: Documentation
     url: https://coreos.com/operators/etcd/docs/latest/
   - name: etcd Operator Source Code
     url: https://github.com/coreos/etcd-operator
   icon:
   - base64data: <base64-encoded-data>
     mediatype: image/png
   installModes:
   - type: OwnNamespace
     supported: true
   - type: SingleNamespace
     supported: true
   - type: MultiNamespace
     supported: false
   - type: AllNamespaces
     supported: true
```

## Operator Install
The install block is how the Lifecycle Manager will instantiate the Operator on the cluster. There are two subsections within install: one to describe the `deployment` that will be started within the desired namespace and one that describes the Role `permissions` required to successfully run the Operator.

Ensure that the `serviceAccountName` used in the `deployment` spec matches one of the Roles described under `permissions`.

Multiple Roles should be described to reduce the scope of any actions needed containers that the Operator may run on the cluster. For example, if you have a component that generates a TLS Secret upon start up, a Role that allows `create` but not `list` on Secrets is more secure than using a single all-powerful Service Account.

Here’s a full example:

```yaml
  install:
    spec:
      deployments:
        - name: example-operator
          spec:
            replicas: 1
            selector:
              matchLabels:
                k8s-app: example-operator
            template:
              metadata:
                labels:
                  k8s-app: example-operator
              spec:
                containers:
                    image: 'quay.io/example/example-operator:v0.0.1'
                    imagePullPolicy: Always
                    name: example-operator
                    resources:
                      limits:
                        cpu: 200m
                        memory: 100Mi
                      requests:
                        cpu: 100m
                        memory: 50Mi
                imagePullSecrets:
                  - name: ''
                nodeSelector:
                  kubernetes.io/os: linux
                serviceAccountName: example-operator
      permissions:
        - serviceAccountName: example-operator
          rules:
            - apiGroups:
                - ''
              resources:
                - configmaps
                - secrets
                - services
              verbs:
                - get
                - list
                - create
                - update
                - delete
            - apiGroups:
                - apps
              resources:
                - statefulsets
              verbs:
                - '*'
            - apiGroups:
                - apiextensions.k8s.io
              resources:
                - customresourcedefinitions
              verbs:
                - get
                - list
                - watch
                - create
                - delete
            - apiGroups:
                - mongodb.com
              resources:
                - '*'
              verbs:
                - '*'
        - serviceAccountName: example-operator-list
          rules:
            - apiGroups:
                - ''
              resources:
                - services
              verbs:
                - get
                - list
    strategy: deployment
```

## Full Examples

Several [complete examples of CSV files](https://github.com/operator-framework/community-operators) are stored in Github.
