FROM quay.io/bitnami/golang:1.17 AS builder
WORKDIR /go/src/stolostron/hub-cluster-controller
COPY . .
ENV GO_PACKAGE stolostron/hub-cluster-controller

RUN make build --warn-undefined-variables

FROM registry.access.redhat.com/ubi8/ubi-minimal:latest
ENV USER_UID=10001

COPY --from=builder /go/src/stolostron/hub-cluster-controller/ /
RUN microdnf update && microdnf clean all

USER ${USER_UID}
