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
	"fmt"
	"strconv"
	"strings"

	"github.com/gardener/gardener/pkg/utils/imagevector"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type registryCache struct {
	Name      string
	Namespace string
	Labels    map[string]string

	Upstream                 string
	VolumeSize               resource.Quantity
	GarbageCollectionEnabled bool
	UpstreamUsername         string
	UpstreamPassword         string
	RegistryImage            *imagevector.Image
}

const (
	registryCacheNamespaceName = "registry-cache"
	registryCacheInternalName  = "registry-cache"
	registryCacheVolumeName    = "cache-volume"
	registryVolumeMountPath    = "/var/lib/registry"

	environmentVariableNameRegistryURL      = "REGISTRY_PROXY_REMOTEURL"
	environmentVariableNameRegistryUsername = "REGISTRY_PROXY_USERNAME"
	environmentVariableNameRegistryPassword = "REGISTRY_PROXY_PASSWORD"
	environmentVariableNameRegistryDelete   = "REGISTRY_STORAGE_DELETE_ENABLED"
	registryCacheServiceUpstreamLabel       = "upstream-host"
)

func (c *registryCache) Ensure() ([]client.Object, error) {
	c.Name = strings.Replace(fmt.Sprintf("registry-%s", strings.Split(c.Upstream, ":")[0]), ".", "-", -1)

	if c.Labels == nil {
		c.Labels = map[string]string{
			"app": c.Name,
		}
	}

	c.Labels[registryCacheServiceUpstreamLabel] = c.Upstream

	upstreamURL := c.Upstream
	if upstreamURL == "docker.io" {
		upstreamURL = "registry-1.docker.io"
	}
	upstreamURL = fmt.Sprintf("https://%s", upstreamURL)

	envVars := []v1.EnvVar{
		{
			Name:  environmentVariableNameRegistryURL,
			Value: upstreamURL,
		},
		{
			Name:  environmentVariableNameRegistryDelete,
			Value: strconv.FormatBool(c.GarbageCollectionEnabled),
		},
	}
	// set upstream registry credentials
	if len(c.UpstreamUsername) > 0 && len(c.UpstreamPassword) > 0 {
		envVars = append(envVars,
			v1.EnvVar{
				Name:  environmentVariableNameRegistryUsername,
				Value: c.UpstreamUsername,
			},
			v1.EnvVar{
				Name: environmentVariableNameRegistryPassword,
				// value is wrapped in single quotes so that it is interpreted as strings in registry config.yaml,
				// otherwise, the registry may crash; for example password for gcr _json_key user is json and registry
				// cannot start as yaml unmarshal errors occurs (yaml is superset of json)
				Value: fmt.Sprintf("'%s'", c.UpstreamPassword),
			},
		)
	}

	var (
		service = &v1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      c.Name,
				Namespace: registryCacheNamespaceName,
				Labels:    c.Labels,
			},
			Spec: v1.ServiceSpec{
				Selector: c.Labels,
				Ports: []v1.ServicePort{{
					Name:       registryCacheInternalName,
					Port:       5000,
					Protocol:   v1.ProtocolTCP,
					TargetPort: intstr.FromString(registryCacheInternalName),
				}},
				Type: v1.ServiceTypeClusterIP,
			},
		}

		statefulSet = &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{
				Name:      c.Name,
				Namespace: registryCacheNamespaceName,
				Labels:    c.Labels,
			},
			Spec: appsv1.StatefulSetSpec{
				ServiceName: service.Name,
				Selector: &metav1.LabelSelector{
					MatchLabels: c.Labels,
				},
				Replicas: pointer.Int32(1),
				Template: v1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: c.Labels,
					},
					Spec: v1.PodSpec{
						Containers: []v1.Container{
							{
								Name:            registryCacheInternalName,
								Image:           c.RegistryImage.Repository,
								ImagePullPolicy: v1.PullIfNotPresent,
								Ports: []v1.ContainerPort{
									{
										ContainerPort: 5000,
										Name:          registryCacheInternalName,
									},
								},
								Env: envVars,
								VolumeMounts: []v1.VolumeMount{
									{
										Name:      registryCacheVolumeName,
										ReadOnly:  false,
										MountPath: registryVolumeMountPath,
									},
								},
							},
						},
					},
				},
				VolumeClaimTemplates: []v1.PersistentVolumeClaim{
					{
						ObjectMeta: metav1.ObjectMeta{
							Name:   registryCacheVolumeName,
							Labels: c.Labels,
						},
						Spec: v1.PersistentVolumeClaimSpec{
							AccessModes: []v1.PersistentVolumeAccessMode{v1.ReadWriteOnce},
							Resources: v1.ResourceRequirements{
								Requests: v1.ResourceList{
									v1.ResourceStorage: c.VolumeSize,
								},
							},
						},
					},
				},
			},
		}
	)

	return []client.Object{
		service,
		statefulSet,
	}, nil
}
