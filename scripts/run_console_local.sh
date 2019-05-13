#!/bin/bash

# Colors definition
readonly RED=$(tput setaf 1)
readonly RESET=$(tput sgr0)
readonly BLUE=$(tput setaf 2)

# Add port as 9000:9000 as arg when the SO is MacOS or Win
add_host_port_arg (){
    args="--net=host"
    if [[ "$OSTYPE" == "darwin"* ]] || [[ "$(< /proc/version)" == *"@(Microsoft|WSL)"* ]]; then
      args="-p 9000:9000"
    fi
}

pull_ocp_console_image (){
   docker pull quay.io/openshift/origin-console:latest
}

run_docker_console (){
    secretname=$(kubectl get serviceaccount default --namespace=kube-system -o jsonpath='{.secrets[0].name}')
    endpoint=$(kubectl config view -o json | jq '{myctx: .["current-context"], ctxs: .contexts[], clusters: .clusters[]}' | jq 'select(.myctx == .ctxs.name)' | jq 'select(.ctxs.context.cluster ==  .clusters.name)' | jq '.clusters.cluster.server' -r)

    echo -e "Using $endpoint"
    command -v docker run -it $args \
      -e BRIDGE_USER_AUTH="disabled" \
      -e BRIDGE_K8S_MODE="off-cluster" \
      -e BRIDGE_K8S_MODE_OFF_CLUSTER_ENDPOINT=$endpoint \
      -e BRIDGE_K8S_MODE_OFF_CLUSTER_SKIP_VERIFY_TLS=true \
      -e BRIDGE_K8S_AUTH="bearer-token" \
      -e BRIDGE_K8S_AUTH_BEARER_TOKEN=$(kubectl get secret "$secretname" --namespace=kube-system -o template --template='{{.data.token}}' | base64 --decode) \
      quay.io/openshift/origin-console:latest &> /dev/null

    docker_exists=${?}; if [[ ${docker_exists} -ne 0 ]]; then
        echo -e "${BLUE}The OLM is accessible via web console at:${RESET}"
        echo -e "${BLUE}https://localhost:9000/${RESET}"
    else
        echo -e "${RED}Unable to run the console locally. May this port is in usage already. ${RESET}"
        echo -e "${RED}Check if the OLM is not accessible via web console at: https://localhost:9000/. ${RESET}"
        exit 1
    fi

}


# Calling the functions
add_host_port_arg
pull_ocp_console_image
run_docker_console


