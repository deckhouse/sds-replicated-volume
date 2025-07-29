#!/bin/bash

cd ..

# PAT TOKEN must be created here https://fox.flant.com/-/user_settings/personal_access_tokens
# It must have api, read_api, read_repository, read_registry, write_registry scopes
#PAT_TOKEN='REPLACEME'
# prefix of the custom tag
# the final image will look like: ${REGISTRY_PATH}:${CUSTOM_TAG}-<image_name>
#CUSTOM_TAG=username
REGISTRY_PATH=registry.flant.com/deckhouse/storage/localbuild
if [ -z "$PAT_TOKEN" ];then
  echo "ERR: empty PAT_TOKEN"
  exit 1
fi
if [ -z "$CUSTOM_TAG" ];then
  echo "ERR: empty CUSTOM_TAG"
  exit 1
fi

# CI and werf variables
export SOURCE_REPO="https://github.com"
export CI_COMMIT_REF_NAME="null"

print_help() {
  echo "  Usage: $0 build|build_dev [<image_name1> [<image_name2> ...]]"
  echo "  Possible actions: build, build_dev, enable_deckhouse, disable_deckhouse, werf_install, create_secret create_script_for_patch_agent"
}

build_action() {
  #{ which werf | grep -qsE "^${HOME}/.trdl/"; } && werf_install

  echo "Get base_images.yml"
  _base_images get

  echo "Start building for images:"
  if [ -z "$IMAGE_NAMES" ]; then
    echo "ERR: <no image names provided>"
  else
    werf cr login $REGISTRY_PATH --username='pat' --password=$PAT_TOKEN
    for image in $IMAGE_NAMES; do
      echo "Building image: $image"
      werf build $image --add-custom-tag=$CUSTOM_TAG"-"$image --repo=$REGISTRY_PATH $1
    done
  fi

  echo "Delete base_images.yml"
  _base_images delete
}

build_dev_action() {
  build_action --dev
}

disable_deckhouse() {
  kubectl -n d8-system scale deployment/deckhouse --replicas 0
}

enable_deckhouse() {
  kubectl -n d8-system scale deployment/deckhouse --replicas 1
}

werf_install() {
  curl -sSL https://werf.io/install.sh | bash -s -- --version 2 --channel stable
}

create_secret() {
  echo "{\"auths\":{\"${REGISTRY_PATH}\":{\"auth\":\"$(echo -n pat:${PAT_TOKEN} | base64 -w 0)\"}}}" |  base64 -w 0
}

create_script_for_patch_agent() {
cat << EOF | tee /dev/null
#!/bin/bash
set -exuo pipefail
NAMESPACE=d8-sds-replicated-volume

DAEMONSET_NAME=sds-replicated-volume-agent
DAEMONSET_CONTAINER_NAME=sds-replicated-volume-agent

IMAGE=${REGISTRY_PATH}:${CUSTOM_TAG}-agent

SECRET_NAME=sds-replicated-volume-module-registry
SECRET_DATA=$(create_secret)

kubectl -n d8-system scale deployment deckhouse --replicas=0

kubectl -n \$NAMESPACE patch secret \$SECRET_NAME -p \
 "{\"data\": {\".dockerconfigjson\": \"\$SECRET_DATA\"}}"

kubectl -n \$NAMESPACE patch daemonset \$DAEMONSET_NAME -p \
 "{\"spec\": {\"template\": {\"spec\": {\"containers\": [{\"name\": \"\$DAEMONSET_CONTAINER_NAME\", \"image\": \"\$IMAGE\"}]}}}}"
kubectl -n \$NAMESPACE patch daemonset \$DAEMONSET_NAME -p \
 "{\"spec\": {\"template\": {\"spec\": {\"containers\": [{\"name\": \"\$DAEMONSET_CONTAINER_NAME\", \"imagePullPolicy\": \"Always\"}]}}}}"
kubectl -n \$NAMESPACE rollout restart daemonset \$DAEMONSET_NAME
EOF
}

_base_images() {
  local ACTION=$1
  if [ "$ACTION" = 'get' ];then
    BASE_IMAGES_VERSION=$(grep -roP 'BASE_IMAGES_VERSION:\s+"v\d+\.\d+\.\d+"' | grep -oP 'v\d+\.\d+\.\d+' | head -n1)
    if [ -z "$BASE_IMAGES_VERSION" ];then
      echo "ERR: empty BASE_IMAGES_VERSION"
      exit 1
    fi
    echo BASE_IMAGES_VERSION=$BASE_IMAGES_VERSION
    curl -OJL https://fox.flant.com/api/v4/projects/deckhouse%2Fbase-images/packages/generic/base_images/${BASE_IMAGES_VERSION}/base_images.yml
  else
    rm -rf base_images.yml
  fi
}

if [ $# -lt 1 ]; then
  print_help
  exit 1
fi
ACTION=$1

shift

if [ $# -eq 0 ]; then
  IMAGE_NAMES=""
else
  IMAGE_NAMES="$@"
fi

case "$ACTION" in
  build)
    build_action
    ;;
  build_dev)
    build_dev_action
    ;;
  enable_deckhouse)
    enable_deckhouse
    ;;
  disable_deckhouse)
    disable_deckhouse
    ;;
  werf_install)
    werf_install
    ;;
  create_secret)
    create_secret
    ;;
  create_script_for_patch_agent)
    create_script_for_patch_agent
    ;;
  *)
    echo "Unknown action: $ACTION"
    print_help
    exit 1
    ;;
esac
