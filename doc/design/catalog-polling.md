# Catalog Polling

## Description
It is possible to configure the catalog source to poll a source, such as an image registry, to check whether the
catalog source pod should be updated. A common use case would be pushing new bundles to the same catalog source tag, and seeing
updated operators from those bundles being installed in the cluster. Currently polling is only implemented for image-based catalogs
that serve bundles over gRPC. 

For example, say currently you have Operator X v1.0 installed in the cluster. It came from a catalog source `quay.io/my-catalogs/my-catalog:master`. 
This is the latest version of the X operator in the catalog source. Lets say Operator X is upgraded to v2.0. The catalog source image can be rebuilt
to include the v2.0 version of the X operator and pushed to the same `master` tag. With catalog polling enabled, OLM will pull down the newer version 
of the catalog source image and route service traffic to the newer pod. The existing subscription will seamlessly create the v2.0 operator and remove the old v1.0 one. 

Each type of check for an updated catalog source is called an `updateStrategy`. Only one `updateStrategy` is supported at a time.
`registryPoll` is a type of `updateStrategy` that checks an image registry for an updated version of the same tag. The `interval` defines
the amount of time between each successive poll.  

## Example Spec
Here is an example catalog source that polls `quay.io/my-catalogs/my-catalog:master` every 45 minutes to see if the image has been updated. 

```yaml
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: catsrc-test
spec:
  displayName: CatalogSource Test
  sourceType: grpc
  image: quay.io/my-catalogs/my-catalog:master
  updateStrategy:
    registryPoll:
      interval: 45m
```

It is required for the catalog source to be sourceType grpc and be backed by an image for polling to work.  

## Caveats
* The polling sequence is not instantaneous - it can take up to 15 minutes from each poll for the new catalog source pod to be deployed
into the cluster. It may take longer for larger clusters. 
* Because OLM pulls down the image every poll interval and starts the pod, to see if its updated, the updated catalog pod must be able to be
scheduled onto the cluster. If the cluster is at absolutely maximum capacity, without autoscaling enabled, this feature may not work. 
* OLM checks to see whether the container ImageID has changed between the old and new catalog source image when determining if an upgrade
is in order. It does not actually parse the image content itself to check for later CSVs. If there is a bad upgrade to the catalog source image,
simply overwrite the tag with another version and it will be pulled down, or delete and recreate the catalog source. 
* The polling interval should be reasonably high to ensure the update functionality works as intended. Avoid intervals less than 15m. 
