# Dependency Resolution and Upgrades

OLM manages the dependency resolution and upgrade lifecycle of running operators. In many ways, thes problems OLM faces are similar to other OS package managers like `apt`/`dkpg` and `yum`/`rpm`.

However, there is one constraint that similar systems don't generally have that OLM does: because operators are always running, OLM attempts to ensure that at no point in time are you left with a set of operators that do not work with each other. 

This means that OLM needs to never:
 
 - install a set of operators that require APIs that can't be provided
 - update an operator in a way that breaks another that depends upon it
 
The following examples motivate why OLM's dependency resolution and upgrade strategy works as it does, followed by a description of the current algorithm.

# Example: Deprecate dependant API

A and B are APIs (e.g. CRDs)

* A's provider depends on B
* B’s provider has a Subscription
* B’s provider updates to provide C but deprecates B

This results in:

* B no longer has a provider
* A no longer works

This is a case we prevent with OLM's upgrade strategy.


# Example: Version deadlock

A and B are APIs

* A's provider requires B
* B's provider requires A
* A's provider updates to (provide A2, require B2) and deprecate A
* B's provider updates to (provide B2, require A2) and deprecate B

If we attempt to update A without simultaneously updating B, or vice-versa, we won't be able to progress to new versions of the operators, even though a new compatible set can be found.

This is another case we prevent with OLM's upgrade strategy.


# Dependency resolution 

A Provider is an operator which "Owns" a CRD or APIService.

This algorithm will result in a successful update of a generation (in which as many operators which can be updated have been):

```
Consider the set of operators defined by running operators in a namespace:

  For each subscription in the namespace:
     if the subscription hasn't been checked before, find the latest CSV in the source/package/channel
        provisionally add the operator to the generation
     else
        check for a replacement in the source/package/channel
  
  // Generation resolution
  For each required API with no provider in gen:
    search through prioritized sources to pick a provider
    provisionally add any new operators found to the generation, this could also add to the required APIs without providers
    
  // Downgrade
  if there are still required APIs that can't be satisfied by sources:
    downgrade the operator(s) that require the APIs that can't be satisfied

  // Apply
  for each new operator required, install it into the cluster. Any newly resolved operator will be given a subscription to the channel/package/source it was discovered in.
```

The operator expansion loop is bounded by the total number of provided apis across sources (because a generation may not have multiple providers)

The downgrade loop will eventually stop, though it may contract back down to the original generation in the namespace. Downgrading an operator means it was in the previous generation. By definition, either its required apis are satisfied, or will be satisfied by the downgrade of another operator.
