package validation

import (
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/gardener/gardener-extension-registry-cache/pkg/apis/spegel"
)

// ValidateSpegelConfig validates the passed configuration instance.
func ValidateSpegelConfig(spegelConfig *spegel.SpegelConfig, fldPath *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	if spegelConfig.RegistryPort == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("registryPort"), "registry port must be provided"))
	} else {
		for _, msg := range validation.IsValidPortNum(int(*spegelConfig.RegistryPort)) {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("registryPort"), *spegelConfig.RegistryPort, msg))
		}
	}
	if spegelConfig.RouterPort == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("routerPort"), "router port must be provided"))
	} else {
		for _, msg := range validation.IsValidPortNum(int(*spegelConfig.RouterPort)) {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("routerPort"), *spegelConfig.RouterPort, msg))
		}
	}
	if spegelConfig.MetricsPort == nil {
		allErrs = append(allErrs, field.Required(fldPath.Child("metricsPort"), "metrics port must be provided"))
	} else {
		for _, msg := range validation.IsValidPortNum(int(*spegelConfig.MetricsPort)) {
			allErrs = append(allErrs, field.Invalid(fldPath.Child("metricsPort"), *spegelConfig.MetricsPort, msg))
		}
	}

	return allErrs
}
