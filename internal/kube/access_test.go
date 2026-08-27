package kube

import (
	"testing"

	authorizationv1 "k8s.io/api/authorization/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCanISendsAuthoritativeSubresourceReview(t *testing.T) {
	t.Parallel()
	client := fake.NewSimpleClientset()
	var got authorizationv1.ResourceAttributes
	client.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		create := action.(k8stesting.CreateAction)
		review := create.GetObject().(*authorizationv1.SelfSubjectAccessReview)
		got = *review.Spec.ResourceAttributes
		return true, &authorizationv1.SelfSubjectAccessReview{
			Status: authorizationv1.SubjectAccessReviewStatus{
				Allowed:         true,
				Reason:          "authorized by webhook",
				EvaluationError: "secondary authorizer unavailable",
			},
		}, nil
	})

	cluster := &Cluster{clientset: client}
	result, err := cluster.CanI(t.Context(), WhoCanQuery{
		Verb: "create", Resource: DebugAttachResource, Namespace: "default",
	})
	if err != nil {
		t.Fatalf("CanI: %v", err)
	}
	if got.Verb != "create" || got.Resource != "pods" || got.Subresource != "ephemeralcontainers" || got.Namespace != "default" || got.Group != "" {
		t.Fatalf("resource attributes = %+v, want create pods/ephemeralcontainers in default", got)
	}
	if !result.Allowed || result.Denied || result.Reason != "authorized by webhook" || result.EvaluationError != "secondary authorizer unavailable" {
		t.Fatalf("result = %+v, want the API server status unchanged", result)
	}
}

func TestCanISendsOrdinaryPodReviewForDebugCopy(t *testing.T) {
	t.Parallel()
	client := fake.NewSimpleClientset()
	var got authorizationv1.ResourceAttributes
	client.PrependReactor("create", "selfsubjectaccessreviews", func(action k8stesting.Action) (bool, runtime.Object, error) {
		got = *action.(k8stesting.CreateAction).GetObject().(*authorizationv1.SelfSubjectAccessReview).Spec.ResourceAttributes
		return true, &authorizationv1.SelfSubjectAccessReview{
			Status: authorizationv1.SubjectAccessReviewStatus{Denied: true, Reason: "forbidden"},
		}, nil
	})

	result, err := (&Cluster{clientset: client}).CanI(t.Context(), WhoCanQuery{
		Verb: "create", Resource: DebugCopyResource, Namespace: "jobs",
	})
	if err != nil {
		t.Fatalf("CanI: %v", err)
	}
	if got.Resource != "pods" || got.Subresource != "" || got.Namespace != "jobs" {
		t.Fatalf("resource attributes = %+v, want create pods in jobs", got)
	}
	if result.Allowed || !result.Denied || result.Reason != "forbidden" {
		t.Fatalf("result = %+v, want explicit denial", result)
	}
}
