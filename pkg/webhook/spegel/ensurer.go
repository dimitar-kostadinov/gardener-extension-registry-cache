// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package spegel

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	extensionscontroller "github.com/gardener/gardener/extensions/pkg/controller"
	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"
	extensionscontextwebhook "github.com/gardener/gardener/extensions/pkg/webhook/context"
	"github.com/gardener/gardener/extensions/pkg/webhook/controlplane/genericmutator"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/component/extensions/operatingsystemconfig/original/components/containerd"
	"github.com/gardener/gardener/pkg/component/extensions/operatingsystemconfig/original/components/kubelet"
	"github.com/gardener/gardener/pkg/utils"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/gardener/gardener-extension-registry-cache/pkg/apis/spegel"
	spegelutils "github.com/gardener/gardener-extension-registry-cache/pkg/utils/spegel"
)

// NewEnsurer creates a new spegel configuration ensurer.
func NewEnsurer(client client.Client, decoder runtime.Decoder, logger logr.Logger) genericmutator.Ensurer {
	return &ensurer{
		client:  client,
		decoder: decoder,
		logger:  logger.WithName("registry-spegel-ensurer"),
	}
}

type ensurer struct {
	genericmutator.NoopEnsurer

	client  client.Client
	decoder runtime.Decoder
	logger  logr.Logger
}

const (
	// spegelUnitName is the name of the spegel service unit.
	spegelUnitName = "spegel.service"
	// spegelMetricsUnitName is the name of the spegel metrics service unit.
	spegelMetricsUnitName = "spegel-metrics.service"
	// 	spegelConfiguration   = `caBundle: %s
	// server: %s`
	// defaultHostsToml is the content of default registry host namespace if no other namespace matches.
	defaultHostsToml = `# managed by gardener-extension-registry-cache
[host."http://localhost:%d"]
  capabilities = ["pull", "resolve"]
`
	metricsScraperScript = `#!/bin/bash
set -euo pipefail

function scrape_spegel_metrics {
  while true
  do
    if curl --request GET -sL --url 'http://localhost:%d/metrics' --output "$output_file.tmp"; then
      if [ -f "$output_file.tmp" ]; then
        mv "$output_file.tmp" "$output_file"
      else
        echo "file $output_file.tmp is missing"
      fi
    else
      echo "curl failure: $?"
    fi
    echo "sleep"
    sleep $SLEEP_SECONDS
  done
}

output_file="var/lib/node-exporter/textfile-collector/spegel.prom"
SLEEP_SECONDS=5
echo "Start scraping spegel metrics"
scrape_spegel_metrics`
	spegelBootstrapCAFile     = "/var/lib/spegel/certs/ca.crt"
	spegelBootstrapTLSCrtFile = "/var/lib/spegel/certs/tls.crt"
	spegelBootstrapTLSKeyFile = "/var/lib/spegel/certs/tls.key"
)

func (e *ensurer) EnsureAdditionalFiles(ctx context.Context, gctx extensionscontextwebhook.GardenContext, newFiles, _ *[]extensionsv1alpha1.File) error {
	cluster, err := gctx.GetCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get the cluster resource: %w", err)
	}

	if cluster.Shoot.DeletionTimestamp != nil {
		e.logger.Info("Shoot has a deletion timestamp set, skipping the OperatingSystemConfig mutation", "shoot", client.ObjectKeyFromObject(cluster.Shoot))
		return nil
	}

	extension := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "registry-spegel",
			Namespace: cluster.ObjectMeta.Name,
		},
	}
	if err := e.client.Get(ctx, client.ObjectKeyFromObject(extension), extension); err != nil {
		return fmt.Errorf("failed to get extension '%s': %w", client.ObjectKeyFromObject(extension), err)
	}

	if extension.Spec.ProviderConfig == nil {
		return fmt.Errorf("extension '%s' does not have a .spec.providerConfig specified", client.ObjectKeyFromObject(extension))
	}

	spegelConfig := &api.SpegelConfig{}
	if _, _, err := e.decoder.Decode(extension.Spec.ProviderConfig.Raw, nil, spegelConfig); err != nil {
		return fmt.Errorf("failed to decode providerConfig of extension '%s': %w", client.ObjectKeyFromObject(extension), err)
	}

	*newFiles = extensionswebhook.EnsureFileWithPath(*newFiles, extensionsv1alpha1.File{
		Path:        "/etc/containerd/certs.d/_default/hosts.toml",
		Permissions: ptr.To[uint32](0644),
		Content: extensionsv1alpha1.FileContent{
			Inline: &extensionsv1alpha1.FileContentInline{
				Encoding: string(extensionsv1alpha1.B64FileCodecID),
				Data:     utils.EncodeBase64([]byte(fmt.Sprintf(defaultHostsToml, *spegelConfig.RegistryPort))),
			},
		},
	})

	spegelStatus, err := e.getProviderStatus(ctx, cluster)
	if err != nil {
		return err
	}

	caSecret, err := getSecret(ctx, e.client, cluster.ObjectMeta.Name, spegelStatus.CASecretName)
	if err != nil {
		return err
	}

	bundleCrt, ok := caSecret.Data["bundle.crt"]
	if !ok {
		return fmt.Errorf("failed to find 'bundle.crt' key in the CA bundle secret '%s'", client.ObjectKeyFromObject(caSecret))
	}

	clientTLSSecret, err := getSecret(ctx, e.client, cluster.ObjectMeta.Name, spegelStatus.ClientTLSSecretName)
	if err != nil {
		return err
	}

	tlsCrt := clientTLSSecret.Data["tls.crt"]
	if !ok {
		return fmt.Errorf("failed to find 'tls.crt' key in the client TLS secret '%s'", client.ObjectKeyFromObject(clientTLSSecret))
	}

	tlsKey := clientTLSSecret.Data["tls.key"]
	if !ok {
		return fmt.Errorf("failed to find 'tls.key' key in the client TLS secret '%s'", client.ObjectKeyFromObject(clientTLSSecret))
	}

	*newFiles = extensionswebhook.EnsureFileWithPath(*newFiles, extensionsv1alpha1.File{
		Path:        spegelBootstrapCAFile,
		Permissions: ptr.To[uint32](0600),
		Content: extensionsv1alpha1.FileContent{
			Inline: &extensionsv1alpha1.FileContentInline{
				Encoding: string(extensionsv1alpha1.B64FileCodecID),
				Data:     utils.EncodeBase64(bundleCrt),
			},
		},
	})

	*newFiles = extensionswebhook.EnsureFileWithPath(*newFiles, extensionsv1alpha1.File{
		Path:        spegelBootstrapTLSCrtFile,
		Permissions: ptr.To[uint32](0600),
		Content: extensionsv1alpha1.FileContent{
			Inline: &extensionsv1alpha1.FileContentInline{
				Encoding: string(extensionsv1alpha1.B64FileCodecID),
				Data:     utils.EncodeBase64(tlsCrt),
			},
		},
	})

	*newFiles = extensionswebhook.EnsureFileWithPath(*newFiles, extensionsv1alpha1.File{
		Path:        spegelBootstrapTLSKeyFile,
		Permissions: ptr.To[uint32](0600),
		Content: extensionsv1alpha1.FileContent{
			Inline: &extensionsv1alpha1.FileContentInline{
				Encoding: string(extensionsv1alpha1.B64FileCodecID),
				Data:     utils.EncodeBase64(tlsKey),
			},
		},
	})

	*newFiles = extensionswebhook.EnsureFileWithPath(*newFiles, extensionsv1alpha1.File{
		Path:        v1beta1constants.OperatingSystemConfigFilePathBinaries + "/spegel",
		Permissions: ptr.To[uint32](0755),
		Content: extensionsv1alpha1.FileContent{
			ImageRef: &extensionsv1alpha1.FileContentImageRef{
				//TODO:
				//Image:           "ghcr.io/spegel-org/spegel:v0.0.28",
				Image:           "garden.local.gardener.cloud:5001/spegel-org/spegel:v0.5.1-test", //"reg.seed-aws.i024114.shoot.dev.k8s-hana.ondemand.com/spegel-org/spegel:v0.2.0-test3",
				FilePathInImage: "/app/spegel",
			},
		},
	})

	*newFiles = extensionswebhook.EnsureFileWithPath(*newFiles, extensionsv1alpha1.File{
		Path:        v1beta1constants.OperatingSystemConfigFilePathBinaries + "/spegel_metrics.sh",
		Permissions: ptr.To[uint32](0755),
		Content: extensionsv1alpha1.FileContent{
			Inline: &extensionsv1alpha1.FileContentInline{
				Encoding: string(extensionsv1alpha1.B64FileCodecID),
				Data:     utils.EncodeBase64([]byte(fmt.Sprintf(metricsScraperScript, *spegelConfig.MetricsPort))),
			},
		},
	})

	return nil
}

func (e *ensurer) EnsureAdditionalUnits(ctx context.Context, gctx extensionscontextwebhook.GardenContext, newUnits, _ *[]extensionsv1alpha1.Unit) error {
	cluster, err := gctx.GetCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get the cluster resource: %w", err)
	}

	if cluster.Shoot.DeletionTimestamp != nil {
		e.logger.Info("Shoot has a deletion timestamp set, skipping the OperatingSystemConfig mutation", "shoot", client.ObjectKeyFromObject(cluster.Shoot))
		return nil
	}

	extension := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "registry-spegel",
			Namespace: cluster.ObjectMeta.Name,
		},
	}
	if err := e.client.Get(ctx, client.ObjectKeyFromObject(extension), extension); err != nil {
		return fmt.Errorf("failed to get extension '%s': %w", client.ObjectKeyFromObject(extension), err)
	}

	if extension.Spec.ProviderConfig == nil {
		return fmt.Errorf("extension '%s' does not have a .spec.providerConfig specified", client.ObjectKeyFromObject(extension))
	}

	spegelConfig := &api.SpegelConfig{}
	if _, _, err := e.decoder.Decode(extension.Spec.ProviderConfig.Raw, nil, spegelConfig); err != nil {
		return fmt.Errorf("failed to decode providerConfig of extension '%s': %w", client.ObjectKeyFromObject(extension), err)
	}

	ingress := spegelutils.ComputeIngressHost(cluster.Shoot.Status.TechnicalID, cluster.Seed.Spec.Ingress.Domain)

	*newUnits = extensionswebhook.EnsureUnitWithName(*newUnits, extensionsv1alpha1.Unit{
		Name:    spegelUnitName,
		Command: ptr.To(extensionsv1alpha1.CommandStart),
		Enable:  ptr.To(true),
		Content: ptr.To(`[Unit]
Description=spegel daemon
Documentation=https://github.com/spegel-org/spegel
After=` + containerd.UnitName + `
Requires=` + containerd.UnitName + `
Before=` + kubelet.UnitName + `
[Install]
WantedBy=multi-user.target
[Service]
Restart=always
RestartSec=5
MemoryHigh=80M
MemoryMax=100M
ExecStart=` + v1beta1constants.OperatingSystemConfigFilePathBinaries + `/spegel \
    ` + utils.Indent(strings.Join(getCLIFlags(spegelConfig, ingress), " \\\n"), 4) + "\n"),
		FilePaths: []string{v1beta1constants.OperatingSystemConfigFilePathBinaries + "/spegel"},
	})

	*newUnits = extensionswebhook.EnsureUnitWithName(*newUnits, extensionsv1alpha1.Unit{
		Name:    spegelMetricsUnitName,
		Command: ptr.To(extensionsv1alpha1.CommandStart),
		Enable:  ptr.To(true),
		Content: ptr.To(`[Unit]
Description=spegel metrics daemon
Documentation=https://github.com/spegel-org/spegel
After=` + spegelUnitName + `
BindsTo=` + spegelUnitName + `
[Install]
WantedBy=multi-user.target ` + spegelUnitName + `
[Service]
Restart=always
RestartSec=5
ExecStart=` + v1beta1constants.OperatingSystemConfigFilePathBinaries + `/spegel_metrics.sh`),
		FilePaths: []string{v1beta1constants.OperatingSystemConfigFilePathBinaries + "/spegel_metrics.sh"},
	})

	return nil
}

// EnsureCRIConfig ensures the CRI config.
func (e *ensurer) EnsureCRIConfig(ctx context.Context, gctx extensionscontextwebhook.GardenContext, newCRIConfig, _ *extensionsv1alpha1.CRIConfig) error {
	cluster, err := gctx.GetCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get the cluster resource: %w", err)
	}

	if cluster.Shoot.DeletionTimestamp != nil {
		e.logger.Info("Shoot has a deletion timestamp set, skipping the OperatingSystemConfig mutation", "shoot", client.ObjectKeyFromObject(cluster.Shoot))
		return nil
	}
	extension := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "registry-spegel",
			Namespace: cluster.ObjectMeta.Name,
		},
	}
	if err := e.client.Get(ctx, client.ObjectKeyFromObject(extension), extension); err != nil {
		return fmt.Errorf("failed to get extension '%s': %w", client.ObjectKeyFromObject(extension), err)
	}

	if extension.Spec.ProviderConfig == nil {
		return fmt.Errorf("extension '%s' does not have a .spec.providerConfig specified", client.ObjectKeyFromObject(extension))
	}

	spegelConfig := &api.SpegelConfig{}
	if _, _, err := e.decoder.Decode(extension.Spec.ProviderConfig.Raw, nil, spegelConfig); err != nil {
		return fmt.Errorf("failed to decode providerConfig of extension '%s': %w", client.ObjectKeyFromObject(extension), err)
	}

	if newCRIConfig.Containerd == nil {
		newCRIConfig.Containerd = &extensionsv1alpha1.ContainerdConfig{}
	}

	// ensure discard_unpacked_layers is set to false
	// version = 2 -> [plugins.'io.containerd.grpc.v1.cri'.containerd]
	// version = 3 -> [plugins.'io.containerd.cri.v1.images'] TBD?
	i := slices.IndexFunc(newCRIConfig.Containerd.Plugins, func(pluginConfig extensionsv1alpha1.PluginConfig) bool {
		return slices.Equal(pluginConfig.Path, []string{"io.containerd.grpc.v1.cri", "containerd"}) && ptr.Deref(pluginConfig.Op, extensionsv1alpha1.AddPluginPathOperation) == extensionsv1alpha1.AddPluginPathOperation
	})
	if i == -1 {
		newCRIConfig.Containerd.Plugins = append(newCRIConfig.Containerd.Plugins, extensionsv1alpha1.PluginConfig{
			Path:   []string{"io.containerd.grpc.v1.cri", "containerd"},
			Values: &apiextensionsv1.JSON{Raw: []byte(`{"discard_unpacked_layers": false}`)},
		})
	} else {
		pluginConfig := newCRIConfig.Containerd.Plugins[i]
		if pluginConfig.Values != nil && len(pluginConfig.Values.Raw) > 0 {
			values := map[string]any{}
			err := json.Unmarshal(pluginConfig.Values.Raw, &values)
			if err != nil {
				return fmt.Errorf("failed to unmarshal [plugins.'io.containerd.grpc.v1.cri'.containerd] values %s: %w", string(pluginConfig.Values.Raw), err)
			}
			values["discard_unpacked_layers"] = false
			rawValues, err := json.Marshal(values)
			if err != nil {
				return fmt.Errorf("failed to marshal [plugins.'io.containerd.grpc.v1.cri'.containerd] values %v: %w", values, err)
			}
			newCRIConfig.Containerd.Plugins[i].Values = &apiextensionsv1.JSON{Raw: rawValues}
		} else {
			newCRIConfig.Containerd.Plugins[i].Values = &apiextensionsv1.JSON{Raw: []byte(`{"discard_unpacked_layers": false}`)}
		}
	}

	// inject Spegel configuration
	// TODO: What happens if another webhook is then executed? reinvocationPolicy: IfNeeded?
	// TODO: What if ReadinessProbe=true, the spegel binary is download as imageRef file?
	for i := range newCRIConfig.Containerd.Registries {
		if newCRIConfig.Containerd.Registries[i].Hosts[0].URL != fmt.Sprintf("http://localhost:%d", *spegelConfig.RegistryPort) { //TODO: cache & mirror webhooks should be updated to not overwrite spegel configuration
			newCRIConfig.Containerd.Registries[i].Hosts = append([]extensionsv1alpha1.RegistryHost{
				{
					URL:          fmt.Sprintf("http://localhost:%d", *spegelConfig.RegistryPort),
					Capabilities: []extensionsv1alpha1.RegistryCapability{extensionsv1alpha1.PullCapability, extensionsv1alpha1.ResolveCapability},
				},
			}, newCRIConfig.Containerd.Registries[i].Hosts...)
		}
	}

	// #############

	// What to TODO?: explicitly overwrite host.toml files in local setup
	// "europe-docker.pkg.dev" , "gcr.io", "quay.io", "registry.k8s.io"
	if cluster.Shoot.Name == "local" {
		for _, upstream := range []string{"europe-docker.pkg.dev", "gcr.io", "quay.io", "registry.k8s.io"} {
			cfg := extensionsv1alpha1.RegistryConfig{
				Upstream: upstream,
				Server:   ptr.To(fmt.Sprintf("https://%s", upstream)),
				Hosts: []extensionsv1alpha1.RegistryHost{{
					URL:          fmt.Sprintf("http://localhost:%d", *spegelConfig.RegistryPort),
					Capabilities: []extensionsv1alpha1.RegistryCapability{extensionsv1alpha1.PullCapability, extensionsv1alpha1.ResolveCapability},
				}},
			}
			i := slices.IndexFunc(newCRIConfig.Containerd.Registries, func(registryConfig extensionsv1alpha1.RegistryConfig) bool {
				return registryConfig.Upstream == cfg.Upstream
			})

			if i == -1 {
				newCRIConfig.Containerd.Registries = append(newCRIConfig.Containerd.Registries, cfg)
			} else {
				newCRIConfig.Containerd.Registries[i] = cfg
			}
		}
	}

	return nil
}

func getCLIFlags(spegelConfig *api.SpegelConfig, ingress string) []string {
	return []string{"registry",
		"--log-level=DEBUG",
		"--mirror-resolve-retries=3",
		"--mirror-resolve-timeout=20ms",
		fmt.Sprintf("--registry-addr=:%d", *spegelConfig.RegistryPort),
		fmt.Sprintf("--router-addr=:%d", *spegelConfig.RouterPort),
		fmt.Sprintf("--metrics-addr=:%d", *spegelConfig.MetricsPort),
		"--containerd-sock=/run/containerd/containerd.sock",
		"--containerd-namespace=k8s.io",
		"--containerd-registry-config-path=/etc/containerd/certs.d",
		"--bootstrap-kind=external",
		fmt.Sprintf("--external-bootstrap-url=https://%s/bootstrap-nodes", ingress),
		fmt.Sprintf("--external-bootstrap-ca=%s", spegelBootstrapCAFile),
		fmt.Sprintf("--external-bootstrap-tls-crt=%s", spegelBootstrapTLSCrtFile),
		fmt.Sprintf("--external-bootstrap-tls-key=%s", spegelBootstrapTLSKeyFile),
		"--containerd-content-path=/var/lib/containerd/io.containerd.content.v1.content",
	}
}

func getSecret(ctx context.Context, c client.Client, namespace, name string) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(secret), secret); err != nil {
		return nil, fmt.Errorf("failed to get secret '%s': %w", client.ObjectKeyFromObject(secret), err)
	}
	return secret, nil
}

func (e *ensurer) getProviderStatus(ctx context.Context, cluster *extensionscontroller.Cluster) (*api.SpegelStatus, error) {
	extension := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "registry-spegel",
			Namespace: cluster.ObjectMeta.Name,
		},
	}
	if err := e.client.Get(ctx, client.ObjectKeyFromObject(extension), extension); err != nil {
		return nil, fmt.Errorf("failed to get extension '%s': %w", client.ObjectKeyFromObject(extension), err)
	}

	fmt.Println()

	if extension.Status.ProviderStatus == nil {
		return nil, fmt.Errorf("extension '%s' does not have a .status.providerStatus specified", client.ObjectKeyFromObject(extension))
	}

	spegelStatus := &api.SpegelStatus{}
	if err := runtime.DecodeInto(e.decoder, extension.Status.ProviderStatus.Raw, spegelStatus); err != nil {
		return nil, fmt.Errorf("failed to decode providerStatus of extension '%s': %w", client.ObjectKeyFromObject(extension), err)
	}
	return spegelStatus, nil
}
