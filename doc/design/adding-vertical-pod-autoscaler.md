# Adding Vertical Pod Autoscaler 

## Description

OLM supports users including `VerticalPodAutoscaler` (VPA) objects in their bundle alongside their operator manifests. `VerticalPodAutoscalers`
objects are used to configure the VerticalPodAutoscaler controller to dynamically allocate resources to pods based on their usage of CPU, memory, 
and other custom metrics. VPAs allow for more efficient use of cluster resources as pod resource needs are continually evaluated and adjusted by the VPA controller.
For more info, see the docs at https://github.com/kubernetes/autoscaler/tree/master/vertical-pod-autoscaler. 

## Caveats

Adding a VPA object in your bundle can lead to more efficient use of resource in your cluster. Best practices include limiting
the VPA to only the objects associated with your bundle. Consider your existing autoscaling setup in the cluster before adding
VPA objects to a bundle and installing the bundle on the cluster. 

`VerticalPodAutoscaler` objects watch a controller reference, such as deployment, to find a collection of pods to resize. Be sure to pass
the appropriate reference to your operator or operands depending on which you would like the VPA to watch. 

The VerticalPodAutoscaler controller must be enabled and active in the cluster for the VPA objects included in the bundle to have an effect. 
Alternatively, the installing operator could also add the VPA as a required API to ensure the VPA operator is present in the cluster.

The VPA will continually terminate pods and adjust the resource limits as needed - be sure your application is tolerant of restarts
before including a VPA alongside it. 

Note: at this time it is not recommended for the VPA to run alongside the HorizontalPodAutoscaler (HPA) on the same set of pods. 
VPA can however be used with an HPA that is configured to use either external or custom metrics. 

## Technical Details

VPA yaml manifests can be placed in the bundle alongside existing manifests in the `/manifests` directory. The VPA manifest will be present
in the bundle image. 

VPA objects are clusterwide in scope, and will be applied by OLM directly to the cluster. The VPA object will have
a label referencing the operator that it is associated with. 

OLM installs additional objects in the bundle after installing the CRDs and the CSV, to ensure proper owner references between the objects
and the CSV. Therefore, there may be an initial period where additional objects are not available to the operator. 

Prior versions of OLM (pre-0.16.0) do not support VPA objects. If a VPA is present in a bundle attempting to be installed on-cluster, OLM will throw an invalid installplan error
specifying that the resource is unsupported. 

## Limitations on Vertical Pod Autoscalers 

No limitations are placed on the contents of a VPA manifest at this time when installing on-cluster, but that may change as OLM develops
an advanced strategy to ensure installed objects do not compromise the cluster. 
