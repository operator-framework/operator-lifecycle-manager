{
    local topSelf = self,
    # Generate a sequence array from 1 to i
    seq(i):: (
        [x for x in std.range(1, i)]
    ),

    objectFieldsHidden(obj):: (
        std.setDiff(std.objectFieldsAll(obj), std.objectFields(obj))
    ),

    objectFlatten(obj):: (
        // Merge 1 level dict depth into toplevel
        local visible = {
            [k]: obj[j][k]
            for j in std.objectFieldsAll(obj)
            for k in std.objectFieldsAll(obj[j])
        };

        visible
    ),

    inArray(array, x):: (
        x in self.set(array)
    ),

    compact(array):: (
        [x for x in array if x != null]
    ),

    objectValues(obj):: (
        local fields = std.objectFields(obj);
        [obj[key] for key in fields]
    ),

    objectMap(func, obj):: (
        local fields = std.objectFields(obj);
        { [key]: func(obj[key]) for key in fields }
    ),

    capitalize(str):: (
        std.char(std.codepoint(str[0]) - 32) + str[1:]
    ),

    set(array)::
        { [key]: key for key in array },

    tests: {
        inArray: (assert topSelf.inArray(["a", "foo"], "foo") == true;
                  assert topSelf.inArray([], "af") == false;
                  assert topSelf.inArray(["a", "foo"], "bad") == false;
                  true),
    },


    containerName(repo, tag):: "%s:%s" % [repo, tag],

    docker: {
        local Docker = self,

        login(server, user, password):: [
            "docker login -u %s -p %s %s" % [user, password, server],
        ],

        cp(image, src, dest):: [
            "docker create %s | xargs -I{} docker cp {}:%s %s" % [image, src, dest],
        ],

        run(image, cmd, opts=[]):: [
            local optstr = std.join(" ", opts);
            'docker run %s %s %s' % [optstr, image, cmd],
        ],

        build_and_push(image, cache=true, args={}, extra_opts=[]):: (
            Docker.build(image, cache, args, extra_opts) +
            Docker.push(image)
        ),

        build(image, cache=true, args={}, extra_opts=[]):: [
            local cache_opt = if cache == false
            then '--no-cache'
            else if std.type(cache) == 'boolean'
            then '--no-cache'
            else '--cache-from=%s' % cache;
            local buildargs_opt = std.join(" ", [
                "--build-arg %s=%s" % [key, args[key]]
                for key in std.objectFields(args)
            ]);
            local opts = std.join(" ", [buildargs_opt, cache_opt] + extra_opts);
            'docker build %s -t %s . ' % [opts, image],
        ],

        push(image):: [
            'docker push %s' % image,
        ],

        rename(src, dest):: [
            'docker pull %s' % src,
            'docker tag %s %s' % [src, dest],
            'docker push %s' % [dest],
        ],

    },

    helm: {
        // uses app-registry
        upgrade(name, appname, namespace="default", vars={}, extra_opts=[]):: [
            local set_opts = [
                "--set %s=%s" % [key, vars[key]]
                for key in std.objectFields(vars)
            ];

            std.join(" ", [
                              "helm registry upgrade %s -- --install %s" % [name, appname],
                              "--namespace=%s" % namespace,
                          ] +
                          set_opts +
                          extra_opts),
        ],
    },

    appr: {

        login(server, user, password):: [
            "appr login -u %s -p %s %s" % [user, password, server],
        ],

        push(name, channel=null, force=false):: [
            std.join(" ",
                     ["appr push %s" % name] +
                     if channel != null then ["--channel %s" % channel] else [] +
                                                                             if force == true then ["-f"] else []),
        ],

    },

    k8s: {
        createNamespace(name):: [
            "kubectl create ns %s" % name + " || true",
        ],

        createPullSecret(name, namespace, server, user, password):: [
            std.join(" ",
                     [
                         "kubectl create secret docker-registry %s" % name,
                         "--docker-server %s" % server,
                         "--docker-username %s" % user,
                         "--docker-password %s" % password,
                         "--docker-email ignored@example.com",
                         "--namespace=%s" % namespace,
                         "|| true",
                     ]),
        ],

        get(type, name, namespace, extra_opts=[]):: [
            "kubectl get %s %s -n %s %s" % [
                type,
                name,
                namespace,
                std.join(" ", extra_opts),
            ],
        ],

        apply(filepath, namespace=null, extra_opts=[]):: [
            std.join(
                " ",
                ["kubectl apply -f %s" % filepath] +
                if namespace != null then ["--namespace %s" % namespace] else [] +
                                                                              extra_opts
            ),
        ],

    },

    ci: {

        mergeJob(base_job, jobs, stage=null):: {
            [job_name]: base_job + jobs[job_name] +
                        if stage != null then { stage: stage } else {}
            for job_name in std.objectFields(jobs)
        },

        only(key):: (
            if key == "master"
            then { only: ['master', 'tags'] }
            else { only: ['branches'] }
        ),

        setManual(key, values):: (
            if std.objectHas(topSelf.set(values), key)
            then { when: 'manual' }
            else { only: ['branches'] }
        ),
    },
}
