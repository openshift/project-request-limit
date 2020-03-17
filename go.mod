module github.com/openshift/project-request-limit

go 1.13

require (
	github.com/openshift/build-machinery-go v0.0.0-20200211121458-5e3d6e570160
	github.com/openshift/generic-admission-server v1.14.0
	github.com/spf13/cobra v0.0.6 // indirect
	golang.org/x/sys v0.0.0-20191026070338-33540a1f6037 // indirect

	// k8s dependencies
	k8s.io/api v0.17.4
	k8s.io/apimachinery v0.17.4
	k8s.io/apiserver v0.17.4 // indirect
	k8s.io/client-go v0.17.4
)
