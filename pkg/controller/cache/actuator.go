// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package extension

import (
	"context"
	"fmt"
	"github.com/gardener/gardener-extension-registry-cache/pkg/secrets"
	registryutils "github.com/gardener/gardener-extension-registry-cache/pkg/utils/registry"
	"github.com/gardener/gardener/pkg/utils"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strings"

	extensionsconfig "github.com/gardener/gardener/extensions/pkg/apis/config"
	extensionscontroller "github.com/gardener/gardener/extensions/pkg/controller"
	"github.com/gardener/gardener/extensions/pkg/controller/extension"
	"github.com/gardener/gardener/extensions/pkg/util"
	extensionssecretsmanager "github.com/gardener/gardener/extensions/pkg/util/secret/manager"
	v1beta1helper "github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/component"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/utils/clock"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gardener/gardener-extension-registry-cache/imagevector"
	"github.com/gardener/gardener-extension-registry-cache/pkg/apis/config"
	api "github.com/gardener/gardener-extension-registry-cache/pkg/apis/registry"
	"github.com/gardener/gardener-extension-registry-cache/pkg/apis/registry/v1alpha3"
	"github.com/gardener/gardener-extension-registry-cache/pkg/component/registrycaches"
	"github.com/gardener/gardener-extension-registry-cache/pkg/component/registryconfigurationcleaner"
	"github.com/gardener/gardener-extension-registry-cache/pkg/constants"
)

// NewActuator returns an actuator responsible for registry-cache Extension resources.
func NewActuator(client client.Client, decoder runtime.Decoder, config config.Configuration) extension.Actuator {
	return &actuator{
		client:  client,
		decoder: decoder,
		config:  config,
	}
}

type actuator struct {
	client  client.Client
	decoder runtime.Decoder
	config  config.Configuration
}

// Reconcile the Extension resource.
func (a *actuator) Reconcile(ctx context.Context, logger logr.Logger, ex *extensionsv1alpha1.Extension) error {
	if ex.Spec.ProviderConfig == nil {
		return fmt.Errorf("providerConfig is required for the registry-cache extension")
	}

	registryConfig := &api.RegistryConfig{}
	if _, _, err := a.decoder.Decode(ex.Spec.ProviderConfig.Raw, nil, registryConfig); err != nil {
		return fmt.Errorf("failed to decode provider config: %w", err)
	}

	namespace := ex.GetNamespace()
	cluster, err := extensionscontroller.GetCluster(ctx, a.client, namespace)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	if v1beta1helper.HibernationIsEnabled(cluster.Shoot) {
		return nil
	}

	// create registry cache services
	_, shootClient, err := util.NewClientForShoot(ctx, a.client, namespace, client.Options{}, extensionsconfig.RESTOptions{})
	if err != nil {
		return fmt.Errorf("failed to create shoot client: %w", err)
	}
	if err = createServices(ctx, logger, shootClient, registryConfig.Caches); err != nil {
		return fmt.Errorf("failed to create services: %w", err)
	}

	// Clean registry configuration if a registry cache is removed.
	upstreamsToDelete := sets.New[string]()
	if ex.Status.ProviderStatus != nil {
		registryStatus := &api.RegistryStatus{}
		if _, _, err := a.decoder.Decode(ex.Status.ProviderStatus.Raw, nil, registryStatus); err != nil {
			return fmt.Errorf("failed to decode providerStatus of extension '%s': %w", client.ObjectKeyFromObject(ex), err)
		}

		existingUpstreams := sets.New[string]()
		for _, cache := range registryStatus.Caches {
			existingUpstreams.Insert(cache.Upstream)
		}

		desiredUpstreams := sets.New[string]()
		for _, cache := range registryConfig.Caches {
			desiredUpstreams.Insert(cache.Upstream)
		}

		upstreamsToDelete = existingUpstreams.Difference(desiredUpstreams)
		if upstreamsToDelete.Len() > 0 {
			if err = cleanRegistryConfiguration(ctx, cluster, a.client, ex.GetNamespace(), false, upstreamsToDelete.UnsortedList()); err != nil {
				return err
			}
		}
	}

	registryStatus, err := a.computeProviderStatus(ctx, registryConfig, namespace)
	if err != nil {
		return fmt.Errorf("failed to compute provider status: %w", err)
	}

	// initialize SecretsManager based on Cluster object
	secretsConfig := secrets.ConfigsFor(registryStatus.Caches)
	secretsManager, err := extensionssecretsmanager.SecretsManagerForCluster(ctx, logger.WithName("secretsmanager"), clock.RealClock{}, a.client, cluster, secrets.ManagerIdentity, secretsConfig)
	if err != nil {
		return err
	}

	generatedSecrets, err := extensionssecretsmanager.GenerateAllSecrets(ctx, secretsManager, secretsConfig)
	if err != nil {
		return err
	}

	_, found := secretsManager.Get(secrets.CAName)
	if !found {
		return fmt.Errorf("secret %q not found", secrets.CAName)
	}

	image, err := imagevector.ImageVector().FindImage("registry")
	if err != nil {
		return fmt.Errorf("failed to find the registry image: %w", err)
	}

	mappedSecrets := make(map[string]*corev1.Secret, len(generatedSecrets))
	// remap secrets
	for _, secret := range generatedSecrets {
		mappedSecrets[strings.TrimSuffix(secret.Labels["name"], "-tls")] = secret
	}

	registryCaches := registrycaches.New(a.client, logger, namespace, registrycaches.Values{
		Image:              image.String(),
		VPAEnabled:         v1beta1helper.ShootWantsVerticalPodAutoscaler(cluster.Shoot),
		Caches:             registryConfig.Caches,
		ResourceReferences: cluster.Shoot.Spec.Resources,
		TLSSecrets:         mappedSecrets,
	})

	if err = registryCaches.Deploy(ctx); err != nil {
		return fmt.Errorf("failed to deploy the registry caches component: %w", err)
	}

	if err = deleteServices(ctx, shootClient, upstreamsToDelete.UnsortedList()); err != nil {
		return fmt.Errorf("failed to delete services: %w", err)
	}

	if err = a.updateProviderStatus(ctx, ex, registryStatus); err != nil {
		return fmt.Errorf("failed to update Extension status: %w", err)
	}

	if err = secretsManager.Cleanup(ctx); err != nil {
		return fmt.Errorf("failed to cleanup secrets: %w", err)
	}

	return nil
}

// Delete the Extension resource.
func (a *actuator) Delete(ctx context.Context, logger logr.Logger, ex *extensionsv1alpha1.Extension) error {
	namespace := ex.GetNamespace()

	cluster, err := extensionscontroller.GetCluster(ctx, a.client, namespace)
	if err != nil {
		return fmt.Errorf("failed to get cluster: %w", err)
	}

	// If the Shoot is in deletion, then there is no need to clean up the registry configuration from Nodes.
	// The Shoot deletion flows ensures that the Worker is deleted before the Extension deletion.
	// Hence, there are no Nodes, no need to clean up registry configuration.
	if ex.Status.ProviderStatus != nil && cluster.Shoot.DeletionTimestamp == nil {
		registryStatus := &api.RegistryStatus{}
		if _, _, err := a.decoder.Decode(ex.Status.ProviderStatus.Raw, nil, registryStatus); err != nil {
			return fmt.Errorf("failed to decode providerStatus of extension '%s': %w", client.ObjectKeyFromObject(ex), err)
		}

		upstreams := make([]string, 0, len(registryStatus.Caches))
		for _, cache := range registryStatus.Caches {
			upstreams = append(upstreams, cache.Upstream)
		}

		if err := cleanRegistryConfiguration(ctx, cluster, a.client, ex.GetNamespace(), true, upstreams); err != nil {
			return err
		}

		if !v1beta1helper.HibernationIsEnabled(cluster.Shoot) {
			_, shootClient, err := util.NewClientForShoot(ctx, a.client, namespace, client.Options{}, extensionsconfig.RESTOptions{})
			if err != nil {
				return fmt.Errorf("failed to create shoot client: %w", err)
			}
			if err = deleteServices(ctx, shootClient, upstreams); err != nil {
				return fmt.Errorf("failed to delete services: %w", err)
			}
		}
	}

	// If the Shoot is in deletion, destroy the cleaner component (delete the cleaner ManagedResource)
	// as the cleaner ManagedResource could still exist (deployed in a previous reconciliation but failed to be cleaned up)
	// and could block the Shoot deletion afterward.
	if cluster.Shoot.DeletionTimestamp != nil {
		cleaner := registryconfigurationcleaner.New(a.client, namespace, registryconfigurationcleaner.Values{})
		if err := component.OpDestroyAndWait(cleaner).Destroy(ctx); err != nil {
			return fmt.Errorf("failed to destroy the registry configuration cleaner component: %w", err)
		}
	}

	registryCaches := registrycaches.New(a.client, logger, namespace, registrycaches.Values{})
	if err := component.OpDestroyAndWait(registryCaches).Destroy(ctx); err != nil {
		return fmt.Errorf("failed to destroy the registry caches component: %w", err)
	}

	secretsManager, err := extensionssecretsmanager.SecretsManagerForCluster(ctx, logger.WithName("secretsmanager"), clock.RealClock{}, a.client, cluster, secrets.ManagerIdentity, nil)
	if err != nil {
		return err
	}

	return secretsManager.Cleanup(ctx)
}

// Restore the Extension resource.
func (a *actuator) Restore(ctx context.Context, logger logr.Logger, ex *extensionsv1alpha1.Extension) error {
	return a.Reconcile(ctx, logger, ex)
}

// Migrate the Extension resource.
func (a *actuator) Migrate(ctx context.Context, logger logr.Logger, ex *extensionsv1alpha1.Extension) error {
	namespace := ex.GetNamespace()

	registryCaches := registrycaches.New(a.client, logger, namespace, registrycaches.Values{
		KeepObjectsOnDestroy: true,
	})
	if err := component.OpDestroyAndWait(registryCaches).Destroy(ctx); err != nil {
		return fmt.Errorf("failed to destroy the registry caches component: %w", err)
	}

	return nil
}

// ForceDelete the Extension resource.
//
// We don't need to wait for the ManagedResource deletion because ManagedResources are finalized by gardenlet
// in later step in the Shoot force deletion flow.
func (a *actuator) ForceDelete(ctx context.Context, logger logr.Logger, ex *extensionsv1alpha1.Extension) error {
	namespace := ex.GetNamespace()

	registryCaches := registrycaches.New(a.client, logger, namespace, registrycaches.Values{})
	if err := component.OpDestroy(registryCaches).Destroy(ctx); err != nil {
		return fmt.Errorf("failed to destroy the registry caches component: %w", err)
	}

	return nil
}

func (a *actuator) computeProviderStatus(ctx context.Context, registryConfig *api.RegistryConfig, namespace string) (*v1alpha3.RegistryStatus, error) {
	// get service IPs from shoot
	_, shootClient, err := util.NewClientForShoot(ctx, a.client, namespace, client.Options{}, extensionsconfig.RESTOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to create shoot client: %w", err)
	}

	selector := labels.NewSelector()
	r, err := labels.NewRequirement(constants.UpstreamHostLabel, selection.Exists, nil)
	if err != nil {
		return nil, err
	}
	selector = selector.Add(*r)

	// get all registry cache services
	services := &corev1.ServiceList{}
	if err := shootClient.List(ctx, services, client.InNamespace(metav1.NamespaceSystem), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		return nil, fmt.Errorf("failed to read services from shoot: %w", err)
	}

	if len(services.Items) != len(registryConfig.Caches) {
		return nil, fmt.Errorf("not all services for all configured caches exist")
	}

	caches := make([]v1alpha3.RegistryCacheStatus, 0, len(services.Items))
	for _, service := range services.Items {
		caches = append(caches, v1alpha3.RegistryCacheStatus{
			Upstream:  service.Annotations[constants.UpstreamAnnotation],
			Endpoint:  fmt.Sprintf("https://%s:%d", service.Spec.ClusterIP, constants.RegistryCachePort),
			RemoteURL: service.Annotations[constants.RemoteURLAnnotation],
			ClusterIP: service.Spec.ClusterIP,
		})
	}

	return &v1alpha3.RegistryStatus{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1alpha3.SchemeGroupVersion.String(),
			Kind:       "RegistryStatus",
		},
		Caches: caches,
	}, nil
}

func (a *actuator) updateProviderStatus(ctx context.Context, ex *extensionsv1alpha1.Extension, registryStatus *v1alpha3.RegistryStatus) error {
	patch := client.MergeFrom(ex.DeepCopy())
	ex.Status.ProviderStatus = &runtime.RawExtension{Object: registryStatus}
	return a.client.Status().Patch(ctx, ex, patch)
}

func cleanRegistryConfiguration(ctx context.Context, cluster *extensionscontroller.Cluster, client client.Client, namespace string, deleteSystemdUnit bool, upstreams []string) error {
	// If the Shoot is hibernated, we don't have Nodes. Hence, there is no need to clean up anything.
	if extensionscontroller.IsHibernated(cluster) {
		return nil
	}

	alpineImage, err := imagevector.ImageVector().FindImage("alpine")
	if err != nil {
		return fmt.Errorf("failed to find the alpine image: %w", err)
	}
	pauseImage, err := imagevector.ImageVector().FindImage("pause")
	if err != nil {
		return fmt.Errorf("failed to find the pause image: %w", err)
	}

	values := registryconfigurationcleaner.Values{
		AlpineImage:       alpineImage.String(),
		PauseImage:        pauseImage.String(),
		DeleteSystemdUnit: deleteSystemdUnit,
		Upstreams:         upstreams,
	}
	cleaner := registryconfigurationcleaner.New(client, namespace, values)

	if err := component.OpWait(cleaner).Deploy(ctx); err != nil {
		return fmt.Errorf("failed to deploy the registry configuration cleaner component: %w", err)
	}

	if err := component.OpDestroyAndWait(cleaner).Destroy(ctx); err != nil {
		return fmt.Errorf("failed to destroy the registry configuration cleaner component: %w", err)
	}

	return nil
}

// computeUpstreamLabelValue computes upstream-host label value by given upstream.
//
// Upstream is a valid DNS subdomain (RFC 1123) and optionally a port (e.g. my-registry.io[:5000])
// It is used as a 'upstream-host' label value on registry cache resources (Service, Secret, StatefulSet and VPA).
// Label values cannot contain ':' char, so if upstream is '<host>:<port>' the label value is transformed to '<host>-<port>'.
// It is also used to build the resources names escaping the '.' with '-'; e.g. `registry-<escaped_upstreamLabel>`.
//
// Due to restrictions of resource names length, if upstream length > 43 it is truncated at 37 chars, and the
// label value is transformed to <truncated-upstream>-<hash> where <hash> is first 5 chars of upstream sha256 hash.
//
// The returned upstreamLabel is at most 43 chars.
func computeUpstreamLabelValue(upstream string) string {
	// A label value length and a resource name length limits are 63 chars. However, Pods for a StatefulSet with name > 52 chars
	// cannot be created due to https://github.com/kubernetes/kubernetes/issues/64023.
	// The cache resources name have prefix 'registry-', thus the label value length is limited to 43.
	const labelValueLimit = 43

	upstreamLabel := strings.ReplaceAll(upstream, ":", "-")
	if len(upstream) > labelValueLimit {
		hash := utils.ComputeSHA256Hex([]byte(upstream))[:5]
		limit := labelValueLimit - len(hash) - 1
		upstreamLabel = fmt.Sprintf("%s-%s", upstreamLabel[:limit], hash)
	}
	return upstreamLabel
}

func getLabels(name, upstreamLabel string) map[string]string {
	return map[string]string{
		"app":                       name,
		constants.UpstreamHostLabel: upstreamLabel,
	}
}

func createServices(ctx context.Context, logger logr.Logger, shootClient client.Client, caches []api.RegistryCache) error {
	for _, cache := range caches {
		upstreamLabel := computeUpstreamLabelValue(cache.Upstream)
		name := "registry-" + strings.ReplaceAll(upstreamLabel, ".", "-")
		remoteURL := ptr.Deref(cache.RemoteURL, registryutils.GetUpstreamURL(cache.Upstream))
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: metav1.NamespaceSystem,
			},
		}
		op, err := controllerutil.CreateOrUpdate(ctx, shootClient, service, func() error {
			serviceLabels := getLabels(name, upstreamLabel)
			service.Annotations = map[string]string{
				constants.UpstreamAnnotation:  cache.Upstream,
				constants.RemoteURLAnnotation: remoteURL,
			}
			service.Labels = serviceLabels
			service.Spec.Selector = serviceLabels
			service.Spec.Type = corev1.ServiceTypeClusterIP
			service.Spec.Ports = []corev1.ServicePort{{
				Name:       "registry-cache",
				Port:       constants.RegistryCachePort,
				Protocol:   corev1.ProtocolTCP,
				TargetPort: intstr.FromString("registry-cache"),
			}}
			return nil
		})
		if err != nil {
			return err
		}
		logger.Info("Service successfully reconciled", "operation", op, "upstream", cache.Upstream)
	}
	return nil
}

func deleteServices(ctx context.Context, shootClient client.Client, upstreamsToDelete []string) error {
	for _, cache := range upstreamsToDelete {
		upstreamLabel := computeUpstreamLabelValue(cache)
		name := "registry-" + strings.ReplaceAll(upstreamLabel, ".", "-")
		if err := shootClient.Delete(ctx, &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: metav1.NamespaceSystem,
			},
		}); err != nil {
			return err
		}
	}
	return nil
}
