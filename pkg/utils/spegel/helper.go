// SPDX-FileCopyrightText: SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package spegel

import (
	"fmt"
	"regexp"

	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
)

// technicalIDPattern addresses the ambiguity that one or two dashes could follow the prefix "shoot" in the technical ID of the shoot.
var technicalIDPattern = regexp.MustCompile(fmt.Sprintf("^%s-?", v1beta1constants.TechnicalIDPrefix))

// ComputeIngressHost computes the host for a given prefix.
func ComputeIngressHost(technicalID, ingressDomain string) string {
	shortID := technicalIDPattern.ReplaceAllString(technicalID, "")
	return fmt.Sprintf("%s-%s.%s", "sp", shortID, ingressDomain)
}
