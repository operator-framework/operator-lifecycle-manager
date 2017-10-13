local jpyutils = import "jpy-utils.libsonnet";

jpyutils +
jpyutils.extStd +
{
    containerName(repo, tag):: "%s:%s" % [repo, tag],
    ci: jpyutils.gitlabCi,
}
