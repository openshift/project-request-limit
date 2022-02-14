package projectrequestlimit

import (
	"fmt"
	"testing"

	projectv1 "github.com/openshift/api/project/v1"
	userapi "github.com/openshift/api/user/v1"
	projectv1listers "github.com/openshift/client-go/project/listers/project/v1"
	userv1listers "github.com/openshift/client-go/user/listers/user/v1"
	admissionv1 "k8s.io/api/admission/v1"
	v1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/storage/names"
	corev1listers "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"sigs.k8s.io/yaml"
)

func TestMaxProjectByRequester(t *testing.T) {
	config := multiLevelConfig()
	tests := []struct {
		userLabels      map[string]string
		expectUnlimited bool
		expectedLimit   int64
	}{
		{
			userLabels:      map[string]string{"platinum": "yes"},
			expectUnlimited: true,
		},
		{
			userLabels:    map[string]string{"gold": "yes"},
			expectedLimit: 10,
		},
		{
			userLabels:    map[string]string{"silver": "yes", "bronze": "yes"},
			expectedLimit: 3,
		},
		{
			userLabels:    map[string]string{"unknown": "yes"},
			expectedLimit: 1,
		},
	}

	for _, tc := range tests {
		userLister := fakeUserLister([]*userapi.User{
			fakeUser("testuser", tc.userLabels),
		})
		reqLimit := projectRequestLimitValidator{
			userLister: userLister,
		}
		maxProjects, hasLimit, err := reqLimit.maxProjectsByRequester("testuser", config)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if tc.expectUnlimited {
			if hasLimit {
				t.Errorf("Expected no limit, but got limit for labels %v", tc.userLabels)
			}
			continue
		}
		if !tc.expectUnlimited && !hasLimit {
			t.Errorf("Did not expect unlimited for labels %v", tc.userLabels)
			continue
		}
		if maxProjects != tc.expectedLimit {
			t.Errorf("Did not get expected limit for labels %v. Got: %d. Expected: %d", tc.userLabels, maxProjects, tc.expectedLimit)
		}
	}
}

func TestProjectCountByRequester(t *testing.T) {
	nsLister := fakeNamespaceLister(map[string]projectCount{
		"user1": {1, 5}, // total 6, expect 4
		"user2": {5, 1}, // total 6, expect 5
		"user3": {1, 0}, // total 1, expect 1
	})
	reqLimit := &projectRequestLimitValidator{
		nsLister: nsLister,
	}
	tests := []struct {
		user   string
		expect int
	}{
		{
			user:   "user1",
			expect: 4,
		},
		{
			user:   "user2",
			expect: 5,
		},
		{
			user:   "user3",
			expect: 1,
		},
	}

	for _, test := range tests {
		actual, err := reqLimit.projectCountByRequester(test.user)
		if err != nil {
			t.Errorf("unexpected: %v", err)
		}
		if actual != int64(test.expect) {
			t.Errorf("user %s got %d, expected %d", test.user, actual, test.expect)
		}
	}

}

func TestAdmit(t *testing.T) {
	nsLister := fakeNamespaceLister(map[string]projectCount{
		"user1": {0, 1},
		"user2": {2, 2},
		"user3": {5, 3},
		"user4": {1, 0},

		"system:admin": {5, 0},
	})

	userLister := fakeUserLister([]*userapi.User{
		fakeUser("user2", map[string]string{"bronze": "yes"}),
		fakeUser("user3", map[string]string{"platinum": "yes"}),
		fakeUser("user4", map[string]string{"unknown": "yes"}),
	})

	reqLimit := projectRequestLimitValidator{
		userLister: userLister,
		nsLister:   nsLister,
	}

	tests := []struct {
		config          *projectv1.ProjectRequestLimit
		user            string
		expectForbidden bool
	}{
		// {
		// 	config: multiLevelConfig(),
		// 	user:   "user1",
		// },
		{
			config:          multiLevelConfig(),
			user:            "user2",
			expectForbidden: true,
		},
		{
			config: multiLevelConfig(),
			user:   "user3",
		},
		{
			config:          multiLevelConfig(),
			user:            "user4",
			expectForbidden: true,
		},
		{
			config: emptyConfig(),
			user:   "user2",
		},
		{
			config:          singleDefaultConfig(),
			user:            "user3",
			expectForbidden: true,
		},
		// {
		// 	config: singleDefaultConfig(),
		// 	user:   "user1",
		// },
		{
			// system:admin should always be allowed
			config: singleDefaultConfig(),
			user:   "system:admin",
		},
		{
			config: nil,
			user:   "user3",
		},
	}

	for _, tc := range tests {
		prlLister := fakeConfigLister(
			[]*projectv1.ProjectRequestLimit{
				tc.config,
			})
		reqLimit.prlLister = prlLister

		req := &admissionv1.AdmissionRequest{
			Operation: admissionv1.Create,
			Resource: metav1.GroupVersionResource{
				Group:    "project.openshift.io",
				Version:  "v1",
				Resource: "projectrequests",
			},
			SubResource: "",
			UserInfo: v1.UserInfo{
				Username: tc.user,
			},
		}

		resp := reqLimit.Validate(req)

		d, _ := yaml.Marshal(resp)
		fmt.Println(string(d))

		if !tc.expectForbidden && resp.Result != nil && resp.Result.Status == metav1.StatusFailure {
			t.Errorf("Got unexpected error for user %s: %v", tc.user, resp.Result.Message)
			continue
		}

		if tc.expectForbidden && resp.Result != nil && resp.Result.Status != metav1.StatusFailure {
			t.Errorf("Expecting forbidden error for user %s and config %#v. Got: %v", tc.user, tc.config, resp.Result.Message)
		}
	}
}

func TestIsApplicable(t *testing.T) {
	reqLimit := projectRequestLimitValidator{}
	tests := []struct {
		request *admissionv1.AdmissionRequest
		expect  bool
	}{
		{
			expect: true,
			request: &admissionv1.AdmissionRequest{
				Operation: admissionv1.Create,
				Resource: metav1.GroupVersionResource{
					Group:    "project.openshift.io",
					Version:  "v1",
					Resource: "projectrequests",
				},
				SubResource: "",
				UserInfo: v1.UserInfo{
					Username: "user1",
				},
			},
		},
		{
			expect: false,
			request: &admissionv1.AdmissionRequest{
				Operation: admissionv1.Create,
				Resource: metav1.GroupVersionResource{
					Group:    "project.openshift.io",
					Version:  "v1",
					Resource: "projectrequests",
				},
				SubResource: "status",
				UserInfo: v1.UserInfo{
					Username: "user1",
				},
			},
		},
		{
			expect:  false,
			request: &admissionv1.AdmissionRequest{},
		},
		{
			expect:  false,
			request: nil,
		},
	}

	for _, tc := range tests {
		actual := reqLimit.isApplicable(tc.request)
		if actual != tc.expect {
			t.Errorf("request %#v isApplicable=%t, expected %t", tc.request, actual, tc.expect)
		}
	}

}

func TestIsExempt(t *testing.T) {
	reqLimit := &projectRequestLimitValidator{}
	configs := map[string]*projectv1.ProjectRequestLimit{
		"empty":  emptyConfig(),
		"single": singleDefaultConfig(),
		"multi":  multiLevelConfig(),
	}
	tests := []struct {
		name   string
		expect bool
	}{
		{
			name:   "empty",
			expect: true,
		},
		{
			name:   "single",
			expect: false,
		},
		{
			name:   "multi",
			expect: false,
		},
	}

	for _, test := range tests {
		actual := reqLimit.isExempt(configs[test.name])
		if actual != test.expect {
			t.Errorf("config %s isExempt=%t, expected %t", test.name, actual, test.expect)
		}
	}
}

func intp(n int) *int64 {
	tmp := int64(n)
	return &tmp
}

func fakeNs(name string, terminating bool) *corev1.Namespace {
	ns := &corev1.Namespace{}
	ns.Name = names.SimpleNameGenerator.GenerateName("testns")
	ns.Annotations = map[string]string{
		"openshift.io/requester": name,
	}
	if terminating {
		ns.Status.Phase = corev1.NamespaceTerminating
	}
	return ns
}

func fakeUser(name string, labels map[string]string) *userapi.User {
	user := &userapi.User{}
	user.Name = name
	user.Labels = labels
	return user
}

func fakeUserLister(users []*userapi.User) userv1listers.UserLister {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, u := range users {
		indexer.Add(u)
	}
	return userv1listers.NewUserLister(indexer)
}

func fakeConfigLister(configs []*projectv1.ProjectRequestLimit) projectv1listers.ProjectRequestLimitLister {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for _, c := range configs {
		if c != nil {
			indexer.Add(c)
		}
	}
	return projectv1listers.NewProjectRequestLimitLister(indexer)
}

type projectCount struct {
	active      int
	terminating int
}

func fakeNamespaceLister(requesters map[string]projectCount) corev1listers.NamespaceLister {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{})
	for requester, count := range requesters {
		for i := 0; i < count.active; i++ {
			indexer.Add(fakeNs(requester, false))
		}
		for i := 0; i < count.terminating; i++ {
			indexer.Add(fakeNs(requester, true))
		}
	}
	return corev1listers.NewNamespaceLister(indexer)
}

func multiLevelConfig() *projectv1.ProjectRequestLimit {
	return &projectv1.ProjectRequestLimit{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Limits: []projectv1.ProjectLimitBySelector{
			{
				Selector:    map[string]string{"platinum": "yes"},
				MaxProjects: nil,
			},
			{
				Selector:    map[string]string{"gold": "yes"},
				MaxProjects: intp(10),
			},
			{
				Selector:    map[string]string{"silver": "yes"},
				MaxProjects: intp(3),
			},
			{
				Selector:    map[string]string{"bronze": "yes"},
				MaxProjects: intp(2),
			},
			{
				Selector:    map[string]string{},
				MaxProjects: intp(1),
			},
		},
	}
}

func emptyConfig() *projectv1.ProjectRequestLimit {
	return &projectv1.ProjectRequestLimit{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func singleDefaultConfig() *projectv1.ProjectRequestLimit {
	return &projectv1.ProjectRequestLimit{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Limits: []projectv1.ProjectLimitBySelector{
			{
				Selector:    nil,
				MaxProjects: intp(1),
			},
		},
	}
}
