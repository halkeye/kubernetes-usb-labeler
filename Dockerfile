# Build the manager binary
FROM golang:1.14-alpine as builder

RUN apk add --no-cache libusb-dev gcc alpine-sdk
# RUN apt-get update && apt-get install -y \
#     aufs-tools \
#     automake \
#     build-essential \
#     libusb-1.0-0-dev \
#  && rm -rf /var/lib/apt/lists/*``

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go

# Build
RUN CGO_ENABLED=1 GOOS=linux GOARCH=amd64 GO111MODULE=on go build -a -o manager main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM alpine 
# gcr.io/distroless/base:nonroot
WORKDIR /
RUN apk add --no-cache libusb
COPY --from=builder /workspace/manager /manager
USER nobody
#USER nonroot:nonroot

ENTRYPOINT ["/manager"]
