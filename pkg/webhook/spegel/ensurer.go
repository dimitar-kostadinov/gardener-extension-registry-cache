// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package spegel

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"strings"

	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"
	gcontext "github.com/gardener/gardener/extensions/pkg/webhook/context"
	"github.com/gardener/gardener/extensions/pkg/webhook/controlplane/genericmutator"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/gardener/gardener/pkg/component/extensions/operatingsystemconfig/original/components/containerd"
	"github.com/gardener/gardener/pkg/component/extensions/operatingsystemconfig/original/components/kubelet"
	"github.com/gardener/gardener/pkg/utils"
	"github.com/go-logr/logr"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/gardener/gardener-extension-registry-cache/pkg/apis/spegel"
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
	// defaultHostsToml is the content of default registry host namespace if no other namespace matches.
	defaultHostsToml = `# managed by gardener-extension-registry-cache
[host."http://localhost:%d"]
  capabilities = ["pull", "resolve"]`
	metricsScraperScript = `#!/bin/bash
set -o nounset
set -o pipefail

function scrape_spegel_metrics {
  while true; do
    curl --request GET -sL \
         --url 'http://localhost:%d/metrics'\
         --output "$output_file.tmp"
    mv "$output_file.tmp" "$output_file"
    sleep $SLEEP_SECONDS
  done
}

output_file="var/lib/node-exporter/textfile-collector/spegel.prom"
SLEEP_SECONDS=5
echo "Start scraping spegel metrics"
scrape_spegel_metrics`
)

func (e *ensurer) EnsureAdditionalFiles(ctx context.Context, gctx gcontext.GardenContext, newFiles, _ *[]extensionsv1alpha1.File) error {
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
				Data:     base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(defaultHostsToml, *spegelConfig.RegistryPort))),
			},
		},
	})

	*newFiles = extensionswebhook.EnsureFileWithPath(*newFiles, extensionsv1alpha1.File{
		Path:        v1beta1constants.OperatingSystemConfigFilePathBinaries + "/spegel",
		Permissions: ptr.To[uint32](0755),
		Content: extensionsv1alpha1.FileContent{
			ImageRef: &extensionsv1alpha1.FileContentImageRef{
				//TODO: custom build image with https://github.com/spegel-org/spegel/pull/373 included
				//Image:           "ghcr.io/spegel-org/spegel:v0.0.28",
				Image:           "docker.io/kostadinov/spegel:v0.0.28-test",
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
				Data:     base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(metricsScraperScript, *spegelConfig.MetricsPort))),
			},
		},
	})

	return nil
}

func (e *ensurer) EnsureAdditionalUnits(ctx context.Context, gctx gcontext.GardenContext, newUnits, _ *[]extensionsv1alpha1.Unit) error {
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
Environment="NODE_IP=` + GetOutboundIP().String() + `"
ExecStart=` + v1beta1constants.OperatingSystemConfigFilePathBinaries + `/spegel \
    ` + utils.Indent(strings.Join(getCLIFlags(spegelConfig), " \\\n"), 4) + "\n"),
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
WantedBy=multi-user.target
[Service]
Restart=always
RestartSec=5
ExecStart=` + v1beta1constants.OperatingSystemConfigFilePathBinaries + `/spegel_metrics.sh`),
		FilePaths: []string{v1beta1constants.OperatingSystemConfigFilePathBinaries + "/spegel_metrics.sh"},
	})

	return nil
}

// EnsureCRIConfig ensures the CRI config.
func (e *ensurer) EnsureCRIConfig(ctx context.Context, gctx gcontext.GardenContext, newCRIConfig, _ *extensionsv1alpha1.CRIConfig) error {
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

	// ensure discard_unpacked_layers is set to false (version = 2) - for version = 3 -> [plugins.'io.containerd.cri.v1.images']
	newCRIConfig.Containerd.Plugins = append(newCRIConfig.Containerd.Plugins, extensionsv1alpha1.PluginConfig{
		Path:   []string{"io.containerd.grpc.v1.cri", "containerd"},
		Values: &apiextensionsv1.JSON{Raw: []byte(`{"discard_unpacked_layers": false}`)},
	})

	// inject Spegel configuration
	// TODO: What happens if another webhook is then executed? reinvocationPolicy: IfNeeded?
	// TODO: What if ReadinessProbe=true, the spegel binary is download as imageRef file?
	for i := range newCRIConfig.Containerd.Registries {
		if newCRIConfig.Containerd.Registries[i].Hosts[0].URL != fmt.Sprintf("http://localhost:%d", *spegelConfig.RegistryPort) {
			newCRIConfig.Containerd.Registries[i].Hosts = append([]extensionsv1alpha1.RegistryHost{
				{
					URL:          fmt.Sprintf("http://localhost:%d", *spegelConfig.RegistryPort),
					Capabilities: []extensionsv1alpha1.RegistryCapability{extensionsv1alpha1.PullCapability, extensionsv1alpha1.ResolveCapability},
				},
			}, newCRIConfig.Containerd.Registries[i].Hosts...)
		}
	}

	return nil
}

// GetOutboundIP Get preferred outbound ip of this machine - TODO - how to get Node IP from the machine?
func GetOutboundIP() net.IP {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer func(conn net.Conn) {
		_ = conn.Close()
	}(conn)

	localAddr := conn.LocalAddr().(*net.UDPAddr)

	return localAddr.IP
}

func getCLIFlags(spegelConfig *api.SpegelConfig) []string {
	return []string{"registry",
		"--log-level=DEBUG",
		"--mirror-resolve-retries=3",
		"--mirror-resolve-timeout=20ms",
		fmt.Sprintf("--registry-addr=:%d", *spegelConfig.RegistryPort),
		fmt.Sprintf("--router-addr=:%d", *spegelConfig.RouterPort),
		fmt.Sprintf("--metrics-addr=:%d", *spegelConfig.MetricsPort),
		"--registries",
		"--containerd-sock=/run/containerd/containerd.sock",
		"--containerd-namespace=k8s.io",
		"--containerd-registry-config-path=/etc/containerd/certs.d",
		"--bootstrap-kind=kubernetes",
		//TODO GNA kubeconfig is used, either use dedicated kubeconfig or another mechanism for router peer bootstrapping.
		"--kubeconfig-path=/var/lib/gardener-node-agent/credentials/kubeconfig",
		"--leader-election-namespace=" + metav1.NamespaceSystem,
		"--resolve-latest-tag=true",
		fmt.Sprintf("--local-addr=$(NODE_IP):%d", *spegelConfig.RegistryPort),
		"--containerd-content-path=/var/lib/containerd/io.containerd.content.v1.content",
	}
}
