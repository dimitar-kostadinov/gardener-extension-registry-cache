package validation

import (
	"github.com/gardener/gardener-extension-registry-cache/pkg/apis/spegel"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ValidateSpegelConfig validates the passed configuration instance.
func ValidateSpegelConfig(spegelConfig *spegel.SpegelConfig, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if spegelConfig.RegistryPort == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("registryPort"), "registry port must be provided"))
	}
	if spegelConfig.RouterPort == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("routerPort"), "router port must be provided"))
	}
	if spegelConfig.MetricsPort == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("metricsPort"), "metrics port must be provided"))
	}
	return allErrs
}
