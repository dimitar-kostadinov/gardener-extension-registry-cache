package spegel

import (
	"context"
	"strconv"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	kubeapiserverconstants "github.com/gardener/gardener/pkg/component/kubernetes/apiserver/constants"
	monitoringutils "github.com/gardener/gardener/pkg/component/observability/monitoring/utils"
	"github.com/gardener/gardener/pkg/controllerutils"
	kubernetesutils "github.com/gardener/gardener/pkg/utils/kubernetes"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	monitoringv1alpha1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func deployMonitoringScrapeConfig(ctx context.Context, client client.Client, namespace string) error {
	scrapeConfig := emptyScrapeConfig(namespace)
	if _, err := controllerutils.GetAndCreateOrMergePatch(ctx, client, scrapeConfig, func() error {
		metav1.SetMetaDataLabel(&scrapeConfig.ObjectMeta, "component", "registry-spegel")
		metav1.SetMetaDataLabel(&scrapeConfig.ObjectMeta, "prometheus", "shoot")
		scrapeConfig.Spec = monitoringv1alpha1.ScrapeConfigSpec{
			HonorLabels:   ptr.To(false),
			ScrapeTimeout: ptr.To(monitoringv1.Duration("10s")),
			Scheme:        ptr.To("HTTPS"),
			// This is needed because the kubelets' certificates are not are generated for a specific pod IP
			TLSConfig: &monitoringv1.SafeTLSConfig{InsecureSkipVerify: ptr.To(true)},
			Authorization: &monitoringv1.SafeAuthorization{Credentials: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "shoot-access-prometheus-shoot"},
				Key:                  "token",
			}},
			KubernetesSDConfigs: []monitoringv1alpha1.KubernetesSDConfig{{
				APIServer:  ptr.To("https://" + v1beta1constants.DeploymentNameKubeAPIServer + ":" + strconv.Itoa(kubeapiserverconstants.Port)),
				Role:       "endpoints",
				Namespaces: &monitoringv1alpha1.NamespaceDiscovery{Names: []string{metav1.NamespaceSystem}},
				Authorization: &monitoringv1.SafeAuthorization{Credentials: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "shoot-access-prometheus-shoot"},
					Key:                  "token",
				}},
				// This is needed because we do not fetch the correct cluster CA bundle right now
				TLSConfig:       &monitoringv1.SafeTLSConfig{InsecureSkipVerify: ptr.To(true)},
				FollowRedirects: ptr.To(true),
			}},
			RelabelConfigs: []monitoringv1.RelabelConfig{
				{
					Action:      "replace",
					Replacement: ptr.To("registry-cache-metrics"),
					TargetLabel: "job",
				},
				{
					SourceLabels: []monitoringv1.LabelName{"__meta_kubernetes_service_name", "__meta_kubernetes_endpoint_port_name"},
					Action:       "keep",
					Regex:        `node-exporter;metrics`,
				},
				{
					Action: "labelmap",
					Regex:  `__meta_kubernetes_service_label_(.+)`,
				},
				{
					SourceLabels: []monitoringv1.LabelName{"__meta_kubernetes_pod_name"},
					TargetLabel:  "pod",
				},
				{
					SourceLabels: []monitoringv1.LabelName{"__meta_kubernetes_pod_node_name"},
					TargetLabel:  "node",
				},
				{
					TargetLabel: "__address__",
					Action:      "replace",
					Replacement: ptr.To(v1beta1constants.DeploymentNameKubeAPIServer + ":" + strconv.Itoa(kubeapiserverconstants.Port)),
				},
				{
					SourceLabels: []monitoringv1.LabelName{"__meta_kubernetes_pod_name", "__meta_kubernetes_pod_container_port_number"},
					Action:       "replace",
					TargetLabel:  "__metrics_path__",
					Regex:        `(.+);(.+)`,
					Replacement:  ptr.To("/api/v1/namespaces/kube-system/pods/${1}:${2}/proxy/metrics"),
				},
			},
			MetricRelabelConfigs: monitoringutils.StandardMetricRelabelConfig("spegel_.+|http_requests_.+|http_response_.+"),
		}
		return nil
	}); err != nil {
		return err
	}

	return nil
}

func destroyMonitoringScrapeConfig(ctx context.Context, client client.Client, namespace string) error {
	return kubernetesutils.DeleteObjects(ctx, client, emptyScrapeConfig(namespace))
}

func emptyScrapeConfig(namespace string) *monitoringv1alpha1.ScrapeConfig {
	return &monitoringv1alpha1.ScrapeConfig{ObjectMeta: monitoringutils.ConfigObjectMeta("registry-spegel", namespace, "shoot")}
}
