# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

generate: manifests fmt

# Generate manifests e.g. CRD, RBAC etc.
manifests: controller-gen
	rm -f deployments/liqo/crds/*
	$(CONTROLLER_GEN) crd paths="./apis/..." output:crd:artifacts:config=deployments/liqo/crds

# Install gci if not available
gci:
ifeq (, $(shell which gci))
	@{ \
	set -e ;\
	TMP_DIR=$$(mktemp -d) ;\
	cd $$TMP_DIR ;\
	go mod init tmp ;\
	go get github.com/daixiang0/gci@v0.2.9 ;\
	rm -rf $$TMP_DIR ;\
	}
GCI=$(GOBIN)/gci
else
GCI=$(shell which gci)
endif

# Run go fmt against code
fmt: gci
	go mod tidy
	go fmt ./...
	find . -type f -name '*.go' -a ! -name '*zz_generated*' -exec $(GCI) -local github.com/CapacitorSet/liqo-broker -w {} \;

# Generate gRPC files
grpc: protoc
	$(PROTOC) --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative resource-reader.proto

protoc:
ifeq (, $(shell which protoc))
	@{ \
	PB_REL="https://github.com/protocolbuffers/protobuf/releases" ;\
	version=3.15.5 ;\
	arch=x86_64 ;\
	curl -LO $${PB_REL}/download/v$${version}/protoc-$${version}-linux-$${arch}.zip ;\
	unzip protoc-$${version}-linux-$${arch}.zip -d $${HOME}/.local ;\
	rm protoc-$${version}-linux-$${arch}.zip ;\
	PROTOC_TMP_DIR=$$(mktemp -d) ;\
	cd $$PROTOC_TMP_DIR ;\
	go mod init tmp ;\
	go get google.golang.org/protobuf/cmd/protoc-gen-go ;\
	go get google.golang.org/grpc/cmd/protoc-gen-go-grpc ;\
	rm -rf $$PROTOC_TMP_DIR ;\
	}
endif
PROTOC=$(shell which protoc)
