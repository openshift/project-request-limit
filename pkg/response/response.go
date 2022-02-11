package response

import (
	"net/http"

	admissionv1 "k8s.io/api/admission/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func WithAllowed(request *admissionv1.AdmissionRequest) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		UID:     request.UID,
		Allowed: true,
	}
}

func WithInternalServerError(request *admissionv1.AdmissionRequest, err error) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		UID:     request.UID,
		Allowed: false,
		Result: &metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    http.StatusInternalServerError,
			Reason:  metav1.StatusReasonInternalError,
			Message: err.Error(),
		},
	}
}

func WithForbidden(request *admissionv1.AdmissionRequest, err error) *admissionv1.AdmissionResponse {
	return &admissionv1.AdmissionResponse{
		UID:     request.UID,
		Allowed: false,
		Result: &metav1.Status{
			Status:  metav1.StatusFailure,
			Code:    http.StatusForbidden,
			Reason:  metav1.StatusReasonForbidden,
			Message: err.Error(),
		},
	}
}
