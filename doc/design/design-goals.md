# General Design Goals

Whether designing new features or redesigning old ones, OLM should maintain these general design goals:

## Build (reasonably) small, sharp features

All designs should be kept as simple and tightly scoped as is reasonably possible. Where possible, attempt to follow the [unix philosophy](https://en.wikipedia.org/wiki/Unix_philosophy) for writing small, sharp features that compose well. Additionally, when writing a new proposal, always remember to [keep it simple ...student!](https://en.wikipedia.org/wiki/KISS_principle)

## Upstream first

Before adding a feature, check to see if an upstream Kubernetes community has a project that we can extend, adopt, or contribute to that attempts to solve the same or similar problems. If such a project is found, but for some reason cannot be used in lieu of an OLM-specific solution, make the community aware of OLM's usecase and attempt to open a dialog on the matter. Additionally, any OLM-specific proposals should avoid relying on the features of downstream Kubernetes distributions.

## Operators should remain agnostic of OLM's APIs

Be cautious when adding any feature that requires operators to know about an OLM interface. This promotes lock-in and makes adoption more difficult, usually requiring code changes to operators. Generally, a proposal of this category should only be accepted when all of the following are true:

1. The feature cannot easily be designed in a way that operators are agnostic of its interface and that interface is straightforward
2. The feature is likely to be accepted upstream
3. The feature is optional

## Adhere to Kubernetes API conventions

Ensure new APIs adhere to the [Kubernetes API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md). When making changes to an existing API, make sure those changes follow the [corresponding backwards compatibility rules](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api_changes.md).
