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

package app

import (
	"os"

	controllercmd "github.com/gardener/gardener/extensions/pkg/controller/cmd"
	"github.com/gardener/gardener/extensions/pkg/controller/controlplane/genericactuator"
	heartbeatcmd "github.com/gardener/gardener/extensions/pkg/controller/heartbeat/cmd"
	webhookcmd "github.com/gardener/gardener/extensions/pkg/webhook/cmd"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	registrycmd "github.com/gardener/gardener-extension-registry-cache/pkg/cmd"
	registryservicecmd "github.com/gardener/gardener-extension-registry-cache/pkg/cmd"
	"github.com/gardener/gardener-extension-registry-cache/pkg/controller"
)

// ExtensionName is the name of the extension.
const ExtensionName = "extension-registry-cache"

// Options holds configuration passed to the registry service controller.
type Options struct {
	generalOptions     *controllercmd.GeneralOptions
	registryOptions    *registrycmd.RegistryOptions
	restOptions        *controllercmd.RESTOptions
	managerOptions     *controllercmd.ManagerOptions
	controllerOptions  *controllercmd.ControllerOptions
	healthOptions      *controllercmd.ControllerOptions
	heartbeatOptions   *heartbeatcmd.Options
	controllerSwitches *controllercmd.SwitchOptions
	reconcileOptions   *controllercmd.ReconcilerOptions
	optionAggregator   controllercmd.OptionAggregator
	webhookOptions     *webhookcmd.AddToManagerOptions
}

// NewOptions creates a new Options instance.
func NewOptions() *Options {
	webhookSwitchOptions := registryservicecmd.WebhookSwitchOptions()

	// options for the webhook server
	webhookServerOptions := &webhookcmd.ServerOptions{
		Namespace: os.Getenv("WEBHOOK_CONFIG_NAMESPACE"),
	}

	webhookOptions := webhookcmd.NewAddToManagerOptions(
		"registry-cache",
		genericactuator.ShootWebhooksResourceName,
		genericactuator.ShootWebhookNamespaceSelector(controller.Type),
		webhookServerOptions,
		webhookSwitchOptions,
	)

	options := &Options{
		generalOptions:  &controllercmd.GeneralOptions{},
		registryOptions: &registrycmd.RegistryOptions{},
		restOptions:     &controllercmd.RESTOptions{},
		managerOptions: &controllercmd.ManagerOptions{
			// These are default values.
			LeaderElection:             true,
			LeaderElectionID:           controllercmd.LeaderElectionNameID(ExtensionName),
			LeaderElectionResourceLock: resourcelock.LeasesResourceLock,
			LeaderElectionNamespace:    os.Getenv("LEADER_ELECTION_NAMESPACE"),
		},
		controllerOptions: &controllercmd.ControllerOptions{
			// This is a default value.
			MaxConcurrentReconciles: 5,
		},
		healthOptions: &controllercmd.ControllerOptions{
			// This is a default value.
			MaxConcurrentReconciles: 5,
		},
		heartbeatOptions: &heartbeatcmd.Options{
			// This is a default value.
			ExtensionName:        ExtensionName,
			RenewIntervalSeconds: 30,
			Namespace:            os.Getenv("LEADER_ELECTION_NAMESPACE"),
		},
		controllerSwitches: registrycmd.ControllerSwitches(),
		reconcileOptions:   &controllercmd.ReconcilerOptions{},
		webhookOptions:     webhookOptions,
	}

	options.optionAggregator = controllercmd.NewOptionAggregator(
		options.generalOptions,
		options.restOptions,
		options.managerOptions,
		options.controllerOptions,
		options.registryOptions,
		controllercmd.PrefixOption("healthcheck-", options.healthOptions),
		controllercmd.PrefixOption("heartbeat-", options.heartbeatOptions),
		options.controllerSwitches,
		options.reconcileOptions,
		options.webhookOptions,
	)

	return options
}
