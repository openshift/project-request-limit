package main

import (
	"fmt"
	"sync"

	"github.com/openshift/project-request-limit/pkg/api"
	"github.com/openshift/project-request-limit/pkg/projectrequestlimit"
	"github.com/openshift/project-request-limit/pkg/response"
	admissionv1 "k8s.io/api/admission/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	restclient "k8s.io/client-go/rest"
)

type projectRequestLimit struct {
	sync.RWMutex
	initialized bool
	validator   projectrequestlimit.Validator
}

func (p *projectRequestLimit) ValidatingResource() (plural schema.GroupVersionResource, singular string) {
	return schema.GroupVersionResource{
		Group:    api.Group,
		Version:  api.Version,
		Resource: projectrequestlimit.Resource,
	}, projectrequestlimit.Singular
}

func (p *projectRequestLimit) Initialize(kubeClientConfig *restclient.Config, stopCh <-chan struct{}) (err error) {
	p.Lock()
	defer p.Unlock()
	p.initialized = true
	p.validator, err = projectrequestlimit.NewValidator(kubeClientConfig, stopCh)
	return
}

func (p *projectRequestLimit) Validate(request *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	p.RLock()
	defer p.RUnlock()
	if !p.initialized {
		response.WithInternalServerError(request, fmt.Errorf("not initialized"))
	}

	return p.validator.Validate(request)
}
