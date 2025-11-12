// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0

package spegel

import (
	"context"
	"fmt"

	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"
	"github.com/gardener/gardener/pkg/apis/core"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/gardener/gardener-extension-registry-cache/pkg/admission/validator/helper"
	spegelapi "github.com/gardener/gardener-extension-registry-cache/pkg/apis/spegel"
	"github.com/gardener/gardener-extension-registry-cache/pkg/apis/spegel/validation"
)

type shoot struct {
	decoder runtime.Decoder
}

// NewShootValidator returns a new instance of a shoot validator that validates: TODO
func NewShootValidator(decoder runtime.Decoder) extensionswebhook.Validator {
	return &shoot{
		decoder: decoder,
	}
}

func (s *shoot) Validate(_ context.Context, newObj, _ client.Object) error {
	shoot, ok := newObj.(*core.Shoot)
	if !ok {
		return fmt.Errorf("wrong object type %T", newObj)
	}

	i, spegelExt := helper.FindExtension(shoot.Spec.Extensions, "registry-spegel")
	if i == -1 {
		return nil
	}

	for _, worker := range shoot.Spec.Provider.Workers {
		if worker.CRI.Name != "containerd" {
			return fmt.Errorf("container runtime needs to be containerd when the registry-spegel extension is enabled")
		}
	}

	providerConfigPath := field.NewPath("spec", "extensions").Index(i).Child("providerConfig")
	if spegelExt.ProviderConfig == nil {
		return field.Required(providerConfigPath, "providerConfig is required for the registry-spegel extension")
	}

	spegelConfig := &spegelapi.SpegelConfig{}
	if err := runtime.DecodeInto(s.decoder, spegelExt.ProviderConfig.Raw, spegelConfig); err != nil {
		return fmt.Errorf("failed to decode providerConfig: %w", err)
	}

	allErrs := field.ErrorList{}
	allErrs = append(allErrs, validation.ValidateSpegelConfig(spegelConfig, providerConfigPath)...)

	return allErrs.ToAggregate()
}
