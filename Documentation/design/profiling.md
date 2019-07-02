# Profiling OLM Operators

OLM's `olm` and `catalog` commands support serving profiling samples via the `--profiling` option.

```sh
# run olm operator with profiling enabled
$ go run cmd/olm/main.go --profiling --kubeconfig ~/.kube/config
```

Samples are in a format recognized by [pprof](https://golang.org/pkg/net/http/pprof) and an index of available profile types is made available at `https://127.0.0.1:8080/debug/pprof`.

If profiling is enabled, but operators are running on a kubernetes cluster, a convienient way to expose samples locally is with `kubectl port-forward`:

```sh
# forward traffic from 127.0.0.1:8080 to port 8080 of catalog operator pods
$ kubectl -n <olm-namespace> port-forward deployments/catalog-operator
```

When profiling is enabled, `go tool pprof` can be used to export and visualize samples:

```sh
# assuming a catalog operator's samples are accessible at 127.0.0.1:8080:
# show in-use heap memory in top format
$ go tool pprof -top http://127.0.0.1:8080/debug/pprof/heap
Fetching profile over HTTP from http://127.0.0.1:8080/debug/pprof/heap
Saved profile in /Users/nhale/pprof/pprof.catalog.alloc_objects.alloc_space.inuse_objects.inuse_space.013.pb.gz
File: catalog
Type: inuse_space
Time: Jun 27, 2019 at 12:27pm (EDT)
Showing nodes accounting for 2202.74kB, 100% of 2202.74kB total
      flat  flat%   sum%        cum   cum%
  650.62kB 29.54% 29.54%   650.62kB 29.54%  bufio.NewWriterSize
  520.04kB 23.61% 53.15%   520.04kB 23.61%  golang.org/x/net/http2.NewFramer.func1
  520.04kB 23.61% 76.75%   520.04kB 23.61%  sync.(*Map).LoadOrStore
  512.03kB 23.25%   100%   512.03kB 23.25%  github.com/modern-go/reflect2.newUnsafeStructField
  ...

# save in-use objects graph to svg file
$ go tool pprof -sample_index=inuse_objects -svg http://127.0.0.1:8080/debug/pprof/heap
Fetching profile over HTTP from http://127.0.0.1:8080/debug/pprof/heap
Saved profile in /Users/<user>/pprof/pprof.catalog.alloc_objects.alloc_space.inuse_objects.inuse_space.01.pb.gz
Generating report in profile001.svg
```