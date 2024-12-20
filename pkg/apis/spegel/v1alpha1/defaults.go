// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import "k8s.io/utils/ptr"

// SetDefaults_SpegelConfig sets the defaults ports.
func SetDefaults_SpegelConfig(spegelConfig *SpegelConfig) {
	if spegelConfig.RegistryPort == nil {
		spegelConfig.RegistryPort = ptr.To[uint16](5000)
	}
	if spegelConfig.RouterPort == nil {
		spegelConfig.RouterPort = ptr.To[uint16](5001)
	}
	if spegelConfig.MetricsPort == nil {
		spegelConfig.MetricsPort = ptr.To[uint16](9090)
	}
}
