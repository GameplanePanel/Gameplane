package controlplane

import (
	"context"
	"encoding/json"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type secrets struct{ objects }
type clusters struct{ objects }

func secretObject(secret *corev1.Secret) (*unstructured.Unstructured, error) {
	secret = secret.DeepCopy()
	if secret.Data == nil {
		secret.Data = map[string][]byte{}
	}
	for key, value := range secret.StringData {
		secret.Data[key] = []byte(value)
	}
	secret.StringData = nil
	secret.APIVersion = "v1"
	secret.Kind = "Secret"
	data, err := json.Marshal(secret)
	if err != nil {
		return nil, fmt.Errorf("encode credential: %w", err)
	}
	obj := &unstructured.Unstructured{}
	if err := json.Unmarshal(data, &obj.Object); err != nil {
		return nil, fmt.Errorf("decode credential object: %w", err)
	}
	return obj, nil
}

func asSecret(obj *unstructured.Unstructured, err error) (*corev1.Secret, error) {
	if err != nil {
		return nil, err
	}
	data, err := json.Marshal(obj.Object)
	if err != nil {
		return nil, fmt.Errorf("encode credential object: %w", err)
	}
	secret := &corev1.Secret{}
	if err := json.Unmarshal(data, secret); err != nil {
		return nil, fmt.Errorf("decode credential: %w", err)
	}
	return secret, nil
}

func (s *secrets) Get(ctx context.Context, name string, _ metav1.GetOptions) (*corev1.Secret, error) {
	return asSecret(s.get(ctx, name))
}

func (s *secrets) List(ctx context.Context, opts metav1.ListOptions) (*corev1.SecretList, error) {
	list, err := s.list(ctx, opts)
	if err != nil {
		return nil, err
	}
	out := &corev1.SecretList{Items: []corev1.Secret{}}
	out.Continue = list.GetContinue()
	for i := range list.Items {
		secret, err := asSecret(&list.Items[i], nil)
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, *secret)
	}
	return out, nil
}

func (s *secrets) Create(ctx context.Context, secret *corev1.Secret, opts metav1.CreateOptions) (*corev1.Secret, error) {
	if len(opts.DryRun) != 0 {
		return nil, apierrors.NewBadRequest("management dry run is unsupported")
	}
	obj, err := secretObject(secret)
	if err != nil {
		return nil, err
	}
	return asSecret(s.save(ctx, obj, true))
}

func (s *secrets) Update(ctx context.Context, secret *corev1.Secret, opts metav1.UpdateOptions) (*corev1.Secret, error) {
	if len(opts.DryRun) != 0 {
		return nil, apierrors.NewBadRequest("management dry run is unsupported")
	}
	obj, err := secretObject(secret)
	if err != nil {
		return nil, err
	}
	return asSecret(s.save(ctx, obj, false))
}

func (s *secrets) Delete(ctx context.Context, name string, opts metav1.DeleteOptions) error {
	if len(opts.DryRun) != 0 {
		return apierrors.NewBadRequest("management dry run is unsupported")
	}
	return s.delete(ctx, name, opts)
}

func (s *clusters) Get(ctx context.Context, name string, _ metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if len(subresources) != 0 {
		return nil, apierrors.NewBadRequest("management subresources are unsupported")
	}
	return s.get(ctx, name)
}

func (s *clusters) List(ctx context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return s.list(ctx, opts)
}

func (s *clusters) Create(ctx context.Context, obj *unstructured.Unstructured, opts metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if len(subresources) != 0 || len(opts.DryRun) != 0 {
		return nil, apierrors.NewBadRequest("management subresources and dry run are unsupported")
	}
	return s.save(ctx, obj, true)
}

func (s *clusters) Update(ctx context.Context, obj *unstructured.Unstructured, opts metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if len(subresources) != 0 || len(opts.DryRun) != 0 {
		return nil, apierrors.NewBadRequest("management subresources and dry run are unsupported")
	}
	return s.save(ctx, obj, false)
}

func (s *clusters) Delete(ctx context.Context, name string, opts metav1.DeleteOptions, subresources ...string) error {
	if len(subresources) != 0 || len(opts.DryRun) != 0 {
		return apierrors.NewBadRequest("management subresources and dry run are unsupported")
	}
	return s.delete(ctx, name, opts)
}
