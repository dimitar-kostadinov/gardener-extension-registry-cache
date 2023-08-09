// Copyright (c) 2022 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"fmt"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"strings"
	"time"

	extensionsconfig "github.com/gardener/gardener/extensions/pkg/apis/config"
	"github.com/gardener/gardener/extensions/pkg/controller"
	"github.com/gardener/gardener/extensions/pkg/controller/extension"
	"github.com/gardener/gardener/extensions/pkg/util"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/client/kubernetes"
	"github.com/gardener/gardener/pkg/controllerutils"
	"github.com/gardener/gardener/pkg/resourcemanager/controller/garbagecollector/references"
	kubernetesutils "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/gardener/gardener/pkg/utils/managedresources"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gardener/gardener-extension-registry-cache/pkg/apis/config"
	"github.com/gardener/gardener-extension-registry-cache/pkg/apis/registry/v1alpha1"
	"github.com/gardener/gardener-extension-registry-cache/pkg/imagevector"
)

const scrapeConfig = `- job_name: '%s-metrics'
  scheme: https
  tls_config:
    ca_file: /etc/prometheus/seed/ca.crt
  authorization:
    type: Bearer
    credentials_file: /var/run/secrets/gardener.cloud/shoot/token/token
  honor_labels: false      
  kubernetes_sd_configs:
  - role: endpoints
    api_server: https://kube-apiserver:443
    namespaces:
      names: [%s]
    tls_config:
      ca_file: /etc/prometheus/seed/ca.crt
    authorization:
      type: Bearer
      credentials_file: /var/run/secrets/gardener.cloud/shoot/token/token
  relabel_configs:
  - source_labels: [__meta_kubernetes_service_name, __meta_kubernetes_endpoint_port_name]
    regex: %s;%s
    action: keep
  - action: labelmap
    regex: __meta_kubernetes_service_label_(.+)        
  - target_label: __address__
    replacement: kube-apiserver:443 
  - source_labels: [__meta_kubernetes_endpoint_node_name]
    target_label: node
  - source_labels: [__meta_kubernetes_pod_name]
    target_label: pod
  - source_labels: [__meta_kubernetes_pod_name, __meta_kubernetes_pod_container_port_number]
    regex: (.+);(.+)
    target_label: __metrics_path__
    replacement: /api/v1/namespaces/%s/pods/${1}:${2}/proxy/metrics
  metric_relabel_configs:
  - source_labels: [ __name__ ]
    regex: registry_proxy_.+
    action: keep
`

const dashboard = `{
  "annotations": {
    "list": [
      {
        "builtIn": 1,
        "datasource": "-- Plutono --",
        "enable": true,
        "hide": true,
        "iconColor": "rgba(0, 211, 255, 1)",
        "name": "Annotations & Alerts",
        "type": "dashboard"
      }
    ]
  },
  "editable": true,
  "gnetId": null,
  "graphTooltip": 1,
  "id": null,
  "iteration": 1691483983180,
  "links": [],
  "panels": [
    {
      "collapsed": false,
      "datasource": null,
      "gridPos": {
        "h": 1,
        "w": 24,
        "x": 0,
        "y": 0
      },
      "id": 39,
      "panels": [],
      "repeat": null,
      "title": "Registry Proxy Cache",
      "type": "row"
    },
    {
      "datasource": null,
      "description": "Pulled bytes from upstream",
      "fieldConfig": {
        "defaults": {
          "color": {
            "mode": "thresholds"
          },
          "mappings": [],
          "thresholds": {
            "mode": "absolute",
            "steps": [
              {
                "color": "green",
                "value": null
              },
              {
                "color": "red",
                "value": 80
              }
            ]
          }
        },
        "overrides": []
      },
      "gridPos": {
        "h": 6,
        "w": 4,
        "x": 0,
        "y": 1
      },
      "id": 41,
      "options": {
        "colorMode": "value",
        "graphMode": "area",
        "justifyMode": "auto",
        "orientation": "auto",
        "reduceOptions": {
          "calcs": [
            "lastNotNull"
          ],
          "fields": "",
          "values": false
        },
        "text": {},
        "textMode": "auto"
      },
      "pluginVersion": "7.5.22",
      "targets": [
        {
          "exemplar": true,
          "expr": "registry_proxy_bytesPulled_total/1000000",
          "format": "time_series",
          "instant": false,
          "interval": "",
          "intervalFactor": 1,
          "legendFormat": "pulled bytes from upstream {{pod}}",
          "refId": "A"
        }
      ],
      "title": "Pulled",
      "type": "stat"
    },
    {
      "datasource": null,
      "description": "Pulled bytes from upstream",
      "fieldConfig": {
        "defaults": {
          "color": {
            "mode": "thresholds"
          },
          "mappings": [],
          "thresholds": {
            "mode": "absolute",
            "steps": [
              {
                "color": "green",
                "value": null
              },
              {
                "color": "red",
                "value": 80
              }
            ]
          }
        },
        "overrides": []
      },
      "gridPos": {
        "h": 6,
        "w": 4,
        "x": 4,
        "y": 1
      },
      "id": 43,
      "options": {
        "colorMode": "value",
        "graphMode": "area",
        "justifyMode": "auto",
        "orientation": "auto",
        "reduceOptions": {
          "calcs": [
            "lastNotNull"
          ],
          "fields": "",
          "values": false
        },
        "text": {},
        "textMode": "auto"
      },
      "pluginVersion": "7.5.22",
      "targets": [
        {
          "exemplar": true,
          "expr": "registry_proxy_bytesPushed_total/1000000",
          "format": "time_series",
          "instant": false,
          "interval": "",
          "intervalFactor": 1,
          "legendFormat": "pushed bytes from upstream {{pod}}",
          "refId": "A"
        }
      ],
      "title": "Pushed",
      "type": "stat"
    },
    {
      "datasource": null,
      "description": "Pulled bytes from upstream",
      "fieldConfig": {
        "defaults": {
          "color": {
            "mode": "thresholds"
          },
          "mappings": [],
          "thresholds": {
            "mode": "absolute",
            "steps": [
              {
                "color": "green",
                "value": null
              },
              {
                "color": "red",
                "value": 80
              }
            ]
          }
        },
        "overrides": []
      },
      "gridPos": {
        "h": 6,
        "w": 4,
        "x": 8,
        "y": 1
      },
      "id": 44,
      "options": {
        "colorMode": "value",
        "graphMode": "area",
        "justifyMode": "auto",
        "orientation": "auto",
        "reduceOptions": {
          "calcs": [
            "lastNotNull"
          ],
          "fields": "",
          "values": false
        },
        "text": {},
        "textMode": "auto"
      },
      "pluginVersion": "7.5.22",
      "targets": [
        {
          "exemplar": true,
          "expr": "(registry_proxy_bytesPushed_total - registry_proxy_bytesPulled_total)/1000000",
          "format": "time_series",
          "instant": false,
          "interval": "",
          "intervalFactor": 1,
          "legendFormat": "pulled bytes from upstream {{pod}}",
          "refId": "A"
        }
      ],
      "title": "Delta",
      "type": "stat"
    },
    {
      "aliasColors": {},
      "bars": false,
      "dashLength": 10,
      "dashes": false,
      "datasource": "prometheus",
      "description": "The cache hits describes how many image pull requests were answered due to a cached image.\n\nThe cache misses describes how much image pull requests need to be done to upstream, because the requests image does not exist in the local cache.",
      "editable": true,
      "error": false,
      "fieldConfig": {
        "defaults": {
          "links": []
        },
        "overrides": []
      },
      "fill": 1,
      "fillGradient": 0,
      "grid": {},
      "gridPos": {
        "h": 7,
        "w": 12,
        "x": 12,
        "y": 1
      },
      "hiddenSeries": false,
      "id": 42,
      "interval": null,
      "legend": {
        "avg": false,
        "current": false,
        "max": false,
        "min": false,
        "show": true,
        "total": false,
        "values": false
      },
      "lines": true,
      "linewidth": 2,
      "links": [],
      "nullPointMode": "connected",
      "options": {
        "alertThreshold": true
      },
      "percentage": false,
      "pluginVersion": "7.5.22",
      "pointradius": 5,
      "points": false,
      "renderer": "flot",
      "seriesOverrides": [],
      "spaceLength": 10,
      "stack": false,
      "steppedLine": false,
      "targets": [
        {
          "exemplar": true,
          "expr": "rate(registry_proxy_hits_total[$__rate_interval])",
          "format": "time_series",
          "hide": false,
          "instant": false,
          "interval": "",
          "intervalFactor": 2,
          "legendFormat": "hits {{app}}",
          "refId": "A",
          "step": 40
        },
        {
          "exemplar": true,
          "expr": "rate(registry_proxy_misses_total[$__rate_interval])",
          "format": "time_series",
          "instant": false,
          "interval": "",
          "intervalFactor": 2,
          "legendFormat": "misses {{app}}",
          "refId": "B",
          "step": 40
        }
      ],
      "thresholds": [],
      "timeFrom": null,
      "timeRegions": [],
      "timeShift": null,
      "title": "Cache Hits and Misses",
      "tooltip": {
        "shared": true,
        "sort": 0,
        "value_type": "cumulative"
      },
      "type": "graph",
      "xaxis": {
        "buckets": null,
        "mode": "time",
        "name": null,
        "show": true,
        "values": []
      },
      "yaxes": [
        {
          "$$hashKey": "object:211",
          "format": "none",
          "logBase": 1,
          "max": null,
          "min": 0,
          "show": true
        },
        {
          "$$hashKey": "object:212",
          "format": "pps",
          "logBase": 1,
          "max": null,
          "min": 0,
          "show": false
        }
      ],
      "yaxis": {
        "align": false,
        "alignLevel": null
      }
    }
  ],
  "refresh": "1h",
  "schemaVersion": 27,
  "style": "dark",
  "tags": [
    "image",
    "cache"
  ],
  "templating": {
    "list": []
  },
  "time": {
    "from": "now-3h",
    "to": "now"
  },
  "timepicker": {
    "refresh_intervals": [
      "10s",
      "30s",
      "1m",
      "5m",
      "15m",
      "30m",
      "1h"
    ],
    "time_options": [
      "5m",
      "15m",
      "1h",
      "3h",
      "6h",
      "12h",
      "24h",
      "2d",
      "7d",
      "14d"
    ]
  },
  "timezone": "utc",
  "title": "Registry Proxy Cache",
  "uid": "extension-extension-registry-cache",
  "version": 1
}`

// NewActuator returns an actuator responsible for Extension resources.
func NewActuator(config config.Configuration) extension.Actuator {
	return &actuator{
		config: config,
	}
}

type actuator struct {
	client  client.Client
	decoder runtime.Decoder
	config  config.Configuration
}

// InjectClient injects the controller runtime client into the reconciler.
func (a *actuator) InjectClient(client client.Client) error {
	a.client = client
	return nil
}

// InjectScheme injects the given scheme into the reconciler.
func (a *actuator) InjectScheme(scheme *runtime.Scheme) error {
	a.decoder = serializer.NewCodecFactory(scheme, serializer.EnableStrict).UniversalDecoder()
	return nil
}

// Reconcile the Extension resource.
func (a *actuator) Reconcile(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	if ex.Spec.ProviderConfig == nil {
		return nil
	}

	namespace := ex.GetNamespace()

	cluster, err := controller.GetCluster(ctx, a.client, namespace)
	if err != nil {
		return err
	}

	registryConfig := &v1alpha1.RegistryConfig{}
	if _, _, err := a.decoder.Decode(ex.Spec.ProviderConfig.Raw, nil, registryConfig); err != nil {
		return fmt.Errorf("failed to decode provider config: %w", err)
	}

	if err := a.createResources(ctx, log, registryConfig, cluster, namespace); err != nil {
		return err
	}

	return nil
}

// Delete the Extension resource.
func (a *actuator) Delete(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	return a.deleteResources(ctx, log, ex.GetNamespace())
}

// Restore the Extension resource.
func (a *actuator) Restore(ctx context.Context, log logr.Logger, ex *extensionsv1alpha1.Extension) error {
	return a.Reconcile(ctx, log, ex)
}

// Migrate the Extension resource.
func (a *actuator) Migrate(_ context.Context, _ logr.Logger, _ *extensionsv1alpha1.Extension) error {
	return nil
}

func (a *actuator) createResources(ctx context.Context, log logr.Logger, registryConfig *v1alpha1.RegistryConfig, cluster *controller.Cluster, namespace string) error {
	registryImage, err := imagevector.ImageVector().FindImage("registry")
	if err != nil {
		return fmt.Errorf("failed to find registry image: %w", err)
	}

	objects := []client.Object{
		&corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: registryCacheNamespaceName,
			},
		},
	}

	scrapeJobs := ""

	for _, cache := range registryConfig.Caches {
		c := registryCache{
			Namespace:                registryCacheNamespaceName,
			Upstream:                 cache.Upstream,
			VolumeSize:               *cache.Size,
			GarbageCollectionEnabled: *cache.GarbageCollectionEnabled,
			RegistryImage:            registryImage,
		}

		c.Name = strings.Replace(fmt.Sprintf("registry-%s", strings.Split(cache.Upstream, ":")[0]), ".", "-", -1)

		// init upstream registry credentials
		if cache.SecretReferenceName != nil {
			refSecretName, err := lookupReferencedSecret(cluster, *cache.SecretReferenceName)
			if err != nil {
				return err
			}
			refSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      refSecretName,
					Namespace: namespace,
				},
			}
			if err = a.client.Get(ctx, client.ObjectKeyFromObject(refSecret), refSecret); err != nil {
				return err
			}
			if err = validateUpstreamRegistrySecret(refSecret); err != nil {
				return err
			}
			c.UpstreamUsername = string(refSecret.Data["username"])
			c.UpstreamPassword = string(refSecret.Data["password"])
		}

		os, err := c.Ensure()
		if err != nil {
			log.Error(err, "could not ensure deployment")
			return err
		}

		scrapeJobs += fmt.Sprintf(scrapeConfig, c.Name, c.Namespace, c.Name, registryCacheMetricsName, c.Namespace)

		objects = append(objects, os...)
	}

	resources, err := managedresources.NewRegistry(kubernetes.SeedScheme, kubernetes.SeedCodec, kubernetes.SeedSerializer).AddAllAndSerialize(objects...)
	if err != nil {
		return err
	}

	// create ManagedResource for the registryCache
	err = a.createManagedResources(ctx, v1alpha1.RegistryResourceName, namespace, resources, map[string]string{v1beta1constants.ShootNoCleanup: "true"})
	if err != nil {
		return err
	}

	// get service IPs from shoot
	_, shootClient, err := util.NewClientForShoot(ctx, a.client, cluster.ObjectMeta.Name, client.Options{}, extensionsconfig.RESTOptions{})
	if err != nil {
		return fmt.Errorf("shoot client cannot be crated: %w", err)
	}

	selector := labels.NewSelector()
	r, err := labels.NewRequirement(registryCacheServiceUpstreamLabel, selection.Exists, nil)
	if err != nil {
		return err
	}
	selector = selector.Add(*r)

	// get all registry cache services
	services := &corev1.ServiceList{}
	if err := shootClient.List(ctx, services, client.InNamespace(registryCacheNamespaceName), client.MatchingLabelsSelector{Selector: selector}); err != nil {
		log.Error(err, "could not read services from shoot")
		return err
	}

	if len(services.Items) != len(registryConfig.Caches) {
		return fmt.Errorf("not all services for all configured caches exist")
	}

	monitoring := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "registry-cache-config-prometheus",
			Namespace: namespace,
		},
		Data: map[string]string{
			"scrape_config":       scrapeJobs,
			"dashboard_operators": fmt.Sprintf("registry-cache-dashboard.json: '%s'", dashboard),
		},
	}
	utilruntime.Must(kubernetesutils.MakeUnique(monitoring))

	_, err = controllerutils.GetAndCreateOrMergePatch(ctx, a.client, monitoring, func() error {
		metav1.SetMetaDataLabel(&monitoring.ObjectMeta, "component", "registry-cache")
		metav1.SetMetaDataLabel(&monitoring.ObjectMeta, "extensions.gardener.cloud/configuration", "monitoring")
		metav1.SetMetaDataLabel(&monitoring.ObjectMeta, references.LabelKeyGarbageCollectable, references.LabelValueGarbageCollectable)
		return nil
	})
	if err != nil {
		return err
	}

	return nil
}

func (a *actuator) deleteResources(ctx context.Context, log logr.Logger, namespace string) error {
	log.Info("deleting managed resource for registry cache")

	if err := managedresources.Delete(ctx, a.client, namespace, v1alpha1.RegistryResourceName, false); err != nil {
		return err
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return managedresources.WaitUntilDeleted(timeoutCtx, a.client, namespace, v1alpha1.RegistryResourceName)
}

func (a *actuator) createManagedResources(ctx context.Context, name, namespace string, resources map[string][]byte, injectedLabels map[string]string) error {
	var (
		secretName, secret = managedresources.NewSecret(a.client, namespace, name, resources, false)
		managedResource    = managedresources.New(a.client, namespace, name, "", pointer.Bool(false), nil, injectedLabels, pointer.Bool(false)).
					WithSecretRef(secretName).
					DeletePersistentVolumeClaims(true)
	)

	if err := secret.Reconcile(ctx); err != nil {
		return fmt.Errorf("could not create or update secret of managed resources: %w", err)
	}

	if err := managedResource.Reconcile(ctx); err != nil {
		return fmt.Errorf("could not create or update managed resource: %w", err)
	}

	return nil
}

func (a *actuator) updateStatus(ctx context.Context, ex *extensionsv1alpha1.Extension, _ *v1alpha1.RegistryConfig) error {
	patch := client.MergeFrom(ex.DeepCopy())
	// ex.Status.Resources = resources
	return a.client.Status().Patch(ctx, ex, patch)
}

func lookupReferencedSecret(cluster *controller.Cluster, refname string) (string, error) {
	if cluster.Shoot != nil {
		for _, ref := range cluster.Shoot.Spec.Resources {
			if ref.Name == refname {
				if ref.ResourceRef.Kind != "Secret" {
					err := fmt.Errorf("invalid referenced resource, expected kind Secret, not %s: %s", ref.ResourceRef.Kind, ref.ResourceRef.Name)
					return "", err
				}
				return v1beta1constants.ReferencedResourcesPrefix + ref.ResourceRef.Name, nil
			}
		}
	}
	return "", fmt.Errorf("missing or invalid referenced resource: %s", refname)
}

// validateUpstreamRegistrySecret validates the state of an upstream registry secret
func validateUpstreamRegistrySecret(secret *corev1.Secret) error {
	key := client.ObjectKeyFromObject(secret)
	if _, ok := secret.Data["username"]; !ok {
		return fmt.Errorf("secret %s is missing username value", key.String())
	}
	if _, ok := secret.Data["password"]; !ok {
		return fmt.Errorf("secret %s is missing password value", key.String())
	}
	if len(secret.Data) != 2 {
		return fmt.Errorf("secret %s should have only two data entries", key.String())
	}
	return nil
}
