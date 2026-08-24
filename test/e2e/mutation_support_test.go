//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
)

func mutationClient(t *testing.T) kubernetes.Interface {
	t.Helper()
	RequireCluster(t)
	return e2eClientset(t)
}

func cleanupObject(t *testing.T, resource schema.GroupResource, name string, delete func(context.Context) error) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := delete(ctx); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("cleaning up %s %s: %v", resource.Resource, name, err)
		}
	})
}

func waitForAPI(t *testing.T, description string, check func(context.Context) (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(Settle)
	var lastErr error
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		ok, err := check(ctx)
		cancel()
		if ok {
			return
		}
		lastErr = err
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s: %v", description, lastErr)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func waitForDeleted(t *testing.T, description string, get func(context.Context) error) {
	t.Helper()
	waitForAPI(t, description+" to be deleted", func(ctx context.Context) (bool, error) {
		err := get(ctx)
		return apierrors.IsNotFound(err), err
	})
}

func requireNewResourceVersion(t *testing.T, before, after string) {
	t.Helper()
	if before == "" || after == "" || before == after {
		t.Fatalf("resourceVersion did not advance: before=%q after=%q", before, after)
	}
}

func replaceTypedValue(a *App, old, value string) {
	a.t.Helper()
	for range len(old) {
		a.Press("backspace")
	}
	a.Type(value)
}

func (a *App) waitForSelectedRow(t *testing.T, name string) {
	t.Helper()
	frame, ok := a.poll(func(frame string) bool {
		for _, line := range strings.Split(frame, "\n") {
			if selectedTableRow(line, name) {
				return true
			}
		}
		return false
	}, Settle)
	if !ok {
		t.Fatalf("row %q never became selected:\n%s", name, frame)
	}
}

func (a *App) waitForRowText(t *testing.T, name, text string) {
	t.Helper()
	frame, ok := a.poll(func(frame string) bool {
		for _, line := range strings.Split(frame, "\n") {
			if strings.Contains(line, name) && strings.Contains(line, text) {
				return true
			}
		}
		return false
	}, Settle)
	if !ok {
		t.Fatalf("row %q never contained %q:\n%s", name, text, frame)
	}
}

func createConfigMap(t *testing.T, name string, data map[string]string) *corev1.ConfigMap {
	t.Helper()
	client := mutationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cm, err := client.CoreV1().ConfigMaps(Namespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace}, Data: data,
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("creating configmap %s: %v", name, err)
	}
	cleanupObject(t, schema.GroupResource{Resource: "configmaps"}, name, func(ctx context.Context) error {
		return client.CoreV1().ConfigMaps(Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	})
	return cm
}

func createSecret(t *testing.T, name string, data map[string][]byte) *corev1.Secret {
	t.Helper()
	client := mutationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	secret, err := client.CoreV1().Secrets(Namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace}, Type: corev1.SecretTypeOpaque, Data: data,
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("creating secret %s: %v", name, err)
	}
	cleanupObject(t, schema.GroupResource{Resource: "secrets"}, name, func(ctx context.Context) error {
		return client.CoreV1().Secrets(Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	})
	return secret
}

func createDeployment(t *testing.T, name string, replicas int32, configMap string) *appsv1.Deployment {
	t.Helper()
	client := mutationClient(t)
	labels := map[string]string{"app": name, "phase3": "before"}
	container := corev1.Container{
		Name: "worker", Image: "busybox:1.37",
		Command: []string{"/bin/sh", "-c", "while true; do sleep 30; done"},
		Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
			corev1.ResourceCPU: resourceQuantity("10m"), corev1.ResourceMemory: resourceQuantity("16Mi"),
		}},
	}
	if configMap != "" {
		container.Env = []corev1.EnvVar{{Name: "MODE", ValueFrom: &corev1.EnvVarSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: configMap}, Key: "mode",
		}}}}
	}
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: Namespace, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": name}},
			Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": name}}, Spec: corev1.PodSpec{Containers: []corev1.Container{container}}},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	created, err := client.AppsV1().Deployments(Namespace).Create(ctx, dep, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("creating deployment %s: %v", name, err)
	}
	cleanupObject(t, schema.GroupResource{Resource: "deployments"}, name, func(ctx context.Context) error {
		return client.AppsV1().Deployments(Namespace).Delete(ctx, name, metav1.DeleteOptions{})
	})
	return created
}

func resourceQuantity(value string) resource.Quantity {
	return resource.MustParse(value)
}

func getJobByAnnotation(t *testing.T, key, value string) *batchv1.Job {
	t.Helper()
	client := mutationClient(t)
	var found *batchv1.Job
	waitForAPI(t, fmt.Sprintf("job with annotation %s=%s", key, value), func(ctx context.Context) (bool, error) {
		list, err := client.BatchV1().Jobs(Namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		for i := range list.Items {
			if list.Items[i].Annotations[key] == value {
				found = list.Items[i].DeepCopy()
				return true, nil
			}
		}
		return false, nil
	})
	return found
}

func deleteJobNow(t *testing.T, name string) {
	t.Helper()
	zero := int64(0)
	client := mutationClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := client.BatchV1().Jobs(Namespace).Delete(ctx, name, metav1.DeleteOptions{GracePeriodSeconds: &zero, PropagationPolicy: ptr(metav1.DeletePropagationBackground)})
	cancel()
	if err != nil && !apierrors.IsNotFound(err) {
		t.Errorf("deleting job %s: %v", name, err)
		return
	}
	waitForDeleted(t, "job "+name, func(ctx context.Context) error {
		_, err := client.BatchV1().Jobs(Namespace).Get(ctx, name, metav1.GetOptions{})
		return err
	})
}

func ptr[T any](v T) *T { return &v }
