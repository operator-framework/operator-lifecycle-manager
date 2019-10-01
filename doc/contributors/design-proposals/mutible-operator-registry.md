# Ability to Modify Operator Registry

## Motivation

An index image is defined as a set of operator bundle images that are released independently and optimized in a way that can be productized by operator authors and pipeline developers. The intent of the index image is to aggregate these images that are out of scope of this enhancement. As an operator author, I want to be able to modify an existing index so I don't have to build it from scratch each time.

## Proposal

This implementation makes the assumption that a separate process is built that generates container images that contain individual operator bundles for each version of an operator.

To start, let's define what the operator-registry does today. It:

1. Takes a set of manifest files and outputs a database file.
2. Takes a database file and serves a grpc api that serves content from that db
3. Takes a configmap reference, builds a db and serves a grpc api that serves content from that db
4. Takes an app-registry path/namespace reference, builds a db and serves a grpc api that serves content from that db

And what we would like it to:

1. Use a reference to a container image (also referred to here as the operator index) rather than app-registry namespaces to expose installable operators on a cluster
2. Build an operator-registry database that serves the data required by OLM to drive install and UI workflows
3. Have a way to optimize the build workflow of operator-registry databases (which currently drive OLM's workflows) from previous versions of the database plus new content (ex. new operator at new version) so that the database need not be built from scratch every time they are created.

### Updating the Operator Registry to insert incremental updates

We want to add create db, delete, and batch insert APIs to the model layer of operator-registry and a new set of operator registry commands to utilize those new APIs:

`operator-registry create`

- inputs: none

- outputs: empty operator registry database
    
`operator-registry add`

- inputs: $operatorBundleImagePath, $dbFile

- outputs: updated database file

ex: `operator-registry add quay.io/community-operators/foo:sha256@abcd123 example.db`
   
`operator-registry add --batch`

- inputs: $operatorBundleImagePath, $dbFile

- outputs: updated database file

ex: `operator-registry add "quay.io/community-operators/foo:sha256@abcd123,quay.io/community-operators/bar:sha256@defg456" example.db`
    
`operator-registry delete`

- inputs: $operatorName, $dbFile

- outputs: updated database file without $operatorName included

ex: `operator-registry delete bar example.db`

`operator-registry delete-latest`

- inputs: $operatorName, $dbFile

- outputs: updated database file without latest version of $operatorName included

ex: `operator-registry delete-latest foo example.db`

### Implementation details

#### Add

To achieve this we need to pull down images from a registry using podman, extract the bundle files from the container's filesystem and insert the bundle into the database. For each channel defined in the bundle's `package.yaml`:

1. If the channel points to a csv that is not in this bundle.

In this case, IGNORE this channel.

2. The bundle is attached to an operator that is not in the database.

In this case, we can simply add this bundle to the database.

3. The bundle is attached to an operator in the database. If the channel points to a csv that is in this bundle:

    1. This channel is not in the database. Insert the bundle for this new channel into the database.

    2. This channel is in the database. SELECT all csv's of that channel from the database. By looking at the replaces and skips fields of the bundle's csv, we can know where this new csv fits in the update graph. If:
    
        - That csv is the latest version for that channel. Insert the bundle for that channel.

        - That csv defines a replacement that another csv is already replacing. Suppose we have we are trying to insert A which replaces C. But B also replaces C. We need the operator owner to either specify in A that it skips/replaces B or to delete B from the database and update B such that it replaces/skips A. Without these conditions we cannot insert.

        - The csv specifies a replacement that cannot be found. Cannot insert.

        - The csv is already in the database. Cannot insert.

4. The bundle is an updated version of an operator that is in the database.

Not allowed.

#### Add --batch

This is similar to `add`. We have two options:

1. We can naively, sequentially insert from the list of images

Pros: simple to implement

Cons: ordering of images may affect the state of the db?

2. We can preprocess the images to minimize collisions or optimize for a given objective

Pros: versatile

Cons: Not clear what objective we would need to optimze for

#### Delete

Delete will remove all channels and versions of a given operator from the database.

We need to warn the user before deleting an operator whose APIs are required by another operator in that same database.

#### Delete-latest

Delete-latest will remove the latest version added to an operator's channel. The latest added version to the database is defined as the version that is at the tip of the update graph for a given channel that has the latest insert time (or index key) among all channels.

We should warn the user about APIs that are required by another operator that are not in previous versions of this operator.

