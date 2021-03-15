// SPDX-FileCopyrightText: 2020 SAP SE or an SAP affiliate company and Gardener contributors.
//
// SPDX-License-Identifier: Apache-2.0

package manifest

import (
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	lsv1alpha1 "github.com/gardener/landscaper/apis/core/v1alpha1"
	manifestv1alpha1 "github.com/gardener/landscaper/apis/deployer/manifest/v1alpha1"
)

// AddControllerToManager adds a new manifest deployer to a controller manager.
func AddControllerToManager(mgr manager.Manager, config *manifestv1alpha1.Configuration) error {
	deployer, err := NewController(
		ctrl.Log.WithName("controllers").WithName("ManifestDeployer"),
		mgr.GetClient(),
		mgr.GetScheme(),
		config,
	)
	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&lsv1alpha1.DeployItem{}).
		Complete(deployer)
}
