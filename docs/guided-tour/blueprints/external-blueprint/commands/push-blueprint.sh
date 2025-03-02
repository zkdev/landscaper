#!/bin/bash
#
# Copyright (c) 2022 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

COMPONENT_DIR="$(dirname $0)/.."
BLUEPRINT_DIR="${COMPONENT_DIR}/blueprint"
OCI_ARTIFACT_REF="eu.gcr.io/gardener-project/landscaper/examples/blueprints/guided-tour/external-blueprint:1.0.0"

landscaper-cli blueprints push "${OCI_ARTIFACT_REF}" "${BLUEPRINT_DIR}"
