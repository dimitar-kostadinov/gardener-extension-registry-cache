//go:build !ignore_autogenerated
// +build !ignore_autogenerated

// SPDX-FileCopyrightText: 2024 SAP SE or an SAP affiliate company and Gardener contributors
//
// SPDX-License-Identifier: Apache-2.0
// Code generated by conversion-gen. DO NOT EDIT.

package v1alpha1

import (
	unsafe "unsafe"

	spegel "github.com/gardener/gardener-extension-registry-cache/pkg/apis/spegel"
	conversion "k8s.io/apimachinery/pkg/conversion"
	runtime "k8s.io/apimachinery/pkg/runtime"
)

func init() {
	localSchemeBuilder.Register(RegisterConversions)
}

// RegisterConversions adds conversion functions to the given scheme.
// Public to allow building arbitrary schemes.
func RegisterConversions(s *runtime.Scheme) error {
	if err := s.AddGeneratedConversionFunc((*SpegelConfig)(nil), (*spegel.SpegelConfig)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_v1alpha1_SpegelConfig_To_spegel_SpegelConfig(a.(*SpegelConfig), b.(*spegel.SpegelConfig), scope)
	}); err != nil {
		return err
	}
	if err := s.AddGeneratedConversionFunc((*spegel.SpegelConfig)(nil), (*SpegelConfig)(nil), func(a, b interface{}, scope conversion.Scope) error {
		return Convert_spegel_SpegelConfig_To_v1alpha1_SpegelConfig(a.(*spegel.SpegelConfig), b.(*SpegelConfig), scope)
	}); err != nil {
		return err
	}
	return nil
}

func autoConvert_v1alpha1_SpegelConfig_To_spegel_SpegelConfig(in *SpegelConfig, out *spegel.SpegelConfig, s conversion.Scope) error {
	out.RegistryPort = (*uint16)(unsafe.Pointer(in.RegistryPort))
	out.RouterPort = (*uint16)(unsafe.Pointer(in.RouterPort))
	out.MetricsPort = (*uint16)(unsafe.Pointer(in.MetricsPort))
	return nil
}

// Convert_v1alpha1_SpegelConfig_To_spegel_SpegelConfig is an autogenerated conversion function.
func Convert_v1alpha1_SpegelConfig_To_spegel_SpegelConfig(in *SpegelConfig, out *spegel.SpegelConfig, s conversion.Scope) error {
	return autoConvert_v1alpha1_SpegelConfig_To_spegel_SpegelConfig(in, out, s)
}

func autoConvert_spegel_SpegelConfig_To_v1alpha1_SpegelConfig(in *spegel.SpegelConfig, out *SpegelConfig, s conversion.Scope) error {
	out.RegistryPort = (*uint16)(unsafe.Pointer(in.RegistryPort))
	out.RouterPort = (*uint16)(unsafe.Pointer(in.RouterPort))
	out.MetricsPort = (*uint16)(unsafe.Pointer(in.MetricsPort))
	return nil
}

// Convert_spegel_SpegelConfig_To_v1alpha1_SpegelConfig is an autogenerated conversion function.
func Convert_spegel_SpegelConfig_To_v1alpha1_SpegelConfig(in *spegel.SpegelConfig, out *SpegelConfig, s conversion.Scope) error {
	return autoConvert_spegel_SpegelConfig_To_v1alpha1_SpegelConfig(in, out, s)
}
