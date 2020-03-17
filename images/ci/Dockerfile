FROM registry.svc.ci.openshift.org/openshift/release:golang-1.13 AS builder

WORKDIR /go/src/github.com/openshift/project-request-limit

COPY . .

RUN make build

FROM registry.svc.ci.openshift.org/ocp/4.5:base

COPY --from=builder /go/src/github.com/openshift/project-request-limit/_output/bin/project-request-limit /usr/bin/
