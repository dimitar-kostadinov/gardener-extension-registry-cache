// Copyright (c) 2021 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
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

package controlplane

import (
	"context"
	"fmt"

	"github.com/gardener/gardener/extensions/pkg/apis/config"
	"github.com/gardener/gardener/extensions/pkg/util"
	gcontext "github.com/gardener/gardener/extensions/pkg/webhook/context"
	"github.com/gardener/gardener/extensions/pkg/webhook/controlplane/genericmutator"
	"github.com/gardener/gardener/pkg/apis/core/v1beta1"
	extensionsv1alpha1 "github.com/gardener/gardener/pkg/apis/extensions/v1alpha1"
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewEnsurer creates a new controlplane ensurer.
func NewEnsurer(logger logr.Logger) genericmutator.Ensurer {
	return &ensurer{
		logger: logger.WithName("registry-cache-ensurer"),
	}
}

type ensurer struct {
	genericmutator.NoopEnsurer
	logger logr.Logger
	client client.Client
}

// InjectClient injects the given client into the ensurer.
func (e *ensurer) InjectClient(client client.Client) error {
	e.client = client
	return nil
}

// EnsureAdditionalFiles ensures that additional required system files are added.
func (e *ensurer) EnsureAdditionalFiles(ctx context.Context, gctx gcontext.GardenContext, new, _ *[]extensionsv1alpha1.File) error {
	cluster, err := gctx.GetCluster(ctx)
	if err != nil {
		return err
	}

	ex := &extensionsv1alpha1.Extension{}
	if err := e.client.Get(ctx, kutil.Key(cluster.ObjectMeta.Name, "registry-cache"), ex); err != nil { //TODO
		logger.Error(err, "could not read extension from shoot namespace", "cluster name", cluster.ObjectMeta.Name)
		return err
	}

	if ex.Status.LastOperation == nil || ex.Status.LastOperation.State != v1beta1.LastOperationStateSucceeded {
		return fmt.Errorf("registry extension has not yet succeeded")
	}

	_, shootClient, err := util.NewClientForShoot(ctx, e.client, cluster.ObjectMeta.Name, client.Options{}, config.RESTOptions{})
	if err != nil {
		return err
	}

	selector := labels.NewSelector()
	r, err := labels.NewRequirement("upstream-host", selection.Exists, []string{}) //TODO
	if err != nil {
		return err
	}
	selector = selector.Add(*r)

	services := &corev1.ServiceList{}
	if err := shootClient.List(ctx, services, &client.ListOptions{
		LabelSelector: selector,
	}); err != nil {
		logger.Error(err, "could not read extension from shoot namespace", "cluster name", cluster.ObjectMeta.Name)
		return err
	}

	if len(services.Items) == 0 {
		logger.Info("no registry cache services found", "cluster name", cluster.ObjectMeta.Name)
		return err
	}

	for _, svc := range services.Items {
		mirrorHost, ok := svc.Labels["upstream-host"]
		if !ok {
			return fmt.Errorf("service is missing mirror-host annotation")
		}

		dirname := mirrorHost

		if mirrorHost == "docker.io" {
			mirrorHost = "registry-1.docker.io"
		}

		appendUniqueFile(new, extensionsv1alpha1.File{
			Path:        fmt.Sprintf("/etc/containerd/certs.d/%s/hosts.toml", dirname),
			Permissions: pointer.Int32(0644),
			Content: extensionsv1alpha1.FileContent{
				Inline: &extensionsv1alpha1.FileContentInline{
					Encoding: "",
					Data:     fmt.Sprintf("server=\"https://%s\"\n[host.\"http://%s:%d\"]\n  capabilities = [\"pull\", \"resolve\"]\n", mirrorHost, svc.Spec.ClusterIP, svc.Spec.Ports[0].Port),
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
