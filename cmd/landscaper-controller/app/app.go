// SPDX-FileCopyrightText: 2020 SAP SE or an SAP affiliate company and Gardener contributors.
//
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"os"

	"github.com/gardener/landscaper/controller-utils/pkg/logging"
	lc "github.com/gardener/landscaper/controller-utils/pkg/logging/constants"
	lsutils "github.com/gardener/landscaper/pkg/utils"

	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

	"github.com/mandelsoft/vfs/pkg/osfs"
	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/selection"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/gardener/landscaper/pkg/metrics"

	"github.com/gardener/landscaper/pkg/landscaper/blueprints"

	lsv1alpha1 "github.com/gardener/landscaper/apis/core/v1alpha1"
	contextctrl "github.com/gardener/landscaper/pkg/landscaper/controllers/context"
	"github.com/gardener/landscaper/pkg/landscaper/controllers/healthcheck"

	"github.com/gardener/landscaper/pkg/agent"

	deployers "github.com/gardener/landscaper/pkg/deployermanagement/controller"

	install "github.com/gardener/landscaper/apis/core/install"
	deployitemctrl "github.com/gardener/landscaper/pkg/landscaper/controllers/deployitem"
	executionactrl "github.com/gardener/landscaper/pkg/landscaper/controllers/execution"
	"github.com/gardener/landscaper/pkg/version"

	controllerruntimeMetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	installationsctrl "github.com/gardener/landscaper/pkg/landscaper/controllers/installations"
	"github.com/gardener/landscaper/pkg/landscaper/controllers/targetsync"

	"github.com/gardener/landscaper/pkg/landscaper/crdmanager"
)

// NewLandscaperControllerCommand creates a new landscaper command that runs the landscaper controller.
func NewLandscaperControllerCommand(ctx context.Context) *cobra.Command {
	options := NewOptions()

	cmd := &cobra.Command{
		Use:   "landscaper-controller",
		Short: "Landscaper controller manages the orchestration of components",

		Run: func(cmd *cobra.Command, args []string) {
			if err := options.Complete(ctx); err != nil {
				fmt.Print(err)
				os.Exit(1)
			}
			if err := options.run(ctx); err != nil {
				options.Log.Error(err, "unable to run landscaper controller")
				os.Exit(1)
			}
		},
	}

	options.AddFlags(cmd.Flags())

	return cmd
}

func (o *Options) run(ctx context.Context) error {
	setupLogger := o.Log.WithName("setup")
	setupLogger.Info("Starting Landscaper Controller", lc.KeyVersion, version.Get().String())

	configBytes, err := yaml.Marshal(o.Config)
	if err != nil {
		return fmt.Errorf("unable to marshal Landscaper config: %w", err)
	}
	_, _ = fmt.Fprintln(os.Stderr, string(configBytes))

	opts := manager.Options{
		LeaderElection:     false,
		Port:               9443,
		MetricsBindAddress: "0",
		NewClient:          lsutils.NewUncachedClient,
	}

	if o.Config.Controllers.SyncPeriod != nil {
		opts.SyncPeriod = &o.Config.Controllers.SyncPeriod.Duration
	}

	if o.Config.Metrics != nil {
		opts.MetricsBindAddress = fmt.Sprintf(":%d", o.Config.Metrics.Port)
	}

	hostMgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), opts)
	if err != nil {
		return fmt.Errorf("unable to setup manager: %w", err)
	}

	lsMgr := hostMgr
	if len(o.landscaperKubeconfigPath) > 0 {
		data, err := os.ReadFile(o.landscaperKubeconfigPath)
		if err != nil {
			return fmt.Errorf("unable to read landscaper kubeconfig from %s: %w", o.landscaperKubeconfigPath, err)
		}
		client, err := clientcmd.NewClientConfigFromBytes(data)
		if err != nil {
			return fmt.Errorf("unable to build landscaper cluster client from %s: %w", o.landscaperKubeconfigPath, err)
		}
		lsConfig, err := client.ClientConfig()
		if err != nil {
			return fmt.Errorf("unable to build landscaper cluster rest client from %s: %w", o.landscaperKubeconfigPath, err)
		}
		opts.MetricsBindAddress = "0"
		lsMgr, err = ctrl.NewManager(lsConfig, opts)
		if err != nil {
			return fmt.Errorf("unable to setup landscaper cluster manager from %s: %w", o.landscaperKubeconfigPath, err)
		}
	}

	metrics.RegisterMetrics(controllerruntimeMetrics.Registry)

	store, err := blueprints.NewStore(o.Log.WithName("blueprintStore"), osfs.New(), o.Config.BlueprintStore)
	if err != nil {
		return fmt.Errorf("unable to setup blueprint store: %w", err)
	}
	blueprints.SetStore(store)

	if err := o.ensureCRDs(ctx, lsMgr); err != nil {
		return err
	}

	if lsMgr != hostMgr {
		if err := o.ensureCRDs(ctx, hostMgr); err != nil {
			return err
		}
	}

	install.Install(lsMgr.GetScheme())

	ctrlLogger := o.Log.WithName("controllers")
	if err := installationsctrl.AddControllerToManager(ctrlLogger, lsMgr, o.Config); err != nil {
		return fmt.Errorf("unable to setup installation controller: %w", err)
	}

	if err := executionactrl.AddControllerToManager(ctrlLogger, lsMgr, o.Config.Controllers.Executions); err != nil {
		return fmt.Errorf("unable to setup execution controller: %w", err)
	}

	if err := deployitemctrl.AddControllerToManager(ctrlLogger,
		lsMgr,
		o.Config.Controllers.DeployItems,
		o.Config.DeployItemTimeouts.Pickup,
		o.Config.DeployItemTimeouts.ProgressingDefault); err != nil {
		return fmt.Errorf("unable to setup deployitem controller: %w", err)
	}

	if err := contextctrl.AddControllerToManager(ctrlLogger, lsMgr, o.Config); err != nil {
		return fmt.Errorf("unable to setup context controller: %w", err)
	}

	if !o.Config.DeployerManagement.Disable {
		if err := deployers.AddControllersToManager(ctrlLogger, lsMgr, o.Config); err != nil {
			return fmt.Errorf("unable to setup deployer controllers: %w", err)
		}
		if !o.Config.DeployerManagement.Agent.Disable {
			agentConfig := o.Config.DeployerManagement.Agent.AgentConfiguration
			// add default selector and in addition reconcile all target that do not have a a environment definition
			agentConfig.TargetSelectors = append(agent.DefaultTargetSelector(agentConfig.Name),
				lsv1alpha1.TargetSelector{
					Annotations: []lsv1alpha1.Requirement{
						{
							Key:      lsv1alpha1.DeployerEnvironmentTargetAnnotationName,
							Operator: selection.DoesNotExist,
						},
					},
				},
			)
			if err := agent.AddToManager(ctx, o.Log, lsMgr, hostMgr, agentConfig); err != nil {
				return fmt.Errorf("unable to setup default agent: %w", err)
			}
		}
		if err := o.DeployInternalDeployers(ctx, lsMgr); err != nil {
			return err
		}

		if err := healthcheck.AddControllersToManager(ctx, ctrlLogger, hostMgr,
			&o.Config.DeployerManagement.Agent.AgentConfiguration, o.Config.LsDeployments, o.Deployer.EnabledDeployers); err != nil {
			return fmt.Errorf("unable to register health check controller: %w", err)
		}
	}

	if err := targetsync.AddControllerToManagerForTargetSyncs(ctrlLogger, lsMgr); err != nil {
		return fmt.Errorf("unable to register target sync controller: %w", err)
	}

	setupLogger.Info("starting the controllers")
	eg, ctx := errgroup.WithContext(ctx)

	if lsMgr != hostMgr {
		eg.Go(func() error {
			if err := hostMgr.Start(ctx); err != nil {
				return fmt.Errorf("error while running host manager: %w", err)
			}
			return nil
		})
		setupLogger.Info("Waiting for host cluster cache to sync")
		if !hostMgr.GetCache().WaitForCacheSync(ctx) {
			return fmt.Errorf("unable to sync host cluster cache")
		}
		setupLogger.Info("Cache of host cluster successfully synced")
	}
	eg.Go(func() error {
		if err := lsMgr.Start(ctx); err != nil {
			return fmt.Errorf("error while running landscaper manager: %w", err)
		}
		return nil
	})
	return eg.Wait()
}

// DeployInternalDeployers automatically deploys configured deployers using the new Deployer registrations.
func (o *Options) DeployInternalDeployers(ctx context.Context, mgr manager.Manager) error {
	directClient, err := client.New(mgr.GetConfig(), client.Options{
		Scheme: mgr.GetScheme(),
	})
	if err != nil {
		return fmt.Errorf("unable to create direct client: %q", err)
	}
	ctx = logging.NewContext(ctx, logging.Wrap(ctrl.Log.WithName("deployerManagement")))
	return o.Deployer.DeployInternalDeployers(ctx, directClient, o.Config)
}

func (o *Options) ensureCRDs(ctx context.Context, mgr manager.Manager) error {
	ctx = logging.NewContext(ctx, logging.Wrap(ctrl.Log.WithName("crdManager")))
	crdmgr, err := crdmanager.NewCrdManager(mgr, o.Config)
	if err != nil {
		return fmt.Errorf("unable to setup CRD manager: %w", err)
	}

	if err := crdmgr.EnsureCRDs(ctx); err != nil {
		return fmt.Errorf("failed to handle CRDs: %w", err)
	}

	return nil
}
