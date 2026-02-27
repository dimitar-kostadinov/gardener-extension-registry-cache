// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package spegel

import (
	"context"
	"fmt"
	"strconv"
	"time"

	extensionssecretsmanager "github.com/gardener/gardener/extensions/pkg/util/secret/manager"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	resourcesv1alpha1 "github.com/gardener/gardener/pkg/apis/resources/v1alpha1"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/component"
	kubeapiserverconstants "github.com/gardener/gardener/pkg/component/kubernetes/apiserver/constants"
	"github.com/gardener/gardener/pkg/utils"
	gardenerutils "github.com/gardener/gardener/pkg/utils/gardener"
	kubernetesutils "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/gardener/gardener/pkg/utils/managedresources"
	secretsutils "github.com/gardener/gardener/pkg/utils/secrets"
	secretsmanager "github.com/gardener/gardener/pkg/utils/secrets/manager"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gardener/gardener-extension-registry-cache/pkg/apis/spegel/v1alpha1"
	"github.com/gardener/gardener-extension-registry-cache/pkg/secrets"
)

const (
	managedResourceShootName = "extension-registry-cache-spegel-shoot"
	managedResourceSeedName  = "extension-registry-cache-spegel-seed"
	spegelClusterRoleName    = "gardener.cloud:registry:spegel"
	spegelName               = "gardener-spegel"

	shootAccessSecretName = "shoot-access-spegel"
)

// Values is a set of configuration values for the spegel cache.
type Values struct {
	// KeepObjectsOnDestroy marks whether the ManagedResource's .spec.keepObjects will be set to true
	// before ManagedResource deletion during the Destroy operation. When set to true, the deployed
	// resources by ManagedResources won't be deleted, but the ManagedResource itself will be deleted.
	KeepObjectsOnDestroy bool
	// Image is the container image used for the spegel peers.
	Image string
	//Domain is the speggel peers ingress domain
	Domain string
	//Config is the Spegel API config
	Config *v1alpha1.SpegelConfig
}

// Interface is an interface for managing Registry Caches.
type Interface interface {
	component.DeployWaiter
	// CASecretName returns the name of the CA secret.
	CASecretName() string
	// ClientTLSSecretName returns the name of the client TLS secret.
	ClientTLSSecretName() string
}

// New creates a new instance of component.DeployWaiter for registry cache services.
func New(
	client client.Client,
	namespace string,
	secretsManager secretsmanager.Interface,
	values Values,
) Interface {
	return &spegelCache{
		client:         client,
		namespace:      namespace,
		secretsManager: secretsManager,
		values:         values,
	}
}

type spegelCache struct {
	client         client.Client
	namespace      string
	secretsManager secretsmanager.Interface
	values         Values

	// secrets used by the spegel external bootstrapper.
	caSecretName        string
	clientTLSSecretName string
}

func (s *spegelCache) Deploy(ctx context.Context) error {
	configs := secrets.ConfigsForSpegel(s.namespace, s.values.Domain)
	generatedSecrets, err := extensionssecretsmanager.GenerateAllSecrets(ctx, s.secretsManager, configs)
	if err != nil {
		return err
	}

	clusterCABundle, err := getLatestIssuedCABundleSecret(ctx, s.client, s.namespace)
	if err != nil {
		return err
	}

	caBundle, found := s.secretsManager.Get(secrets.CAName)
	if !found {
		return fmt.Errorf("secret %q not found", secrets.CAName)
	}
	s.caSecretName = caBundle.Name
	s.clientTLSSecretName = generatedSecrets[secrets.SpegelPeersClientTLSSecretName].Name

	spegelShootAccessSecret := s.newSpegelShootAccessSecret()
	if err := spegelShootAccessSecret.Reconcile(ctx, s.client); err != nil {
		return err
	}

	shootData, err := s.computeResourcesShootData(spegelShootAccessSecret.ServiceAccountName)
	if err != nil {
		return err
	}
	seedData, err := s.computeResourcesSeedData(generatedSecrets[secrets.SpegelPeersTLSSecretName].Name, caBundle.Name, clusterCABundle.Name)
	if err != nil {
		return err
	}

	if err := managedresources.CreateForShoot(ctx, s.client, s.namespace, managedResourceShootName, "spegel-cache", false, shootData); err != nil {
		return fmt.Errorf("failed to create ManagedResource for Shoot: %w", err)
	}
	if err := managedresources.CreateForSeed(ctx, s.client, s.namespace, managedResourceSeedName, false, seedData); err != nil {
		return fmt.Errorf("failed to create ManagedResource for Shoot: %w", err)
	}

	if err := deployMonitoringScrapeConfig(ctx, s.client, s.namespace); err != nil {
		return fmt.Errorf("failed to deploy monitoring config: %w", err)
	}

	return nil
}

func (s *spegelCache) Destroy(ctx context.Context) error {
	if s.values.KeepObjectsOnDestroy {
		if err := managedresources.SetKeepObjects(ctx, s.client, s.namespace, managedResourceSeedName, true); err != nil {
			return err
		}
		if err := managedresources.SetKeepObjects(ctx, s.client, s.namespace, managedResourceShootName, true); err != nil {
			return err
		}
	}

	if err := managedresources.Delete(ctx, s.client, s.namespace, managedResourceSeedName, false); err != nil {
		return err
	}

	if err := managedresources.Delete(ctx, s.client, s.namespace, managedResourceShootName, false); err != nil {
		return err
	}

	if err := kubernetesutils.DeleteObject(ctx, s.client, s.newSpegelShootAccessSecret().Secret); err != nil {
		return err
	}

	if err := destroyMonitoringScrapeConfig(ctx, s.client, s.namespace); err != nil {
		return fmt.Errorf("failed to destroy monitoring config: %w", err)
	}

	return nil
}

// TimeoutWaitForManagedResource is the timeout used while waiting for the ManagedResources to become healthy
// or deleted.
var TimeoutWaitForManagedResource = 2 * time.Minute

func (s *spegelCache) Wait(ctx context.Context) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, TimeoutWaitForManagedResource)
	defer cancel()

	if err := managedresources.WaitUntilHealthy(timeoutCtx, s.client, s.namespace, managedResourceSeedName); err != nil {
		return err
	}

	return managedresources.WaitUntilHealthy(timeoutCtx, s.client, s.namespace, managedResourceShootName)
}

func (s *spegelCache) WaitCleanup(ctx context.Context) error {
	timeoutCtx, cancel := context.WithTimeout(ctx, TimeoutWaitForManagedResource)
	defer cancel()

	if err := managedresources.WaitUntilHealthy(timeoutCtx, s.client, s.namespace, managedResourceSeedName); err != nil {
		return err
	}

	return managedresources.WaitUntilDeleted(timeoutCtx, s.client, s.namespace, managedResourceShootName)
}

func (s *spegelCache) computeResourcesShootData(spegelServiceAccountName string) (map[string][]byte, error) {
	registry := managedresources.NewRegistry(kubernetes.ShootScheme, kubernetes.ShootCodec, kubernetes.ShootSerializer)

	return registry.AddAllAndSerialize(
		s.getSpegelClusterRole(),
		s.getSpegelClusterRoleBinding(spegelServiceAccountName),
		s.getSpegelDaemonSet(),
		s.getSpegelService(),
	)
}

func (s *spegelCache) computeResourcesSeedData(serverTlsSecretName, caBundleSecretName, clusterCAName string) (map[string][]byte, error) {
	registry := managedresources.NewRegistry(kubernetes.SeedScheme, kubernetes.SeedCodec, kubernetes.SeedSerializer)

	return registry.AddAllAndSerialize(
		s.getSpegelPeersDeployment(serverTlsSecretName, caBundleSecretName, clusterCAName),
		s.getSpegelPeersService(),
		s.getSpegelPeersIngress(),
	)
}

func (s *spegelCache) newSpegelShootAccessSecret() *gardenerutils.AccessSecret {
	return gardenerutils.NewShootAccessSecret("spegel", s.namespace).
		WithServiceAccountName(spegelName).
		WithTokenExpirationDuration("720h")
}

func (s *spegelCache) getSpegelClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   spegelClusterRoleName,
			Labels: map[string]string{v1beta1constants.LabelApp: spegelName},
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{
					"nodes",
				},
				Verbs: []string{
					"get",
					"list",
					"watch",
				},
			},
		},
	}
}

func (s *spegelCache) getSpegelClusterRoleBinding(serviceAccountName string) *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   spegelClusterRoleName,
			Labels: map[string]string{v1beta1constants.LabelApp: spegelName},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     spegelClusterRoleName,
		},
		Subjects: []rbacv1.Subject{{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      serviceAccountName,
			Namespace: metav1.NamespaceSystem,
		}},
	}
}

func (s *spegelCache) getSpegelService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spegel-bootstrap",
			Namespace: metav1.NamespaceSystem,
			Labels:    getShootLabels(),
		},
		Spec: corev1.ServiceSpec{
			Selector: getShootLabels(),
			Ports: []corev1.ServicePort{
				{
					Name:       "router",
					Port:       *s.values.Config.RouterPort,
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromInt32(*s.values.Config.RouterPort),
				},
			},
			Type:                     corev1.ServiceTypeClusterIP,
			ClusterIP:                "None",
			ClusterIPs:               []string{"None"},
			InternalTrafficPolicy:    ptr.To(corev1.ServiceInternalTrafficPolicyCluster),
			PublishNotReadyAddresses: true,
			SessionAffinity:          corev1.ServiceAffinityNone,
		},
	}
}

func (s *spegelCache) getSpegelDaemonSet() *appsv1.DaemonSet {
	spegelDaemonSet := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spegel",
			Namespace: metav1.NamespaceSystem,
			// Labels:    getShootLabels(),
			Labels: utils.MergeStringMaps(getShootLabels(), map[string]string{
				v1beta1constants.LabelNodeCriticalComponent: "true",
			}),
		},
		Spec: appsv1.DaemonSetSpec{
			RevisionHistoryLimit: ptr.To[int32](2),
			Selector: &metav1.LabelSelector{
				MatchLabels: getShootLabels(),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: utils.MergeStringMaps(getShootLabels(), map[string]string{
						v1beta1constants.LabelNodeCriticalComponent:         "true",
						v1beta1constants.LabelNetworkPolicyToDNS:            v1beta1constants.LabelNetworkPolicyAllowed,
						v1beta1constants.LabelNetworkPolicyShootToAPIServer: v1beta1constants.LabelNetworkPolicyAllowed,
					}),
					Annotations: map[string]string{
						"prometheus.io/port":   strconv.Itoa(9090),
						"prometheus.io/scrape": strconv.FormatBool(true),
					},
				},
				Spec: corev1.PodSpec{
					PriorityClassName:  "system-node-critical",
					ServiceAccountName: "gardener-spegel",
					HostNetwork:        true,
					DNSPolicy:          corev1.DNSClusterFirstWithHostNet,
					SecurityContext: &corev1.PodSecurityContext{
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Tolerations: []corev1.Toleration{
						{
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoExecute,
						},
						{
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "spegel",
							Image: "ghcr.io/spegel-org/spegel:v0.6.0",

							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("48Mi"),
								},
								Limits: corev1.ResourceList{
									corev1.ResourceMemory: resource.MustParse("128Mi"),
								},
							},
							SecurityContext: &corev1.SecurityContext{
								ReadOnlyRootFilesystem:   ptr.To(true),
								AllowPrivilegeEscalation: ptr.To(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{
										"ALL",
									},
								},
							},
							Args: []string{
								"registry",
								"--log-level=DEBUG",
								"--mirror-resolve-retries=3",
								"--mirror-resolve-timeout=20ms",
								fmt.Sprintf("--registry-addr=:%d", *s.values.Config.RegistryPort),
								fmt.Sprintf("--router-addr=:%d", *s.values.Config.RouterPort),
								fmt.Sprintf("--metrics-addr=:%d", *s.values.Config.MetricsPort),
								"--containerd-sock=/run/containerd/containerd.sock",
								"--containerd-namespace=k8s.io",
								"--containerd-registry-config-path=/etc/containerd/certs.d",
								"--bootstrap-kind=dns",
								"--dns-bootstrap-domain=spegel-bootstrap.kube-system.svc.cluster.local.",
								"--containerd-content-path=/var/lib/containerd/io.containerd.content.v1.content",
								"--debug-web-enabled=true",
							},
							Env: []corev1.EnvVar{
								{
									Name: "DATA_DIR",
								},
								{
									Name: "GOMEMLIMIT",
									ValueFrom: &corev1.EnvVarSource{
										ResourceFieldRef: &corev1.ResourceFieldSelector{Resource: "limits.memory"},
									},
								},
								{
									Name: "NODE_IP",
									ValueFrom: &corev1.EnvVarSource{
										FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.hostIP"},
									},
								},
							},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: *s.values.Config.RegistryPort,
									Name:          "registry",
									Protocol:      corev1.ProtocolTCP,
								},
								{
									ContainerPort: *s.values.Config.RouterPort,
									Name:          "router",
									Protocol:      corev1.ProtocolTCP,
								},
								{
									ContainerPort: *s.values.Config.MetricsPort,
									Name:          "metrics",
									Protocol:      corev1.ProtocolTCP,
								},
							},
							// ReadinessProbe: &corev1.Probe{
							// 	ProbeHandler: corev1.ProbeHandler{
							// 		HTTPGet: &corev1.HTTPGetAction{
							// 			Host: "localhost",
							// 			Path: "/readyz",
							// 			Port: intstr.FromInt32(*s.values.Config.RegistryPort),
							// 		},
							// 	},
							// },
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Host: "localhost",
										Path: "/livez",
										Port: intstr.FromInt32(*s.values.Config.RegistryPort),
									},
								},
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									MountPath: "/run/containerd/containerd.sock",
									Name:      "containerd-sock",
								},
								{
									MountPath: "/var/lib/containerd/io.containerd.content.v1.content",
									Name:      "containerd-content",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "containerd-sock",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/run/containerd/containerd.sock",
									Type: ptr.To(corev1.HostPathSocket),
								},
							},
						},
						{
							Name: "containerd-content",
							VolumeSource: corev1.VolumeSource{
								HostPath: &corev1.HostPathVolumeSource{
									Path: "/var/lib/containerd/io.containerd.content.v1.content",
									Type: ptr.To(corev1.HostPathDirectory),
								},
							},
						},
					},
				},
			},
		},
	}
	return spegelDaemonSet
}

func (s *spegelCache) getSpegelPeersDeployment(serverTlsSecretName, caBundleSecretName, clusterCAName string) *appsv1.Deployment {
	spegelDeployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spegelpeers",
			Namespace: s.namespace,
			Labels:    getSeedLabels(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas:             ptr.To[int32](2),
			RevisionHistoryLimit: ptr.To[int32](2),
			Selector: &metav1.LabelSelector{
				MatchLabels: getSeedLabels(),
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: utils.MergeStringMaps(getSeedLabels(), map[string]string{
						v1beta1constants.LabelNetworkPolicyToDNS: v1beta1constants.LabelNetworkPolicyAllowed,
						gardenerutils.NetworkPolicyLabel(v1beta1constants.DeploymentNameKubeAPIServer, kubeapiserverconstants.Port): v1beta1constants.LabelNetworkPolicyAllowed,
					}),
				},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptr.To(false),
					PriorityClassName:            v1beta1constants.PriorityClassNameShootControlPlane100,
					SecurityContext: &corev1.PodSecurityContext{
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "spegel-peers",
							Image:           s.values.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    resource.MustParse("20m"),
									corev1.ResourceMemory: resource.MustParse("50Mi"),
								},
							},
							Ports: []corev1.ContainerPort{
								{
									ContainerPort: 443,
									Name:          "spegel-peers",
								},
								{
									ContainerPort: 8080,
									Name:          "liveness-port",
								},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: ptr.To(false),
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/health",
										Port: intstr.FromString("liveness-port"),
									},
								},
								FailureThreshold: 6,
								SuccessThreshold: 1,
								PeriodSeconds:    20,
							},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "cluster-access",
									MountPath: "/var/run/secrets/cluster-access",
								},
								{
									Name:      "certs",
									MountPath: "/var/run/secrets/certs",
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "cluster-access",
							VolumeSource: corev1.VolumeSource{
								Projected: &corev1.ProjectedVolumeSource{
									DefaultMode: ptr.To[int32](420),
									Sources: []corev1.VolumeProjection{
										{
											Secret: &corev1.SecretProjection{
												LocalObjectReference: corev1.LocalObjectReference{Name: clusterCAName},
												Items: []corev1.KeyToPath{{
													Key:  secretsutils.DataKeyCertificateBundle,
													Path: secretsutils.DataKeyCertificateBundle,
												}},
												Optional: ptr.To(false),
											},
										},
										{
											Secret: &corev1.SecretProjection{
												LocalObjectReference: corev1.LocalObjectReference{Name: shootAccessSecretName},
												Items: []corev1.KeyToPath{{
													Key:  resourcesv1alpha1.DataKeyToken,
													Path: resourcesv1alpha1.DataKeyToken,
												}},
												Optional: ptr.To(false),
											},
										},
									},
								},
							},
						},
						{
							Name: "certs",
							VolumeSource: corev1.VolumeSource{
								Projected: &corev1.ProjectedVolumeSource{
									DefaultMode: ptr.To[int32](420),
									Sources: []corev1.VolumeProjection{
										{
											Secret: &corev1.SecretProjection{
												LocalObjectReference: corev1.LocalObjectReference{Name: serverTlsSecretName},
												Items: []corev1.KeyToPath{
													{
														Key:  secretsutils.DataKeyCertificate,
														Path: secretsutils.DataKeyCertificate,
													},
													{
														Key:  secretsutils.DataKeyPrivateKey,
														Path: secretsutils.DataKeyPrivateKey,
													},
												},
												Optional: ptr.To(false),
											},
										},
										{
											Secret: &corev1.SecretProjection{
												LocalObjectReference: corev1.LocalObjectReference{Name: caBundleSecretName},
												Items: []corev1.KeyToPath{{
													Key:  secretsutils.DataKeyCertificateBundle,
													Path: secretsutils.DataKeyCertificateBundle,
												}},
												Optional: ptr.To(false),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	return spegelDeployment
}

func (s *spegelCache) getSpegelPeersService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spegelpeers",
			Namespace: s.namespace,
			Labels:    getSeedLabels(),
		},
		Spec: corev1.ServiceSpec{
			Selector: getSeedLabels(),
			Ports: []corev1.ServicePort{
				{
					Name:       "spegel-peers",
					Port:       443,
					Protocol:   corev1.ProtocolTCP,
					TargetPort: intstr.FromString("spegel-peers"),
				},
			},
			Type: corev1.ServiceTypeClusterIP,
		},
	}
}

func (s *spegelCache) getSpegelPeersIngress() *networkingv1.Ingress {
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spegelpeers",
			Namespace: s.namespace,
			Labels:    getSeedLabels(),
			Annotations: map[string]string{
				"nginx.ingress.kubernetes.io/backend-protocol": "HTTPS",
				"nginx.ingress.kubernetes.io/ssl-passthrough":  "true",
				"nginx.ingress.kubernetes.io/ssl-redirect":     "true",
			},
		},

		Spec: networkingv1.IngressSpec{
			IngressClassName: ptr.To(v1beta1constants.SeedNginxIngressClass),
			Rules: []networkingv1.IngressRule{{
				Host: s.values.Domain,
				IngressRuleValue: networkingv1.IngressRuleValue{
					HTTP: &networkingv1.HTTPIngressRuleValue{
						Paths: []networkingv1.HTTPIngressPath{{
							Backend: networkingv1.IngressBackend{
								Service: &networkingv1.IngressServiceBackend{
									Name: "spegelpeers",
									Port: networkingv1.ServiceBackendPort{Number: 443},
								},
							},
							Path:     "/",
							PathType: ptr.To(networkingv1.PathTypePrefix),
						}},
					},
				}},
			},
		},
	}
}

func (s *spegelCache) CASecretName() string {
	return s.caSecretName
}

func (s *spegelCache) ClientTLSSecretName() string {
	return s.clientTLSSecretName
}

func getSeedLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":    "spegel-peers",
		"app.kubernetes.io/part-of": "registry-cache",
	}
}

func getShootLabels() map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":    "spegel",
		"app.kubernetes.io/part-of": "registry-cache",
	}
}

// getLatestIssuedCABundleSecret returns the oidc-webhook latest CA bundle secret
func getLatestIssuedCABundleSecret(ctx context.Context, c client.Client, namespace string) (*corev1.Secret, error) {
	secretList := &corev1.SecretList{}
	if err := c.List(ctx, secretList, client.InNamespace(namespace), client.MatchingLabels{
		secretsmanager.LabelKeyBundleFor:       v1beta1constants.SecretNameCACluster,
		secretsmanager.LabelKeyManagedBy:       secretsmanager.LabelValueSecretsManager,
		secretsmanager.LabelKeyManagerIdentity: "gardenlet",
	}); err != nil {
		return nil, err
	}
	if len(secretList.Items) == 0 {
		return nil, fmt.Errorf("bundle CA secrets found for '%s' not found", v1beta1constants.SecretNameCACluster)
	}
	return getLatestIssuedSecret(secretList.Items)
}

// getLatestIssuedSecret returns the secret with the "issued-at-time" label that represents the latest point in time
func getLatestIssuedSecret(secrets []corev1.Secret) (*corev1.Secret, error) {
	var newestSecret *corev1.Secret
	var currentIssuedAtTime time.Time
	for i := range secrets {
		// if some of the secrets have no "issued-at-time" label
		// we have a problem since this is the source of truth
		issuedAt, ok := secrets[i].Labels[secretsmanager.LabelKeyIssuedAtTime]
		if !ok {
			return nil, fmt.Errorf("bundle CA secret %s in namespace %s has no 'issued-at-time' label", secrets[i].Name, secrets[i].Namespace)
		}

		issuedAtUnix, err := strconv.ParseInt(issuedAt, 10, 64)
		if err != nil {
			return nil, err
		}

		issuedAtTime := time.Unix(issuedAtUnix, 0).UTC()
		if newestSecret == nil || issuedAtTime.After(currentIssuedAtTime) {
			newestSecret = &secrets[i]
			currentIssuedAtTime = issuedAtTime
		}
	}

	return newestSecret, nil
}
