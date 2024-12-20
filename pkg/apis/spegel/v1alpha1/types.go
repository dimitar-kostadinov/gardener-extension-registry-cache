// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SpegelConfig contains information about the Spegel listening addresses of each Node.
type SpegelConfig struct {
	metav1.TypeMeta `json:",inline"`

	// RegistryPort is the port that serves the OCI registry on each Node.
	// Defaults to 5000.
	// +optional
	RegistryPort *uint16 `json:"registryPort,omitempty"`
	// RouterPort is the port for P2P router on each Node.
	// Defaults to 5001.
	// +optional
	RouterPort *uint16 `json:"routerPort,omitempty"`
	// MetricsPort is the metrics port on each Node.
	// Defaults to 9090.
	// +optional
	MetricsPort *uint16 `json:"metricsPort,omitempty"`

	// --resolve-tags, --log-level ?
}

/*
	"registry",
	"--log-level=DEBUG",
	"--mirror-resolve-retries=3",
	"--mirror-resolve-timeout=20ms",
	"--registry-addr=:5500",
	"--router-addr=:5501",
	"--metrics-addr=:9590",
	"--registries",
	"https://docker.io",
	"https://ghcr.io",
	"https://quay.io",
	"https://registry.k8s.io",
	"https://k8s.gcr.io",
	"https://europe-docker.pkg.dev",
	"--containerd-sock=/run/containerd/containerd.sock",
	"--containerd-namespace=k8s.io",
	"--containerd-registry-config-path=/etc/containerd/certs.d",
	"--bootstrap-kind=http",
	"--http-bootstrap-addr=:5601",
	"--http-bootstrap-peer=http://10.10.130.192:5601/id",
	"--resolve-latest-tag=true",
	"--local-addr=$(NODE_IP):5500",
	"--containerd-content-path=/var/lib/containerd/io.containerd.content.v1.content",
*/
