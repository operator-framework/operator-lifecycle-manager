# Supporting default polling for catalogs

## Description

Catalog polling is currently an opt-in feature that enables catalog users to push new content to the same image tag and have
that content brought into the cluster asynchronously by OLM. This is an opt-in feature that is enabled on the catalog
by setting an `UpdateStrategy` on the spec. 

The proposal is to enable default polling for all catalogs on the cluster, except for those that are backed by an image digest
(since the content of those are not upgradeable by definition). 

## Pros of default polling
* Catalogs stay up to date by default, providing a nice UX
* Polling becomes opt-out instead of opt-in, requiring users to know less of the OLM APIs to get catalog updates

## Cons of default polling
* More cluster egress - polling will pull images on a continuous basis regardless of whether the user is actually expecting 
the catalog to be upgraded 
* Declarative config - kubernetes is based around a declarative model where the user describes their desired state and the system
works to reconcile that state. By providing defaults that are not explicitly mentioned in the spec of the catalog source object
that principle is violated
* This may not be useful at all in the case of disconnected environments 

## Open Questions
* What should the default polling interval be?
* Should the defaulting behavior be implemented via a mutating admission webhook, or by OLM when creating the catalog?  
* Should there be an explicit opt-out besides using a digest-based catalog image? 