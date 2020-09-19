#!/usr/bin/env bash

# Copyright 2019 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Build apimachinery executables from vendor-ed dependencies

. $(dirname "${BASH_SOURCE}")/common.sh

WORK_DIR=$(mktemp -d)

cleanup() {
  chmod -R +w "${WORK_DIR}"
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

mkdir -p "${WORK_DIR}/go/"
cp -R "${GOPATH}/src/k8s.io/kops/vendor/" "${WORK_DIR}/go/src"

unset GOBIN

env GOBIN="${WORK_DIR}/go/bin" GOPATH="${WORK_DIR}/go/" go install -mod=mod -v k8s.io/code-generator/cmd/conversion-gen/
cp "${WORK_DIR}/go/bin/conversion-gen" "${GOPATH}/bin/"

env GOBIN="${WORK_DIR}/go/bin" GOPATH="${WORK_DIR}/go/" go install -mod=mod k8s.io/code-generator/cmd/deepcopy-gen/
cp "${WORK_DIR}/go/bin/deepcopy-gen" "${GOPATH}/bin/"

env GOBIN="${WORK_DIR}/go/bin" GOPATH="${WORK_DIR}/go/" go install -mod=mod k8s.io/code-generator/cmd/defaulter-gen/
cp "${WORK_DIR}/go/bin/defaulter-gen" "${GOPATH}/bin/"

env GOBIN="${WORK_DIR}/go/bin" GOPATH="${WORK_DIR}/go/" go install -mod=mod k8s.io/code-generator/cmd/client-gen/
cp "${WORK_DIR}/go/bin/client-gen" "${GOPATH}/bin/"