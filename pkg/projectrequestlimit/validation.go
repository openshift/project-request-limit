package projectrequestlimit

import (
	"fmt"
	"reflect"
	"time"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apiserver/pkg/authentication/serviceaccount"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	corev1listers "k8s.io/client-go/listers/core/v1"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"

	projectv1 "github.com/openshift/api/project/v1"
	uservalidation "github.com/openshift/apiserver-library-go/pkg/apivalidation"
	projectclient "github.com/openshift/client-go/project/clientset/versioned"
	projectinformers "github.com/openshift/client-go/project/informers/externalversions"
	projectv1listers "github.com/openshift/client-go/project/listers/project/v1"
	userclient "github.com/openshift/client-go/user/clientset/versioned"
	userinformers "github.com/openshift/client-go/user/informers/externalversions"
	userv1listers "github.com/openshift/client-go/user/listers/user/v1"
	"github.com/openshift/project-request-limit/pkg/response"
)

const (
	// allowedTerminatingProjects is the number of projects that are owned by a user, are in terminating state,
	// and do not count towards the user's limit.
	allowedTerminatingProjects = 2
	defaultResyncPeriod        = 4 * time.Hour
	Resource                   = "projectrequestlimits"
	Singular                   = "projectrequestlimit"
	Name                       = "projectrequestlimit"
)

type Validator interface {
	Validate(request *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse
}

func NewValidator(kubeClientConfig *restclient.Config, stopCh <-chan struct{}) (Validator, error) {
	client, err := kubernetes.NewForConfig(kubeClientConfig)
	if err != nil {
		return nil, err
	}

	userClient, err := userclient.NewForConfig(kubeClientConfig)
	if err != nil {
		return nil, err
	}

	projectClient, err := projectclient.NewForConfig(kubeClientConfig)
	if err != nil {
		return nil, err
	}

	// ProjectRequestLimit shared informer
	piFactory := projectinformers.NewSharedInformerFactory(projectClient, defaultResyncPeriod)
	prlInformer := piFactory.Project().V1().ProjectRequestLimits().Informer()
	go prlInformer.Run(stopCh)

	// User shared informer
	userFactory := userinformers.NewSharedInformerFactory(userClient, defaultResyncPeriod)
	userInformer := userFactory.User().V1().Users().Informer()
	go userInformer.Run(stopCh)

	// Namespace shared informer
	nsFactory := informers.NewSharedInformerFactory(client, defaultResyncPeriod)
	nsInformer := nsFactory.Core().V1().Namespaces().Informer()
	go nsInformer.Run(stopCh)

	// Wait for informer caches to sync
	syncResult := piFactory.WaitForCacheSync(stopCh)
	if r, ok := syncResult[reflect.TypeOf(prlInformer)]; ok && !r {
		return nil, fmt.Errorf("failed to wait for ProjectRequestLimit informer cache to sync")
	}
	syncResult = userFactory.WaitForCacheSync(stopCh)
	if r, ok := syncResult[reflect.TypeOf(userInformer)]; ok && !r {
		return nil, fmt.Errorf("failed to wait for User informer cache to sync")
	}
	if !cache.WaitForCacheSync(stopCh, nsInformer.HasSynced) {
		return nil, fmt.Errorf("failed to wait for Namespace informer cache to sync")
	}

	return &projectRequestLimitValidator{
		prlLister:  piFactory.Project().V1().ProjectRequestLimits().Lister(),
		userLister: userFactory.User().V1().Users().Lister(),
		nsLister:   nsFactory.Core().V1().Namespaces().Lister(),
	}, nil
}

type projectRequestLimitValidator struct {
	prlLister  projectv1listers.ProjectRequestLimitLister
	userLister userv1listers.UserLister
	nsLister   corev1listers.NamespaceLister
}

var _ Validator = (*projectRequestLimitValidator)(nil)

func (p *projectRequestLimitValidator) Validate(request *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	if !p.isApplicable(request) {
		// should not have reached this endpoint, shouldn't happen with correct ValidatingWebhookConfiguration
		return response.WithAllowed(request)
	}

	instance, err := p.getConfig()
	if err != nil {
		if apierrors.IsNotFound(err) {
			// no limits defined
			return response.WithAllowed(request)
		}
		// general error
		return response.WithInternalServerError(request, err)
	}

	if p.isExempt(instance) {
		// no limits defined
		return response.WithAllowed(request)
	}

	userName := request.UserInfo.Username
	projectCount, err := p.projectCountByRequester(userName)
	if err != nil {
		return response.WithInternalServerError(request, err)
	}
	maxProjects, hasLimit, err := p.maxProjectsByRequester(userName, instance)
	if err != nil {
		return response.WithInternalServerError(request, err)
	}
	if hasLimit && projectCount >= maxProjects {
		return response.WithForbidden(request, fmt.Errorf("user %s cannot create more than %d project(s)", userName, maxProjects))
	}

	return response.WithAllowed(request)
}

func (p *projectRequestLimitValidator) isApplicable(request *admissionv1.AdmissionRequest) bool {
	if request == nil {
		return false
	}
	if request.Operation != admissionv1.Create {
		return false
	}
	desiredGR := projectv1.Resource("projectrequests")
	if request.Resource.Group != desiredGR.Group {
		return false
	}
	if request.Resource.Resource != desiredGR.Resource {
		return false
	}
	if request.SubResource != "" {
		return false
	}
	return true
}

func (p *projectRequestLimitValidator) isExempt(instance *projectv1.ProjectRequestLimit) bool {
	if len(instance.Limits) == 0 &&
		(instance.MaxProjectsForServiceAccounts == nil || *instance.MaxProjectsForServiceAccounts == 0) &&
		(instance.MaxProjectsForSystemUsers == nil || *instance.MaxProjectsForSystemUsers == 0) {
		return true
	}
	return false
}

func (p *projectRequestLimitValidator) getConfig() (*projectv1.ProjectRequestLimit, error) {
	// list, err := p.prlLister.List(labels.Everything())
	// if err != nil {
	// 	return nil, err
	// }
	// if len(list) == 1 {
	// 	return list[0], nil
	// }
	// if len(list) == 0 {
	// 	return nil, apierrors.NewNotFound(
	// 		projectv1.Resource(Resource),
	// 		"cluster",
	// 	)
	// }
	// if len(list) > 1 {
	// 	return nil, fmt.Errorf("too many ProjectResourceLimit resources exist")
	// }
	return p.prlLister.Get("cluster")
}

// maxProjectsByRequester returns the maximum number of projects allowed for a given user, whether a limit exists, and an error
// if an error occurred. If a limit doesn't exist, the maximum number should be ignored.
func (p *projectRequestLimitValidator) maxProjectsByRequester(userName string, limits *projectv1.ProjectRequestLimit) (int64, bool, error) {
	// service accounts have a different ruleset, check them
	if _, _, err := serviceaccount.SplitUsername(userName); err == nil {
		if limits.MaxProjectsForServiceAccounts == nil {
			return 0, false, nil
		}

		return *limits.MaxProjectsForServiceAccounts, true, nil
	}

	// if we aren't a valid username, we came in as cert user for certain, use our cert user rules
	if reasons := uservalidation.ValidateUserName(userName, false); len(reasons) != 0 {
		if limits.MaxProjectsForSystemUsers == nil {
			return 0, false, nil
		}

		return *limits.MaxProjectsForSystemUsers, true, nil
	}

	user, err := p.userLister.Get(userName)
	if err != nil {
		return 0, false, err
	}
	userLabels := labels.Set(user.Labels)

	for _, limit := range limits.Limits {
		selector := labels.Set(limit.Selector).AsSelector()
		if selector.Matches(userLabels) {
			if limit.MaxProjects == nil {
				return 0, false, nil
			}
			return *limit.MaxProjects, true, nil
		}
	}
	return 0, false, nil
}

func (p *projectRequestLimitValidator) projectCountByRequester(userName string) (int64, error) {
	// our biggest clusters have less than 10k namespaces.  project requests are infrequent.  This is iterating on an
	// in memory set of pointers.  I can live with all this to avoid a secondary cache.
	allNamespaces, err := p.nsLister.List(labels.Everything())
	if err != nil {
		return 0, err
	}
	namespaces := []*corev1.Namespace{}
	for i := range allNamespaces {
		ns := allNamespaces[i]
		if ns.Annotations[projectv1.ProjectRequesterAnnotation] == userName {
			namespaces = append(namespaces, ns)
		}
	}

	terminatingCount := 0
	for _, ns := range namespaces {
		if ns.Status.Phase == corev1.NamespaceTerminating {
			terminatingCount++
		}
	}
	count := len(namespaces)
	if terminatingCount > allowedTerminatingProjects {
		count -= allowedTerminatingProjects
	} else {
		count -= terminatingCount
	}
	return int64(count), nil
}
