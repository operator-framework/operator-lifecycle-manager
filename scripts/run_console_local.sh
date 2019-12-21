#!/bin/sh

# Colors definition
readonly RED=$(tput setaf 1)
readonly RESET=$(tput sgr0)
readonly GREEN=$(tput setaf 2)

# Check if Podman binary exists
verify_podman_binary() {
    if hash podman 2>/dev/null; then
        POD_MANAGER="podman"
    else
        POD_MANAGER="docker"
    fi
}

# Add port as 9000:9000 as arg when the SO is MacOS or Win
add_host_port_arg (){
    args="--net=host"
    if [ -z "${OSTYPE##*"darwin"*}" ] || uname -r | grep -q 'Microsoft'; then
      args="-p 9000:9000"
    fi
}

pull_ocp_console_image (){
   $POD_MANAGER pull quay.io/openshift/origin-console:latest
}

run_ocp_console_image (){
    secretname=$(kubectl get serviceaccount default --namespace=kube-system -o jsonpath='{.secrets[0].name}')
    endpoint=$(kubectl config view -o json | jq '{myctx: .["current-context"], ctxs: .contexts[], clusters: .clusters[]}' | jq 'select(.myctx == .ctxs.name)' | jq 'select(.ctxs.context.cluster ==  .clusters.name)' | jq '.clusters.cluster.server' -r)

    echo "Using $endpoint"
    $POD_MANAGER run -d --rm $args \
      -e BRIDGE_USER_AUTH="disabled" \
      -e BRIDGE_K8S_MODE="off-cluster" \
      -e BRIDGE_K8S_MODE_OFF_CLUSTER_ENDPOINT="$endpoint" \
      -e BRIDGE_K8S_MODE_OFF_CLUSTER_SKIP_VERIFY_TLS=true \
      -e BRIDGE_K8S_AUTH="bearer-token" \
      -e BRIDGE_K8S_AUTH_BEARER_TOKEN="$(kubectl get secret "$secretname" --namespace=kube-system -o template --template='{{.data.token}}' | base64 --decode)" \
      quay.io/openshift/origin-console:latest > /dev/null 2>&1 &
}

verify_ocp_console_image (){
    # Try for 5 seconds to reach the image
    for i in 1 2 3 4 5; do
      if [ "$($POD_MANAGER ps -q -f label=io.openshift.build.source-location=https://github.com/openshift/console)" ]; then
        container_id="$($POD_MANAGER ps -q -l -f label=io.openshift.build.source-location=https://github.com/openshift/console)"
        echo "${GREEN}The OLM is accessible via web console at:${RESET}"
        echo "${GREEN}http://localhost:9000/${RESET}"
        echo "${GREEN}Press Ctrl-C to quit${RESET}";
        $POD_MANAGER attach "$container_id"
        exit 0
      fi
      sleep 1  # Wait one second to try again
    done
    echo "${RED}Unable to run the console locally. May this port is in usage already.${RESET}"
    echo "${RED}Check if the OLM is not accessible via web console at: http://localhost:9000/${RESET}"
    exit 1
}

# Calling the functions
verify_podman_binary
add_host_port_arg
pull_ocp_console_image
run_ocp_console_image
verify_ocp_console_image
