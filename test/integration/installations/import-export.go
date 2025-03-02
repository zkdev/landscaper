// SPDX-FileCopyrightText: 2021 SAP SE or an SAP affiliate company and Gardener contributors.
//
// SPDX-License-Identifier: Apache-2.0

package installations

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	k8sv1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	lsv1alpha1helper "github.com/gardener/landscaper/apis/core/v1alpha1/helper"

	lsv1alpha1 "github.com/gardener/landscaper/apis/core/v1alpha1"
	kutil "github.com/gardener/landscaper/controller-utils/pkg/kubernetes"
	"github.com/gardener/landscaper/test/framework"
	"github.com/gardener/landscaper/test/utils"
)

func ImportExportTests(f *framework.Framework) {
	var (
		testdataDir = filepath.Join(f.RootPath, "test", "integration", "installations", "testdata", "test1")
	)

	Describe("Imports/Exports", func() {

		var (
			state = f.Register()
			ctx   context.Context
		)

		BeforeEach(func() {
			ctx = context.Background()
		})

		AfterEach(func() {
			ctx.Done()
		})

		It("should pass imports/exports correctly to/from subinstallations", func() {
			By("Create secrets and targets")
			// dummy secret
			secret := &k8sv1.Secret{}
			utils.ExpectNoError(utils.ReadResourceFromFile(secret, path.Join(testdataDir, "10-dummy-secret.yaml")))
			secret.SetNamespace(state.Namespace)
			utils.ExpectNoError(state.Create(ctx, secret))
			expectedDataExport := string(secret.Data["value"])
			expectedDataMappingExport := "mapping-" + expectedDataExport
			// dummy target
			target := &lsv1alpha1.Target{}
			utils.ExpectNoError(utils.ReadResourceFromFile(target, path.Join(testdataDir, "10-dummy-target.yaml")))
			target.SetNamespace(state.Namespace)
			utils.ExpectNoError(state.Create(ctx, target))
			expectedTargetExport := target.Spec

			By("Create root installation")
			root := &lsv1alpha1.Installation{}
			utils.ExpectNoError(utils.ReadResourceFromFile(root, path.Join(testdataDir, "00-root-installation.yaml")))
			root.SetNamespace(state.Namespace)
			lsv1alpha1helper.SetOperation(&root.ObjectMeta, lsv1alpha1.ReconcileOperation)
			utils.ExpectNoError(state.Create(ctx, root))

			By("verify that subinstallation has been created")
			subinst := &lsv1alpha1.Installation{}
			Eventually(func() (bool, error) {
				err := f.Client.Get(ctx, kutil.ObjectKeyFromObject(root), root)
				if err != nil || len(root.Status.InstallationReferences) == 0 {
					return false, err
				}
				err = f.Client.Get(ctx, root.Status.InstallationReferences[0].Reference.NamespacedName(), subinst)
				if err != nil {
					return false, err
				}
				return true, nil
			}, timeoutTime, resyncTime).Should(BeTrue(), "unable to fetch subinstallation")

			By("verify that installations are succeeded")
			Eventually(func() (lsv1alpha1.InstallationPhase, error) {
				err := f.Client.Get(ctx, kutil.ObjectKeyFromObject(root), root)
				if err != nil {
					return "", err
				}
				return root.Status.InstallationPhase, nil
			}, timeoutTime, resyncTime).Should(BeEquivalentTo(lsv1alpha1.InstallationPhases.Succeeded), "root installation should be in phase '%s'", lsv1alpha1.InstallationPhases.Succeeded)
			Eventually(func() (lsv1alpha1.InstallationPhase, error) {
				err := f.Client.Get(ctx, kutil.ObjectKeyFromObject(subinst), subinst)
				if err != nil {
					return "", err
				}
				return subinst.Status.InstallationPhase, nil
			}, timeoutTime, resyncTime).Should(BeEquivalentTo(lsv1alpha1.InstallationPhases.Succeeded), "subinstallation should be in phase '%s'", lsv1alpha1.InstallationPhases.Succeeded)

			labels := map[string]string{
				lsv1alpha1.DataObjectSourceTypeLabel: "export",
				lsv1alpha1.DataObjectSourceLabel:     fmt.Sprintf("Inst.%s", root.Name),
			}

			// data export
			By("verify data exports")
			rawDOExports := &lsv1alpha1.DataObjectList{}
			utils.ExpectNoError(f.Client.List(ctx, rawDOExports, client.InNamespace(state.Namespace), client.MatchingLabels(labels)))
			// remove entries which have non-empty context labels
			doExports := []lsv1alpha1.DataObject{}
			for _, elem := range rawDOExports.Items {
				con, ok := elem.Labels[lsv1alpha1.DataObjectContextLabel]
				if !ok || len(con) == 0 {
					doExports = append(doExports, elem)
				}
			}
			Expect(doExports).To(HaveLen(2), "there should be exactly two root-level dataobject exports")
			Expect(doExports).To(ConsistOf(
				MatchFields(IgnoreExtras, Fields{
					"Data": WithTransform(func(aj lsv1alpha1.AnyJSON) interface{} {
						var res interface{}
						err := json.Unmarshal(aj.RawMessage, &res)
						if err != nil {
							return nil
						}
						return res
					}, BeEquivalentTo(expectedDataExport)),
				}),
				MatchFields(IgnoreExtras, Fields{
					"Data": WithTransform(func(aj lsv1alpha1.AnyJSON) interface{} {
						var res interface{}
						err := json.Unmarshal(aj.RawMessage, &res)
						if err != nil {
							return nil
						}
						return res
					}, BeEquivalentTo(expectedDataMappingExport)),
				}),
			))

			// target exports
			By("verify target exports")
			labels[lsv1alpha1.DataObjectKeyLabel] = "targetExp"
			rawTargetExports := &lsv1alpha1.TargetList{}
			utils.ExpectNoError(f.Client.List(ctx, rawTargetExports, client.InNamespace(state.Namespace), client.MatchingLabels(labels)))
			targetExports := []lsv1alpha1.Target{}
			for _, elem := range rawTargetExports.Items {
				con, ok := elem.Labels[lsv1alpha1.DataObjectContextLabel]
				if !ok || len(con) == 0 {
					targetExports = append(targetExports, elem)
				}
			}
			Expect(targetExports).To(HaveLen(1), "there should be exactly one root-level target export for targetExp")
			Expect(targetExports).To(ContainElement(MatchFields(IgnoreExtras, Fields{
				"Spec": BeEquivalentTo(expectedTargetExport),
			})))

			// target export from list import
			labels[lsv1alpha1.DataObjectKeyLabel] = "targetExpFromList"
			rawTargetExports = &lsv1alpha1.TargetList{}
			utils.ExpectNoError(f.Client.List(ctx, rawTargetExports, client.InNamespace(state.Namespace), client.MatchingLabels(labels)))
			targetExports = []lsv1alpha1.Target{}
			for _, elem := range rawTargetExports.Items {
				con, ok := elem.Labels[lsv1alpha1.DataObjectContextLabel]
				if !ok || len(con) == 0 {
					targetExports = append(targetExports, elem)
				}
			}
			Expect(targetExports).To(HaveLen(1), "there should be exactly one root-level target export for targetExpFromList")
			Expect(targetExports).To(ContainElement(MatchFields(IgnoreExtras, Fields{
				"Spec": BeEquivalentTo(expectedTargetExport),
			})))

			// targetlist import
			// targetlists cannot be exported, so check for successful import in subinstallation instead
			By("verify targetlist imports")
			labels = map[string]string{
				lsv1alpha1.DataObjectKeyLabel:        "subTargetListImp",
				lsv1alpha1.DataObjectSourceTypeLabel: "import",
				lsv1alpha1.DataObjectSourceLabel:     fmt.Sprintf("Inst.%s", subinst.Name),
				lsv1alpha1.DataObjectContextLabel:    fmt.Sprintf("Inst.%s", subinst.Name),
			}
			tlImport := &lsv1alpha1.TargetList{}
			utils.ExpectNoError(f.Client.List(ctx, tlImport, client.InNamespace(state.Namespace), client.MatchingLabels(labels)))
			Expect(tlImport.Items).To(HaveLen(3))
			for _, elem := range tlImport.Items {
				Expect(elem).To(MatchFields(IgnoreExtras, Fields{
					"Spec": BeEquivalentTo(expectedTargetExport),
				}))
			}

			// empty targetlist import
			By("verify empty targetlist import")
			labels = map[string]string{
				lsv1alpha1.DataObjectKeyLabel:        "subEmptyTargetListImp",
				lsv1alpha1.DataObjectSourceTypeLabel: "import",
				lsv1alpha1.DataObjectSourceLabel:     fmt.Sprintf("Inst.%s", subinst.Name),
				lsv1alpha1.DataObjectContextLabel:    fmt.Sprintf("Inst.%s", subinst.Name),
			}
			tlImport = &lsv1alpha1.TargetList{}
			utils.ExpectNoError(f.Client.List(ctx, tlImport, client.InNamespace(state.Namespace), client.MatchingLabels(labels)))
			Expect(tlImport.Items).To(HaveLen(0))
		})
	})
}
