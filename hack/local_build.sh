#!/bin/bash

# Copyright 2025 Flant JSC
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

# prevent the script from being sourced
if [[ "${BASH_SOURCE[0]}" != "$0" ]]; then
  echo "ERROR: This script must not be sourced." >&2
  return 1
fi

REGISTRY_PATH=registry.flant.com/deckhouse/storage/localbuild
NAMESPACE=d8-sds-replicated-volume
DAEMONSET_NAME=agent
SECRET_NAME=sds-replicated-volume-module-registry

# CI and werf variables
export SOURCE_REPO="https://github.com"
export CI_COMMIT_REF_NAME="null"

# PAT TOKEN must be created here https://fox.flant.com/-/user_settings/personal_access_tokens
# It must have api, read_api, read_repository, read_registry, write_registry scopes
#PAT_TOKEN='REPLACEME'
# prefix of the custom tag
# the final image will look like: ${REGISTRY_PATH}:${CUSTOM_TAG}-<image_name>
#CUSTOM_TAG=username

# you can optionally define secrets in a gitignored folder:
source ./.secret/$(basename "$0") 2>/dev/null || true

if [ -z "$PAT_TOKEN" ];then
  echo "ERR: empty PAT_TOKEN"
  exit 1
fi
if [ -z "$CUSTOM_TAG" ];then
  echo "ERR: empty CUSTOM_TAG"
  exit 1
fi
if [ -z "$REGISTRY_PATH" ];then
  echo "ERR: empty REGISTRY_PATH"
  exit 1
fi
if ! command -v werf &> /dev/null; then
    echo "ERR: werf is not installed or not in PATH"
    exit 1
fi

build_action() {
  if [ $# -eq 0 ]; then
    echo "ERR: <no image names provided>"
    exit 1
  else
    IMAGE_NAMES="$@"
  fi

  echo "Get base_images.yml"

  BASE_IMAGES_VERSION=$(grep -oP 'BASE_IMAGES_VERSION:\s+"v\d+\.\d+\.\d+"' ./.github/workflows/build_dev.yml | grep -oP 'v\d+\.\d+\.\d+' | head -n1)
  if [ -z "$BASE_IMAGES_VERSION" ];then
    echo "ERR: empty BASE_IMAGES_VERSION"
    exit 1
  fi

  echo BASE_IMAGES_VERSION=$BASE_IMAGES_VERSION

  curl -OJL https://fox.flant.com/api/v4/projects/deckhouse%2Fbase-images/packages/generic/base_images/${BASE_IMAGES_VERSION}/base_images.yml

  echo "Start building for images:"

  werf cr login $REGISTRY_PATH --username='pat' --password=$PAT_TOKEN

  for image in $IMAGE_NAMES; do
    echo "Building image: $image"
    werf build $image --add-custom-tag=$CUSTOM_TAG"-"$image --repo=$REGISTRY_PATH --dev
  done

  echo "Delete base_images.yml"
  rm -rf base_images.yml
}

_create_secret() {
  echo "{\"auths\":{\"${REGISTRY_PATH}\":{\"auth\":\"$(echo -n pat:${PAT_TOKEN} | base64 -w 0)\"}}}" |  base64 -w 0
}

patch_agent() {
  (
    set -exuo pipefail

    DAEMONSET_CONTAINER_NAME=agent
    IMAGE=${REGISTRY_PATH}:${CUSTOM_TAG}-agent

    SECRET_DATA=$(_create_secret)

    kubectl -n d8-system scale deployment deckhouse --replicas=0

    kubectl -n $NAMESPACE patch secret $SECRET_NAME -p \
    "{\"data\": {\".dockerconfigjson\": \"$SECRET_DATA\"}}"

    kubectl -n $NAMESPACE patch daemonset $DAEMONSET_NAME -p \
    "{\"spec\": {\"template\": {\"spec\": {\"containers\": [{\"name\": \"$DAEMONSET_CONTAINER_NAME\", \"image\": \"$IMAGE\"}]}}}}"

    kubectl -n $NAMESPACE patch daemonset $DAEMONSET_NAME -p \
    "{\"spec\": {\"template\": {\"spec\": {\"containers\": [{\"name\": \"$DAEMONSET_CONTAINER_NAME\", \"imagePullPolicy\": \"Always\"}]}}}}"
    
    kubectl -n $NAMESPACE rollout restart daemonset $DAEMONSET_NAME
  )
}

restore_agent() {
  (
    set -exuo pipefail

    kubectl -n $NAMESPACE delete secret $SECRET_NAME

    kubectl -n $NAMESPACE delete daemonset $DAEMONSET_NAME
    
    kubectl -n d8-system scale deployment deckhouse --replicas=1
  )
}

print_help() {
  echo "  Usage: $0 build [<image_name1> [<image_name2> ...]]"
  echo "  Possible actions: build, patch_agent, build_patch_agent, restore_agent"
}

if [ $# -lt 1 ]; then
  print_help
  exit 1
fi
ACTION=$1

shift

case "$ACTION" in
  --help)
    print_help
    ;;
  build)
    build_action
    ;;
  patch_agent)
    patch_agent
    ;;
  build_patch_agent)
    build_action agent
    patch_agent
    ;;
  restore_agent)
    restore_agent
    ;;
  *)
    echo "Unknown action: $ACTION"
    print_help
    exit 1
    ;;
esac
