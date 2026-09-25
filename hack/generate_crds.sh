#!/bin/bash

# Copyright 2026 Flant JSC
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

# Regenerates crds/ from the Go types in api/.
#
# controller-gen renders the OpenAPI schema from the kubebuilder markers;
# crd-enricher then folds in what controller-gen cannot emit -- the x-doc-*
# documentation fields, description overrides for vendored subtrees such as
# metav1.Condition, and the CRD-level normalisations -- from the +crd-enricher:
# markers sitting next to the same structs. Both read the same packages, so they
# always run as a pair, enricher last.
#
# The tools are pinned in hack/tools/go.mod and built into a temporary directory:
# keeping them out of api/go.mod keeps controller-tools' own dependency pins
# (k8s.io/apiextensions-apiserver, k8s.io/code-generator) out of the build graph
# of everything that imports api/.
#
# The name and location of this script are a contract: the shared
# .crds_generated_check CI job (modules-gitlab-ci, templates/Go_Checks.gitlab-ci.yml)
# runs hack/generate_crds.sh and fails when the result differs from what is
# committed.

set -euo pipefail

root_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# Resolved against the working directory, which has to sit inside the module that
# holds the API types. controller-gen takes a semicolon-separated list.
api_paths="./v1alpha1"

tools_dir=$(mktemp -d)
# Registered before the second mktemp so a failure there does not leak the first
# directory. render_dir is unset at that point, hence the :- default under set -u.
trap 'rm -rf "${tools_dir}" "${render_dir:-}"' EXIT
# Rendering happens in its own directory rather than straight into crds/,
# because crd-enricher is pointed at a directory and rewrites every CRD it finds
# there. crds/ also holds the hand-maintained doc-ru-*.yaml overlays, and where
# those are written as full manifests (with apiVersion/kind/metadata) the
# enricher accepts them as CRDs and re-encodes them -- reflowing a translation
# file on every regeneration. Only the rendered manifests are copied back.
render_dir=$(mktemp -d)

# The generators are built from the pins here and then read api/ through
# go/packages, so the tools module must not declare an older language version than
# the API module: a binary built with the older toolchain refuses to load the newer
# package ("package requires newer Go version go1.26"). This does not reproduce
# locally, where GOTOOLCHAIN=auto silently upgrades the toolchain; the CI runner
# honours the directive literally, so the check has to live here.
# Read straight from the files rather than asking the go command: in a workspace
# `go list -m` reports every main module, so it would answer with several lines.
api_lang=$(awk '$1 == "go" { print $2; exit }' "${root_dir}/api/go.mod" |
    cut -d. -f1,2)
tools_lang=$(awk '$1 == "go" { print $2; exit }' "${root_dir}/hack/tools/go.mod" |
    cut -d. -f1,2)
oldest=$(printf '%s\n%s\n' "${api_lang}" "${tools_lang}" | sort -V | head -n 1)
if [ "${oldest}" != "${api_lang}" ]; then
    echo "error: hack/tools/go.mod declares go ${tools_lang}, older than" \
         "api/go.mod's go ${api_lang}; the generators would refuse to load" \
         "the API package" >&2
    exit 1
fi

# Record which generator produced the manifests. crds/ is a generated artifact,
# so its content depends on these versions; printing them puts that in the CI log
# next to the result instead of leaving it to be reconstructed from go.mod.
# GOWORK=off applies to the tools module only. Where the repository has a
# go.work, hack/tools is deliberately not one of its members -- it exists to keep
# the generators out of the production module graph -- and in workspace mode the
# go command resolves against the workspace instead, so it does not see the pins
# here at all ("no required module provides package ...").
echo "Generator versions:"
GOWORK=off go -C "${root_dir}/hack/tools" list -m \
    sigs.k8s.io/controller-tools \
    github.com/deckhouse/deckhouse/pkg/crd-enricher | sed 's/^/  /'

echo "Building the code-generation tools into ${tools_dir}"
GOWORK=off go -C "${root_dir}/hack/tools" build -o "${tools_dir}" \
    sigs.k8s.io/controller-tools/cmd/controller-gen \
    github.com/deckhouse/deckhouse/pkg/crd-enricher/cmd/crd-enricher

cd "${root_dir}/api"

echo "Rendering the CRDs"
"${tools_dir}/controller-gen" crd:crdVersions=v1 \
    output:crd:dir="${render_dir}" \
    paths="${api_paths}"

echo "Enriching the CRDs"
# strict turns every enricher warning into a failure. A warning means a marker
# was not applied as written -- a mistyped key, an unset: whose target is gone,
# a value that parsed as a map -- and without strict the manifest is written
# anyway, so the drift gate would pass on a render that lost the marker.
"${tools_dir}/crd-enricher" paths="${api_paths//;/,}" crds="${render_dir}" strict

# A kind that disappears from the Go types leaves its manifest behind here; that
# is deliberate, since crds/ is not ours alone to prune -- the doc-ru-*.yaml
# overlays live in it too. Removing a retired CRD stays a manual, reviewed step.

# Without nullglob an empty render puts the literal glob into the loop, and the
# failure surfaces as cp(1) not finding "*.yaml" -- which says nothing about the
# real cause, markers detached from their root type being the usual one.
shopt -s nullglob
rendered_files=("${render_dir}"/*.yaml)
if [ ${#rendered_files[@]} -eq 0 ]; then
    echo "error: controller-gen rendered no CRDs; check the root-type markers in api/" >&2
    exit 1
fi

echo "Installing the CRDs into crds/"
for rendered in "${rendered_files[@]}"; do
    cp "${rendered}" "${root_dir}/crds/$(basename "${rendered}")"
done
