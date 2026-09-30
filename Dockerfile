# Build the manager binary
# Build context must include both the provider repo and the NVIDIA
# infra-controller SDK. Use `docker build -f Dockerfile ../..` from
# the provider repo, or set context to the workspace root in CI.
FROM golang:1.26 AS builder

WORKDIR /workspace

# Copy the SDK dependency first for layer caching
COPY NVIDIA/infra-controller/rest-api/sdk/standard/ NVIDIA/infra-controller/rest-api/sdk/standard/

# Copy the Go Modules manifests
COPY fabiendupont/machine-api-provider-nvidia-infra-controller/go.mod fabiendupont/machine-api-provider-nvidia-infra-controller/go.mod
COPY fabiendupont/machine-api-provider-nvidia-infra-controller/go.sum fabiendupont/machine-api-provider-nvidia-infra-controller/go.sum

WORKDIR /workspace/fabiendupont/machine-api-provider-nvidia-infra-controller

RUN go mod download

# Copy the go source
COPY fabiendupont/machine-api-provider-nvidia-infra-controller/cmd/ cmd/
COPY fabiendupont/machine-api-provider-nvidia-infra-controller/pkg/ pkg/

# Build
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o manager cmd/manager/main.go

FROM gcr.io/distroless/static:nonroot

WORKDIR /

COPY --from=builder /workspace/fabiendupont/machine-api-provider-nvidia-infra-controller/manager .

USER 65532:65532

ENTRYPOINT ["/manager"]
