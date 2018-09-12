#!/usr/bin/env bash

set -e

if [[ ${#@} < 2 ]]; then
    echo "Usage: $0 gitlab_project_url"
    echo "* gitlab_project_url: URL of the gitlab project to queue on"
    echo "* gitlab_pipeline_id: ID of the gitlab pipeline to queue as"
    # echo "* gitlab_api_token: API token to use when accessing gitlab"
    exit 1
fi

gitlab_project_url="${1}/pipelines.json/?scope=running"
gitlab_pipeline_id="$2"
# gitlab_api_token="$3"

function is_head {
    ids="$(curl -s $gitlab_project_url | jq -c '.pipelines | map(.id) | sort')"
    length="$(echo $ids | jq -c 'length')"
    if [[ length -gt 1 ]] && [[ "$gitlab_pipeline_id" -ne "$(echo $ids | jq -c '.[0]')" ]]; then
        return 1
    fi
    echo "at head of pipeline queue. exiting"
}

# wait until the current pipeline is at the 
# head of the queue before exiting
while ! is_head ; do
    echo "waiting in pipeline queue..."
    sleep 5
done