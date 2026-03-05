// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package spegel

import (
	"context"
	"fmt"

	extensionscontroller "github.com/gardener/gardener/extensions/pkg/controller"
	"github.com/gardener/gardener/extensions/pkg/controller/extension"
	extensionssecretsmanager "github.com/gardener/gardener/extensions/pkg/util/secret/manager"
	v1beta1helper "github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/component"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gardener/gardener-extension-registry-cache/imagevector"
	api "github.com/gardener/gardener-extension-registry-cache/pkg/apis/spegel"
	"github.com/gardener/gardener-extension-registry-cache/pkg/apis/spegel/v1alpha1"
	"github.com/gardener/gardener-extension-registry-cache/pkg/component/spegel"
	"github.com/gardener/gardener-extension-registry-cache/pkg/secrets"
	spegelutils "github.com/gardener/gardener-extension-registry-cache/pkg/utils/spegel"
)

// NewActuator returns an actuator responsible for registry-spegel Extension resources.
func NewActuator(client client.Client, decoder runtime.Decoder) extension.Actuator {
	return &actuator{
		client:  client,
		decoder: decoder,
	}
}

type actuator struct {
	client  client.Client
	decoder runtime.Decoder
}

// Reconcile the Extension resource.
func (a *actuator) Reconcile(ctx context.Context, logger logr.Logger, ex *extensionsv1alpha1.Extension) error {
	namespace := ex.GetNamespace()
	cluster, err := extensionscontroller.GetCluster(ctx, a.client, namespace)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	if v1beta1helper.HibernationIsEnabled(cluster.Shoot) {
		return nil
	}

	if ex.Spec.ProviderConfig == nil {
		return fmt.Errorf("providerConfig is required for the registry-spegel extension")
	}
	spegelConfig := &api.SpegelConfig{}
	if err := runtime.DecodeInto(a.decoder, ex.Spec.ProviderConfig.Raw, spegelConfig); err != nil {
		return fmt.Errorf("failed to decode provider config: %w", err)
	}

	image, err := imagevector.ImageVector().FindImage("spegel-peers")
	if err != nil {
		return fmt.Errorf("failed to find the registry image: %w", err)
	}

	ingress := spegelutils.ComputeIngressHost(cluster.Shoot.Status.TechnicalID, cluster.Seed.Spec.Ingress.Domain)

	secretConfigs := secrets.ConfigsForSpegel(ingress, namespace)
	secretsManager, err := extensionssecretsmanager.SecretsManagerForCluster(ctx, logger.WithName("secretsmanager"), clock.RealClock{}, a.client, cluster, secrets.ManagerIdentity, secretConfigs)
	if err != nil {
		return err
	}

	spegelCache := spegel.New(a.client, namespace, secretsManager, spegel.Values{
		Image:       image.String(),
		Domain:      ingress,
		MetricsPort: *spegelConfig.MetricsPort,
	})
	if err = spegelCache.Deploy(ctx); err != nil {
		return fmt.Errorf("failed to deploy the spegel cache component: %w", err)
	}

	if err := spegelCache.Wait(ctx); err != nil {
		return fmt.Errorf("failed to wait the spegel cache component to be healthy: %w", err)
	}

	spegelStatus := computeProviderStatus(spegelCache.CASecretName(), spegelCache.ClientTLSSecretName())

	if err = a.updateProviderStatus(ctx, ex, spegelStatus); err != nil {
		return fmt.Errorf("failed to update Extension status: %w", err)
	}

	if err = secretsManager.Cleanup(ctx); err != nil {
		return fmt.Errorf("failed to cleanup secrets: %w", err)
	}

	return nil
}

func (a *actuator) Delete(ctx context.Context, logger logr.Logger, ex *extensionsv1alpha1.Extension) error {
	namespace := ex.GetNamespace()

	cluster, err := extensionscontroller.GetCluster(ctx, a.client, namespace)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}
	secretsManager, err := extensionssecretsmanager.SecretsManagerForCluster(ctx, logger.WithName("secretsmanager"), clock.RealClock{}, a.client, cluster, secrets.ManagerIdentity, nil)
	if err != nil {
		return err
	}

	spegelCache := spegel.New(a.client, namespace, secretsManager, spegel.Values{})
	if err := component.OpDestroyAndWait(spegelCache).Destroy(ctx); err != nil {
		return fmt.Errorf("failed to destroy the spegel cache component: %w", err)
	}

	return secretsManager.Cleanup(ctx)
}

func (a *actuator) ForceDelete(ctx context.Context, logger logr.Logger, ex *extensionsv1alpha1.Extension) error {
	namespace := ex.GetNamespace()

	cluster, err := extensionscontroller.GetCluster(ctx, a.client, namespace)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}
	secretsManager, err := extensionssecretsmanager.SecretsManagerForCluster(ctx, logger.WithName("secretsmanager"), clock.RealClock{}, a.client, cluster, secrets.ManagerIdentity, nil)
	if err != nil {
		return err
	}

	spegelCache := spegel.New(a.client, namespace, secretsManager, spegel.Values{})
	if err := spegelCache.Destroy(ctx); err != nil {
		return fmt.Errorf("failed to destroy the spegel cache component: %w", err)
	}
	return secretsManager.Cleanup(ctx)
}

func (a *actuator) Restore(ctx context.Context, logger logr.Logger, ex *extensionsv1alpha1.Extension) error {
	return a.Reconcile(ctx, logger, ex)
}

func (a *actuator) Migrate(ctx context.Context, _ logr.Logger, ex *extensionsv1alpha1.Extension) error {
	namespace := ex.GetNamespace()
	spegelCache := spegel.New(a.client, namespace, nil, spegel.Values{
		KeepObjectsOnDestroy: true,
	})
	if err := component.OpDestroyAndWait(spegelCache).Destroy(ctx); err != nil {
		return fmt.Errorf("failed to destroy the spegel cache component: %w", err)
	}
	return nil
}

func computeProviderStatus(caSecretName, clientTLSSecretName string) *v1alpha1.SpegelStatus {
	return &v1alpha1.SpegelStatus{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha1.SchemeGroupVersion.String(),
			Kind:       "SpegelStatus",
		},
		CASecretName:        caSecretName,
		ClientTLSSecretName: clientTLSSecretName,
	}
}

func (a *actuator) updateProviderStatus(ctx context.Context, ex *extensionsv1alpha1.Extension, spegelStatus *v1alpha1.SpegelStatus) error {
	patch := client.MergeFrom(ex.DeepCopy())
	ex.Status.ProviderStatus = &runtime.RawExtension{Object: spegelStatus}
	return a.client.Status().Patch(ctx, ex, patch)
}
