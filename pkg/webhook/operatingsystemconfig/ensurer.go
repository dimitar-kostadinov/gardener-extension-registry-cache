// Copyright (c) 2023 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package operatingsystemconfig

import (
	"context"
	"fmt"
	"path/filepath"

	gcontext "github.com/gardener/gardener/extensions/pkg/webhook/context"
	"github.com/gardener/gardener/extensions/pkg/webhook/controlplane/genericmutator"
	v1beta1helper "github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gardener/gardener-extension-registry-cache/pkg/apis/registry/v1alpha1"
	registryutils "github.com/gardener/gardener-extension-registry-cache/pkg/utils/registry"
)

const hostsTOMLTemplate = `server = "%s"

[host."%s"]
  capabilities = ["pull", "resolve"]
`

const (
	// containerdRegistryHostsDirectory is a directory that is created by the containerd-inializer systemd service.
	// containerd is configured to read registry configuration from this directory.
	containerdRegistryHostsDirectory = "/etc/containerd/certs.d"
)

// NewEnsurer creates a new controlplane ensurer.
func NewEnsurer(client client.Client, decoder runtime.Decoder, logger logr.Logger) genericmutator.Ensurer {
	return &ensurer{
		client:  client,
		decoder: decoder,
		logger:  logger.WithName("registry-cache-ensurer"),
	}
}

type ensurer struct {
	genericmutator.NoopEnsurer
	client  client.Client
	decoder runtime.Decoder
	logger  logr.Logger
}

// EnsureAdditionalFiles ensures that the containerd registry configuration files are added to the <new> files.
func (e *ensurer) EnsureAdditionalFiles(ctx context.Context, gctx gcontext.GardenContext, new, _ *[]extensionsv1alpha1.File) error {
	cluster, err := gctx.GetCluster(ctx)
	if err != nil {
		return fmt.Errorf("failed to get the cluster resource: %w", err)
	}

	if cluster.Shoot.DeletionTimestamp != nil {
		e.logger.Info("Shoot has a deletion timestamp set, skipping the OperatingSystemConfig mutation", "shoot", client.ObjectKeyFromObject(cluster.Shoot))
		return nil
	}
	// If hibernation is enabled for Shoot, then the .status.providerStatus field of the registry-cache Extension can be missing (on Shoot creation)
	// or outdated (if for hibernated Shoot a new registry is added). Hence, we skip the OperatingSystemConfig mutation when hibernation is enabled.
	// When Shoot is waking up, then .status.providerStatus will be updated in the Extension and the OperatingSystemConfig will be mutated according to it.
	if v1beta1helper.HibernationIsEnabled(cluster.Shoot) {
		e.logger.Info("Hibernation is enabeld for Shoot, skipping the OperatingSystemConfig mutation", "shoot", client.ObjectKeyFromObject(cluster.Shoot))
		return nil
	}

	extension := &extensionsv1alpha1.Extension{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "registry-cache",
			Namespace: cluster.ObjectMeta.Name,
		},
	}
	if err := e.client.Get(ctx, client.ObjectKeyFromObject(extension), extension); err != nil {
		return fmt.Errorf("failed to get extension '%s': %w", client.ObjectKeyFromObject(extension), err)
	}

	if extension.Status.ProviderStatus == nil {
		return fmt.Errorf("extension '%s' does not have a .status.providerStatus specified", client.ObjectKeyFromObject(extension))
	}

	registryStatus := &v1alpha1.RegistryStatus{}
	if _, _, err := e.decoder.Decode(extension.Status.ProviderStatus.Raw, nil, registryStatus); err != nil {
		return fmt.Errorf("failed to decode providerStatus of extension '%s': %w", client.ObjectKeyFromObject(extension), err)
	}

	for _, cache := range registryStatus.Caches {
		upstreamURL := registryutils.GetUpstreamURL(cache.Upstream)

		appendUniqueFile(new, extensionsv1alpha1.File{
			Path:        filepath.Join(containerdRegistryHostsDirectory, cache.Upstream, "hosts.toml"),
			Permissions: pointer.Int32(0644),
			Content: extensionsv1alpha1.FileContent{
				Inline: &extensionsv1alpha1.FileContentInline{
					Data: fmt.Sprintf(hostsTOMLTemplate, upstreamURL, cache.Endpoint),
				},
			},
		})
	}

	return nil
}

// appendUniqueFile appends a unit file only if it does not exist, otherwise overwrite content of previous files
func appendUniqueFile(files *[]extensionsv1alpha1.File, file extensionsv1alpha1.File) {
	resFiles := make([]extensionsv1alpha1.File, 0, len(*files))

	for _, f := range *files {
		if f.Path != file.Path {
			resFiles = append(resFiles, f)
		}
	}

	*files = append(resFiles, file)
}