DIR := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))
VERSION ?= $(shell $(DIR)/../tools/git-version.sh)

IMAGE_REPO ?= quay.io/coreos/tectonic-alm-operator
SUDO ?= sudo

all: docker-image

.PHONY: dev
dev:
	$(SUDO) docker run --rm=true \
		-v $(DIR)/manifests:/manifests:ro \
		-v $(KUBECONFIG):/kubeconfig \
		--entrypoint /tectonic-x-operator \
		quay.io/coreos/tectonic-x-operator:1bedef404a333bc736e7a16618545876b6508a00 \
		--kubeconfig /kubeconfig \
		--operator-name=tectonic-alm-operator \
		--appversion-name=tectonic-alm-operator \
		--v=4

.PHONY: docker-image
docker-image:
	@$(SUDO) docker build --rm=true -t $(IMAGE_REPO):$(VERSION) .

.PHONY: docker-push
docker-push: docker-image
	@$(SUDO) docker push $(IMAGE_REPO):$(VERSION)