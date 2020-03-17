package main

import (
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	restclient "k8s.io/client-go/rest"
)

type projectRequestLimitAdmissionHook struct {
}

// Initialize is called as a post-start hook
func (p *projectRequestLimitAdmissionHook) Initialize(kubeClientConfig *restclient.Config, stopCh <-chan struct{}) error {
	return nil
}

func (p *projectRequestLimitAdmissionHook) ValidatingResource() (plural schema.GroupVersionResource, singular string) {
	return schema.GroupVersionResource{
			Group:    "projectrequestlimits.admission.project.openshift.io",
			Version:  "v1",
			Resource: "validatingadmissionreviews",
		},
		"validatingadmissionreview"
}

func (p *projectRequestLimitAdmissionHook) Validate(request *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	response := &admissionv1.AdmissionResponse{}

	return response
}
